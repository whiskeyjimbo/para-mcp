package localvault

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/whiskeyjimbo/para-mcp/internal/core/domain"
	"github.com/whiskeyjimbo/para-mcp/internal/infrastructure/storage/noteutil"
)

func (v *LocalVault) normalizePath(path string) (domain.NormalizedPath, error) {
	np, err := domain.Normalize(path, v.caps.CaseSensitive)
	if err != nil {
		return domain.NormalizedPath{}, err
	}
	if err := checkSymlinks(v.root, np.Storage); err != nil {
		return domain.NormalizedPath{}, err
	}
	return np, nil
}

func (v *LocalVault) readNote(storagePath string) (domain.Note, error) {
	absPath := filepath.Join(v.root, storagePath)
	data, err := os.ReadFile(absPath)
	if err != nil {
		if os.IsNotExist(err) {
			return domain.Note{}, domain.ErrNotFound
		}
		return domain.Note{}, err
	}
	fm, body, err := noteutil.ParseNote(data)
	if err != nil {
		return domain.Note{}, err
	}
	note := domain.Note{
		Ref:         domain.NoteRef{Scope: v.scope, Path: storagePath},
		FrontMatter: fm,
		Body:        body,
	}
	note.ETag = domain.ComputeETag(noteutil.CanonicalFrontMatterYAML(fm), body)
	return note, nil
}

func checkSymlinks(vaultRoot, path string) error {
	abs := filepath.Join(vaultRoot, path)
	real, err := filepath.EvalSymlinks(abs)
	if err != nil {
		real, err = evalSymlinksPartial(abs)
		if err != nil {
			return nil
		}
	}
	vaultReal, err := filepath.EvalSymlinks(vaultRoot)
	if err != nil {
		return nil
	}
	prefix := vaultReal + string(filepath.Separator)
	if real != vaultReal && !strings.HasPrefix(real, prefix) {
		return fmt.Errorf("path resolves outside vault root")
	}
	return nil
}

func evalSymlinksPartial(abs string) (string, error) {
	p := abs
	for {
		parent := filepath.Dir(p)
		if parent == p {
			return "", errors.New("no existing ancestor found")
		}
		p = parent
		real, err := filepath.EvalSymlinks(p)
		if err == nil {
			return real, nil
		}
	}
}

func probeCaseSensitivity(root string) bool {
	probe := filepath.Join(root, ".para_case_probe")
	_ = os.WriteFile(probe, []byte{}, 0o600)
	defer os.Remove(probe)
	_, err := os.Stat(strings.ToUpper(probe))
	return os.IsNotExist(err)
}
