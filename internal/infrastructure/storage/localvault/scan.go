package localvault

import (
	"io/fs"
	"os"
	"path/filepath"

	"github.com/whiskeyjimbo/paras/internal/core/domain"
	"github.com/whiskeyjimbo/paras/internal/infrastructure/storage/noteutil"
)

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
		v.indexNote(path, np)
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
			// Atomic write: write to a sibling tmp file then rename, so concurrent
			// Get calls never observe a half-written (truncated) file.
			tmp := absPath + ".para_tmp"
			if werr := os.WriteFile(tmp, data, 0o644); werr == nil {
				_ = os.Rename(tmp, absPath)
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
	v.graph.Upsert(storagePath, links)
}

func (v *LocalVault) removeNoteFromAllIndexes(indexKey, storagePath string) {
	ref := domain.NoteRef{Scope: v.scope, Path: storagePath}
	v.cache.Delete(indexKey)
	v.graph.Remove(storagePath)
	v.idx.Remove(ref)
}
