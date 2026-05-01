package localvault

import (
	"context"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"sync/atomic"

	"github.com/whiskeyjimbo/paras/internal/core/domain"
	"github.com/whiskeyjimbo/paras/internal/infrastructure/storage/noteutil"
)

// indexSeq is a per-process monotonic counter that gives each indexNote
// write-back its own unique tmp file name, preventing concurrent writes
// to the same .para_tmp file.
var indexSeq atomic.Uint64

// RescanVault re-walks the vault root and rebuilds all indexes.
func (v *LocalVault) RescanVault() error { return v.scanVault() }

func (v *LocalVault) scanVault() error {
	return filepath.WalkDir(v.root, func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if d.IsDir() || !noteutil.IsMDFile(path) {
			return nil
		}
		rel, err := filepath.Rel(v.root, path)
		if err != nil {
			return err
		}
		rel = filepath.ToSlash(rel)
		np, err := v.normalizePath(rel)
		if err != nil {
			return nil
		}
		// Use the actor pool to serialize with concurrent IndexFile calls from the
		// fsnotify watcher. Without this, both can enter indexNote concurrently for
		// the same path, racing on the .para_tmp write-back.
		_ = v.actors.Do(context.Background(), v.scope, np.Storage, func() error {
			v.indexNote(path, np)
			return nil
		})
		return nil
	})
}

func (v *LocalVault) indexNote(absPath string, np domain.NormalizedPath) {
	note, err := v.readNote(np.Storage)
	if err != nil {
		return
	}
	if domain.GetNoteID(note.FrontMatter) == "" {
		domain.SetNoteID(&note.FrontMatter, domain.DeriveNoteID(np.Storage, note.ETag))
		if data, err := noteutil.FormatNote(note.FrontMatter, note.Body); err == nil {
			// Atomic write: write to a uniquely-named sibling tmp file then rename.
			// The unique suffix (per-process counter) ensures concurrent indexNote
			// calls for the same path don't clobber each other's tmp files.
			tmp := fmt.Sprintf("%s.para_tmp.%d", absPath, indexSeq.Add(1))
			if werr := os.WriteFile(tmp, data, 0o644); werr == nil {
				if rerr := os.Rename(tmp, absPath); rerr != nil {
					_ = os.Remove(tmp) // clean up if rename failed
				}
			}
		}
	}
	s := note.Summary()
	links := noteutil.ParseLinks(note.Body)
	v.upsertWithLinks(np.IndexKey, np.Storage, s, links)
	v.idx.Add(noteutil.SummaryToDoc(s, note.Body))
}

func (v *LocalVault) upsertWithLinks(indexKey, storagePath string, s domain.NoteSummary, links []noteutil.OutLink) {
	v.cache.Set(indexKey, s)
	old := v.graph.Links(storagePath)
	v.graph.UpsertDiff(storagePath, old, links)
}

func (v *LocalVault) removeNoteFromAllIndexes(indexKey, storagePath string) {
	ref := domain.NoteRef{Scope: v.scope, Path: storagePath}
	v.cache.Delete(indexKey)
	v.graph.Remove(storagePath)
	v.idx.Remove(ref)
}
