package postgresv

import (
	"context"
	"fmt"

	"github.com/whiskeyjimbo/paras/internal/core/domain"
	"github.com/whiskeyjimbo/paras/internal/infrastructure/storage/noteutil"
)

func (v *PostgresVault) Create(ctx context.Context, in domain.CreateInput) (domain.MutationResult, error) {
	np, err := domain.Normalize(in.Path, false)
	if err != nil {
		return domain.MutationResult{}, err
	}

	now := v.clock()
	in.FrontMatter.CreatedAt = now
	in.FrontMatter.UpdatedAt = now
	etag := domain.ComputeETag(noteutil.CanonicalFrontMatterYAML(in.FrontMatter), in.Body)

	tx, err := v.pool.Begin(ctx)
	if err != nil {
		return domain.MutationResult{}, err
	}
	defer tx.Rollback(ctx) //nolint:errcheck

	// Check for existing row (including soft-deleted) with FOR UPDATE.
	var existsDeleted bool
	err = tx.QueryRow(ctx,
		`SELECT deleted_at IS NOT NULL FROM notes WHERE scope=$1 AND path=$2 FOR UPDATE`,
		v.scope, np.Storage,
	).Scan(&existsDeleted)
	if err == nil {
		if !existsDeleted {
			return domain.MutationResult{}, domain.ErrConflict
		}
		// Reclaim soft-deleted slot.
		_, err = tx.Exec(ctx,
			`UPDATE notes SET body=$3, etag=$4, created_at=$5, updated_at=$5,
			        deleted_at=NULL, title=$6, tags=$7, status=$8, area=$9, project=$10
			 WHERE scope=$1 AND path=$2`,
			v.scope, np.Storage, in.Body, etag, now,
			in.FrontMatter.Title, in.FrontMatter.Tags,
			in.FrontMatter.Status, in.FrontMatter.Area, in.FrontMatter.Project,
		)
	} else if isNoRows(err) {
		_, err = tx.Exec(ctx,
			`INSERT INTO notes (scope, path, body, etag, created_at, updated_at,
			                    title, tags, status, area, project)
			 VALUES ($1, $2, $3, $4, $5, $5, $6, $7, $8, $9, $10)`,
			v.scope, np.Storage, in.Body, etag, now,
			in.FrontMatter.Title, in.FrontMatter.Tags,
			in.FrontMatter.Status, in.FrontMatter.Area, in.FrontMatter.Project,
		)
	}
	if err != nil {
		return domain.MutationResult{}, err
	}
	if err := tx.Commit(ctx); err != nil {
		return domain.MutationResult{}, err
	}

	note := domain.Note{
		Ref:         domain.NoteRef{Scope: v.scope, Path: np.Storage},
		FrontMatter: in.FrontMatter,
		Body:        in.Body,
		ETag:        etag,
	}
	result := domain.MutationResult{Summary: note.Summary(), ETag: etag}
	ik := noteutil.IndexKey(np.Storage, false)
	links := noteutil.ParseLinks(in.Body)
	v.cache.Set(ik, result.Summary)
	v.graph.Upsert(np.Storage, links)
	v.idx.Add(noteutil.SummaryToDoc(result.Summary, in.Body))
	return result, nil
}

func (v *PostgresVault) UpdateBody(ctx context.Context, path, body, ifMatch string) (domain.MutationResult, error) {
	np, err := domain.Normalize(path, false)
	if err != nil {
		return domain.MutationResult{}, err
	}

	tx, err := v.pool.Begin(ctx)
	if err != nil {
		return domain.MutationResult{}, err
	}
	defer tx.Rollback(ctx) //nolint:errcheck

	var r noteRow
	err = tx.QueryRow(ctx,
		`SELECT scope, path, body, etag, created_at, updated_at,
		        title, tags, status, area, project
		 FROM notes WHERE scope=$1 AND path=$2 AND deleted_at IS NULL FOR UPDATE`,
		v.scope, np.Storage,
	).Scan(&r.scope, &r.path, &r.body, &r.etag,
		&r.createdAt, &r.updatedAt,
		&r.title, &r.tags, &r.status, &r.area, &r.project,
	)
	if err != nil {
		if isNoRows(err) {
			return domain.MutationResult{}, domain.ErrNotFound
		}
		return domain.MutationResult{}, err
	}
	if ifMatch != "" && r.etag != ifMatch {
		return domain.MutationResult{}, domain.ErrConflict
	}

	now := v.clock()
	note := r.toNote(false)
	note.FrontMatter.UpdatedAt = now
	note.Body = body
	etag := domain.ComputeETag(noteutil.CanonicalFrontMatterYAML(note.FrontMatter), body)

	_, err = tx.Exec(ctx,
		`UPDATE notes SET body=$3, etag=$4, updated_at=$5 WHERE scope=$1 AND path=$2`,
		v.scope, np.Storage, body, etag, now,
	)
	if err != nil {
		return domain.MutationResult{}, err
	}
	if err := tx.Commit(ctx); err != nil {
		return domain.MutationResult{}, err
	}

	note.ETag = etag
	result := domain.MutationResult{Summary: note.Summary(), ETag: etag}
	ik := noteutil.IndexKey(np.Storage, false)
	links := noteutil.ParseLinks(body)
	v.cache.Set(ik, result.Summary)
	v.graph.Upsert(np.Storage, links)
	v.idx.Add(noteutil.SummaryToDoc(result.Summary, body))
	return result, nil
}

