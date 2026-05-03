package postgresv

import (
	"context"
	"time"

	"github.com/whiskeyjimbo/para-mcp/internal/core/domain"
	"github.com/whiskeyjimbo/para-mcp/internal/infrastructure/storage/noteutil"
)

type noteRow struct {
	scope     string
	path      string
	body      string
	etag      string
	createdAt time.Time
	updatedAt time.Time
	title     string
	tags      []string
	status    string
	area      string
	project   string
}

func (r noteRow) toNote(caseSensitive bool) domain.Note {
	fm := domain.FrontMatter{
		Title:     r.title,
		Tags:      r.tags,
		Status:    r.status,
		Area:      r.area,
		Project:   r.project,
		CreatedAt: r.createdAt,
		UpdatedAt: r.updatedAt,
	}
	return domain.Note{
		Ref:         domain.NoteRef{Scope: r.scope, Path: r.path},
		FrontMatter: fm,
		Body:        r.body,
		ETag:        r.etag,
	}
}

func (v *PostgresVault) loadAll(ctx context.Context) error {
	rows, err := v.pool.Query(ctx,
		`SELECT scope, path, body, etag, created_at, updated_at,
		        title, tags, status, area, project
		 FROM notes
		 WHERE scope = $1 AND deleted_at IS NULL`,
		v.scope,
	)
	if err != nil {
		return err
	}
	defer rows.Close()

	for rows.Next() {
		var r noteRow
		if err := rows.Scan(
			&r.scope, &r.path, &r.body, &r.etag,
			&r.createdAt, &r.updatedAt,
			&r.title, &r.tags, &r.status, &r.area, &r.project,
		); err != nil {
			return err
		}
		note := r.toNote(false)
		s := note.Summary()
		ik := noteutil.IndexKey(r.path, false)
		links := noteutil.ParseLinks(r.body)
		v.cache.Set(ik, s)
		v.graph.Upsert(r.path, links)
		v.idx.Add(noteutil.SummaryToDoc(s, r.body))
	}
	return rows.Err()
}
