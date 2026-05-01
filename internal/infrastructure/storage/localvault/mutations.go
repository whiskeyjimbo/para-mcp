package localvault

import (
	"context"
	"os"
	"path/filepath"

	"github.com/whiskeyjimbo/paras/internal/core/domain"
	"github.com/whiskeyjimbo/paras/internal/infrastructure/storage/noteutil"
)

func (v *LocalVault) Create(ctx context.Context, in domain.CreateInput) (domain.MutationResult, error) {
	np, err := v.normalizePath(in.Path)
	if err != nil {
		return domain.MutationResult{}, err
	}
	var result domain.MutationResult
	err = v.actors.Do(ctx, v.scope, np.Storage, func() error {
		absPath := filepath.Join(v.root, np.Storage)
		if err := os.MkdirAll(filepath.Dir(absPath), 0o755); err != nil {
			return err
		}
		in.FrontMatter.CreatedAt = v.clock().UTC()
		in.FrontMatter.UpdatedAt = in.FrontMatter.CreatedAt
		data, err := noteutil.FormatNote(in.FrontMatter, in.Body)
		if err != nil {
			return err
		}
		f, err := os.OpenFile(absPath, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0o644)
		if err != nil {
			if os.IsExist(err) {
				return domain.ErrConflict
			}
			return err
		}
		_, werr := f.Write(data)
		cerr := f.Close()
		if werr != nil {
			return werr
		}
		if cerr != nil {
			return cerr
		}
		note, err := v.readNote(np.Storage)
		if err != nil {
			return err
		}
		result = domain.MutationResult{Summary: note.Summary(), ETag: note.ETag}
		links := noteutil.ParseLinks(in.Body)
		v.upsertWithLinks(np.IndexKey, np.Storage, result.Summary, links)
		v.idx.Add(noteutil.SummaryToDoc(result.Summary, in.Body))
		return nil
	})
	return result, err
}

func (v *LocalVault) UpdateBody(ctx context.Context, path, body, ifMatch string) (domain.MutationResult, error) {
	np, err := v.normalizePath(path)
	if err != nil {
		return domain.MutationResult{}, err
	}
	var result domain.MutationResult
	err = v.actors.Do(ctx, v.scope, np.Storage, func() error {
		note, err := v.readNote(np.Storage)
		if err != nil {
			return err
		}
		if ifMatch != "" && note.ETag != ifMatch {
			return domain.ErrConflict
		}
		note.FrontMatter.UpdatedAt = v.clock().UTC()
		note.Body = body
		note.ETag = domain.ComputeETag(noteutil.CanonicalFrontMatterYAML(note.FrontMatter), body)
		data, err := noteutil.FormatNote(note.FrontMatter, body)
		if err != nil {
			return err
		}
		absPath := filepath.Join(v.root, np.Storage)
		if err := os.WriteFile(absPath, data, 0o644); err != nil {
			return err
		}
		result = domain.MutationResult{Summary: note.Summary(), ETag: note.ETag}
		links := noteutil.ParseLinks(body)
		v.upsertWithLinks(np.IndexKey, np.Storage, result.Summary, links)
		v.idx.Add(noteutil.SummaryToDoc(result.Summary, body))
		return nil
	})
	return result, err
}

func (v *LocalVault) PatchFrontMatter(ctx context.Context, path string, fields map[string]any, ifMatch string) (domain.MutationResult, error) {
	np, err := v.normalizePath(path)
	if err != nil {
		return domain.MutationResult{}, err
	}
	var result domain.MutationResult
	err = v.actors.Do(ctx, v.scope, np.Storage, func() error {
		note, err := v.readNote(np.Storage)
		if err != nil {
			return err
		}
		if ifMatch != "" && note.ETag != ifMatch {
			return domain.ErrConflict
		}
		domain.ApplyFrontMatterPatch(&note.FrontMatter, fields)
		note.FrontMatter.UpdatedAt = v.clock().UTC()
		note.ETag = domain.ComputeETag(noteutil.CanonicalFrontMatterYAML(note.FrontMatter), note.Body)
		data, err := noteutil.FormatNote(note.FrontMatter, note.Body)
		if err != nil {
			return err
		}
		if err := os.WriteFile(filepath.Join(v.root, np.Storage), data, 0o644); err != nil {
			return err
		}
		result = domain.MutationResult{Summary: note.Summary(), ETag: note.ETag}
		existingLinks := v.graph.Links(np.Storage)
		v.upsertWithLinks(np.IndexKey, np.Storage, result.Summary, existingLinks)
		return nil
	})
	return result, err
}

func (v *LocalVault) Move(ctx context.Context, path, newPath string, ifMatch string) (domain.MutationResult, error) {
	np, err := v.normalizePath(path)
	if err != nil {
		return domain.MutationResult{}, err
	}
	nnp, err := v.normalizePath(newPath)
	if err != nil {
		return domain.MutationResult{}, err
	}
	var result domain.MutationResult
	err = v.actors.Do(ctx, v.scope, np.Storage, func() error {
		note, err := v.readNote(np.Storage)
		if err != nil {
			return err
		}
		if ifMatch != "" && note.ETag != ifMatch {
			return domain.ErrConflict
		}
		newAbs := filepath.Join(v.root, nnp.Storage)
		if err := os.MkdirAll(filepath.Dir(newAbs), 0o755); err != nil {
			return err
		}
		if err := os.Rename(filepath.Join(v.root, np.Storage), newAbs); err != nil {
			return err
		}
		note.Ref.Path = nnp.Storage
		result = domain.MutationResult{Summary: note.Summary(), ETag: note.ETag}
		links := noteutil.ParseLinks(note.Body)
		v.cache.Move(np.IndexKey, nnp.IndexKey, result.Summary)
		v.graph.Remove(np.Storage)
		v.graph.Upsert(nnp.Storage, links)
		v.idx.Remove(domain.NoteRef{Scope: v.scope, Path: np.Storage})
		v.idx.Add(noteutil.SummaryToDoc(result.Summary, note.Body))
		return nil
	})
	return result, err
}

func (v *LocalVault) Delete(ctx context.Context, path string, soft bool, ifMatch string) error {
	np, err := v.normalizePath(path)
	if err != nil {
		return err
	}
	return v.actors.Do(ctx, v.scope, np.Storage, func() error {
		if ifMatch != "" {
			note, err := v.readNote(np.Storage)
			if err != nil {
				return err
			}
			if note.ETag != ifMatch {
				return domain.ErrConflict
			}
		}
		absPath := filepath.Join(v.root, np.Storage)
		if soft {
			trashPath := filepath.Join(v.root, ".trash", filepath.Base(np.Storage))
			if err := os.MkdirAll(filepath.Dir(trashPath), 0o755); err != nil {
				return err
			}
			if err := os.Rename(absPath, trashPath); err != nil {
				if os.IsNotExist(err) {
					return domain.ErrNotFound
				}
				return err
			}
		} else {
			if err := os.Remove(absPath); err != nil {
				if os.IsNotExist(err) {
					return domain.ErrNotFound
				}
				return err
			}
		}
		v.removeNoteFromAllIndexes(np.IndexKey, np.Storage)
		return nil
	})
}