func (v *PostgresVault) PatchFrontMatter(ctx context.Context, path string, fields map[string]any, ifMatch string) (domain.MutationResult, error) {
	np, err := domain.Normalize(path, false)
	if err != nil {
		return domain.MutationResult{}, err
	}

	tx, err := v.pool.Begin(ctx)
	if err != nil {
		return domain.MutationResult{}, err
	}
	defer tx.Rollback(ctx) //nolint:errcheck

	var r noteRow
	err = tx.QueryRow(ctx,
		`SELECT scope, path, body, etag, created_at, updated_at,
		        title, tags, status, area, project
		 FROM notes WHERE scope=$1 AND path=$2 AND deleted_at IS NULL FOR UPDATE`,
		v.scope, np.Storage,
	).Scan(&r.scope, &r.path, &r.body, &r.etag,
		&r.createdAt, &r.updatedAt,
		&r.title, &r.tags, &r.status, &r.area, &r.project,
	)
	if err != nil {
		if isNoRows(err) {
			return domain.MutationResult{}, domain.ErrNotFound
		}
		return domain.MutationResult{}, err
	}
	if ifMatch != "" && r.etag != ifMatch {
		return domain.MutationResult{}, domain.ErrConflict
	}

	note := r.toNote(false)
	domain.ApplyFrontMatterPatch(&note.FrontMatter, fields)
	note.FrontMatter.UpdatedAt = v.clock()
	etag := domain.ComputeETag(noteutil.CanonicalFrontMatterYAML(note.FrontMatter), note.Body)

	_, err = tx.Exec(ctx,
		`UPDATE notes SET etag=$3, updated_at=$4,
		        title=$5, tags=$6, status=$7, area=$8, project=$9
		 WHERE scope=$1 AND path=$2`,
		v.scope, np.Storage, etag, note.FrontMatter.UpdatedAt,
		note.FrontMatter.Title, note.FrontMatter.Tags,
		note.FrontMatter.Status, note.FrontMatter.Area, note.FrontMatter.Project,
	)
	if err != nil {
		return domain.MutationResult{}, err
	}
	if err := tx.Commit(ctx); err != nil {
		return domain.MutationResult{}, err
	}

	note.ETag = etag
	result := domain.MutationResult{Summary: note.Summary(), ETag: etag}
	ik := noteutil.IndexKey(np.Storage, false)
	existingLinks := v.graph.Links(np.Storage)
	v.cache.Set(ik, result.Summary)
	v.graph.Upsert(np.Storage, existingLinks)
	return result, nil
}

func (v *PostgresVault) Replace(ctx context.Context, path string, fields map[string]any, body, ifMatch string) (domain.MutationResult, error) {
	np, err := domain.Normalize(path, false)
	if err != nil {
		return domain.MutationResult{}, err
	}

	tx, err := v.pool.Begin(ctx)
	if err != nil {
		return domain.MutationResult{}, err
	}
	defer tx.Rollback(ctx) //nolint:errcheck

	var r noteRow
	err = tx.QueryRow(ctx,
		`SELECT scope, path, body, etag, created_at, updated_at,
		        title, tags, status, area, project
		 FROM notes WHERE scope=$1 AND path=$2 AND deleted_at IS NULL FOR UPDATE`,
		v.scope, np.Storage,
	).Scan(&r.scope, &r.path, &r.body, &r.etag,
		&r.createdAt, &r.updatedAt,
		&r.title, &r.tags, &r.status, &r.area, &r.project,
	)
	if err != nil {
		if isNoRows(err) {
			return domain.MutationResult{}, domain.ErrNotFound
		}
		return domain.MutationResult{}, err
	}
	if ifMatch != "" && r.etag != ifMatch {
		return domain.MutationResult{}, domain.ErrConflict
	}

	now := v.clock()
	note := r.toNote(false)
	domain.ApplyFrontMatterPatch(&note.FrontMatter, fields)
	note.FrontMatter.UpdatedAt = now
	note.Body = body
	etag := domain.ComputeETag(noteutil.CanonicalFrontMatterYAML(note.FrontMatter), body)

	_, err = tx.Exec(ctx,
		`UPDATE notes SET body=$3, etag=$4, updated_at=$5,
		        title=$6, tags=$7, status=$8, area=$9, project=$10
		 WHERE scope=$1 AND path=$2`,
		v.scope, np.Storage, body, etag, now,
		note.FrontMatter.Title, note.FrontMatter.Tags,
		note.FrontMatter.Status, note.FrontMatter.Area, note.FrontMatter.Project,
	)
	if err != nil {
		return domain.MutationResult{}, err
	}
	if err := tx.Commit(ctx); err != nil {
		return domain.MutationResult{}, err
	}

	note.ETag = etag
	result := domain.MutationResult{Summary: note.Summary(), ETag: etag}
	ik := noteutil.IndexKey(np.Storage, false)
	links := noteutil.ParseLinks(body)
	v.cache.Set(ik, result.Summary)
	v.graph.Upsert(np.Storage, links)
	v.idx.Add(noteutil.SummaryToDoc(result.Summary, body))
	return result, nil
}

func (v *PostgresVault) Move(ctx context.Context, path, newPath string, ifMatch string) (domain.MutationResult, error) {
	np, err := domain.Normalize(path, false)
	if err != nil {
		return domain.MutationResult{}, err
	}
	nnp, err := domain.Normalize(newPath, false)
	if err != nil {
		return domain.MutationResult{}, err
	}

	tx, err := v.pool.Begin(ctx)
	if err != nil {
		return domain.MutationResult{}, err
	}
	defer tx.Rollback(ctx) //nolint:errcheck

	var r noteRow
	err = tx.QueryRow(ctx,
		`SELECT scope, path, body, etag, created_at, updated_at,
		        title, tags, status, area, project
		 FROM notes WHERE scope=$1 AND path=$2 AND deleted_at IS NULL FOR UPDATE`,
		v.scope, np.Storage,
	).Scan(&r.scope, &r.path, &r.body, &r.etag,
		&r.createdAt, &r.updatedAt,
		&r.title, &r.tags, &r.status, &r.area, &r.project,
	)
	if err != nil {
		if isNoRows(err) {
			return domain.MutationResult{}, domain.ErrNotFound
		}
		return domain.MutationResult{}, err
	}
	if ifMatch != "" && r.etag != ifMatch {
		return domain.MutationResult{}, domain.ErrConflict
	}

	_, err = tx.Exec(ctx,
		`UPDATE notes SET path=$3 WHERE scope=$1 AND path=$2`,
		v.scope, np.Storage, nnp.Storage,
	)
	if err != nil {
		return domain.MutationResult{}, fmt.Errorf("move note: %w", err)
	}
	if err := tx.Commit(ctx); err != nil {
		return domain.MutationResult{}, err
	}

	note := r.toNote(false)
	note.Ref.Path = nnp.Storage
	result := domain.MutationResult{Summary: note.Summary(), ETag: r.etag}

	links := noteutil.ParseLinks(r.body)
	oldKey := noteutil.IndexKey(np.Storage, false)
	newKey := noteutil.IndexKey(nnp.Storage, false)
	v.cache.Move(oldKey, newKey, result.Summary)
	v.graph.Remove(np.Storage)
	v.graph.Upsert(nnp.Storage, links)
	v.idx.Remove(domain.NoteRef{Scope: v.scope, Path: np.Storage})
	v.idx.Add(noteutil.SummaryToDoc(result.Summary, r.body))
	return result, nil
}

func (v *PostgresVault) Delete(ctx context.Context, path string, soft bool, ifMatch string) error {
	np, err := domain.Normalize(path, false)
	if err != nil {
		return err
	}

	tx, err := v.pool.Begin(ctx)
	if err != nil {
		return err
	}
	defer tx.Rollback(ctx) //nolint:errcheck

	if ifMatch != "" {
		var etag string
		err = tx.QueryRow(ctx,
			`SELECT etag FROM notes WHERE scope=$1 AND path=$2 AND deleted_at IS NULL FOR UPDATE`,
			v.scope, np.Storage,
		).Scan(&etag)
		if err != nil {
			if isNoRows(err) {
				return domain.ErrNotFound
			}
			return err
		}
		if etag != ifMatch {
			return domain.ErrConflict
		}
	}

	var rowsAffected int64
	if soft {
		ct, e := tx.Exec(ctx,
			`UPDATE notes SET deleted_at=now() WHERE scope=$1 AND path=$2 AND deleted_at IS NULL`,
			v.scope, np.Storage,
		)
		err, rowsAffected = e, ct.RowsAffected()
	} else {
		ct, e := tx.Exec(ctx,
			`DELETE FROM notes WHERE scope=$1 AND path=$2`,
			v.scope, np.Storage,
		)
		err, rowsAffected = e, ct.RowsAffected()
	}
	if err != nil {
		return err
	}
	if rowsAffected == 0 {
		return domain.ErrNotFound
	}
	if err := tx.Commit(ctx); err != nil {
		return err
	}

	ik := noteutil.IndexKey(np.Storage, false)
	v.cache.Delete(ik)
	v.graph.Remove(np.Storage)
	v.idx.Remove(domain.NoteRef{Scope: v.scope, Path: np.Storage})
	return nil
}
