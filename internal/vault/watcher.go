package vault

import (
	"log/slog"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/fsnotify/fsnotify"
	"github.com/whiskeyjimbo/paras/internal/domain"
)

const defaultRescanInterval = 60 * time.Second

// renamePairWindow is how long we wait to pair a REMOVE with a CREATE (rename).
const renamePairWindow = 50 * time.Millisecond

var conflictPatterns = []*regexp.Regexp{
	regexp.MustCompile(`(?i) \(.*conflicted copy.*\)`), // Dropbox
	regexp.MustCompile(`(?i)\.sync-conflict-`),         // Syncthing
	regexp.MustCompile(`(?i) \(Google Docs\)`),         // Google Drive
	regexp.MustCompile(`(?i)~\$`),                      // MS Office temp
	regexp.MustCompile(`(?i)\.~lock\.`),                // LibreOffice lock
	regexp.MustCompile(`(?i)\.DS_Store$`),              // macOS
	regexp.MustCompile(`(?i)\.dropbox$`),               // Dropbox metadata
	regexp.MustCompile(`(?i)desktop\.ini$`),            // Windows
}

func isConflictFile(name string) bool {
	for _, re := range conflictPatterns {
		if re.MatchString(name) {
			return true
		}
	}
	return false
}

type watcher struct {
	vault         *LocalVault
	fw            *fsnotify.Watcher
	ticker        *time.Ticker
	done          chan struct{}
	wg            sync.WaitGroup
	syncConflicts atomic.Int64
	watcherStatus atomic.Value // string: "ok" | "limit_exceeded"

	// pending removes for rename-pair debounce: path -> deadline
	pendingMu sync.Mutex
	pending   map[string]time.Time
}

func newWatcher(v *LocalVault) *watcher {
	w := &watcher{
		vault:   v,
		done:    make(chan struct{}),
		pending: make(map[string]time.Time),
	}
	w.watcherStatus.Store("ok")
	return w
}

// start initializes fsnotify and begins the event loop. Falls back to rescan-only
// if inotify limits are exceeded.
func (w *watcher) start() {
	fw, err := fsnotify.NewWatcher()
	if err != nil {
		slog.Warn("fsnotify unavailable, falling back to rescan-only", "err", err)
		w.watcherStatus.Store("limit_exceeded")
		w.startRescanOnly()
		return
	}

	if err := w.addDirs(fw, w.vault.root); err != nil {
		slog.Warn("fsnotify watch failed, falling back to rescan-only", "err", err)
		fw.Close()
		w.watcherStatus.Store("limit_exceeded")
		w.startRescanOnly()
		return
	}

	w.fw = fw
	w.ticker = time.NewTicker(defaultRescanInterval)

	w.wg.Add(1)
	go w.loop()
}

func (w *watcher) startRescanOnly() {
	w.ticker = time.NewTicker(defaultRescanInterval)
	w.wg.Add(1)
	go w.rescanLoop()
}

func (w *watcher) close() {
	close(w.done)
	if w.fw != nil {
		w.fw.Close()
	}
	if w.ticker != nil {
		w.ticker.Stop()
	}
	w.wg.Wait()
}

func (w *watcher) addDirs(fw *fsnotify.Watcher, root string) error {
	return filepath.WalkDir(root, func(path string, d os.DirEntry, err error) error {
		if err != nil {
			return nil
		}
		if d.IsDir() {
			return fw.Add(path)
		}
		return nil
	})
}

func (w *watcher) loop() {
	defer w.wg.Done()
	for {
		select {
		case <-w.done:
			return
		case <-w.ticker.C:
			// Run in a separate goroutine so a slow scan doesn't block event handling.
			go w.vault.scanVault() //nolint:errcheck
		case event, ok := <-w.fw.Events:
			if !ok {
				return
			}
			w.handleEvent(event)
		case err, ok := <-w.fw.Errors:
			if !ok {
				return
			}
			slog.Warn("fsnotify error", "err", err)
		}
	}
}

func (w *watcher) rescanLoop() {
	defer w.wg.Done()
	for {
		select {
		case <-w.done:
			return
		case <-w.ticker.C:
			w.vault.scanVault() //nolint:errcheck
		}
	}
}

func (w *watcher) handleEvent(event fsnotify.Event) {
	name := event.Name
	base := filepath.Base(name)

	if isConflictFile(base) {
		w.syncConflicts.Add(1)
		if event.Has(fsnotify.Remove) {
			if canonical := resolveCanonicalSibling(name); canonical != "" {
				w.reindexFile(canonical)
			}
		}
		return
	}

	switch {
	case event.Has(fsnotify.Create):
		if info, err := os.Stat(name); err == nil && info.IsDir() {
			if w.fw != nil {
				w.fw.Add(name) //nolint:errcheck
			}
		}
		// Check if this CREATE pairs with a pending REMOVE (rename).
		w.pendingMu.Lock()
		var pairedOld string
		for old, deadline := range w.pending {
			if time.Now().Before(deadline) && isSameBase(old, name) {
				pairedOld = old
				break
			}
		}
		if pairedOld != "" {
			delete(w.pending, pairedOld)
		}
		w.pendingMu.Unlock()

		if pairedOld != "" {
			w.handleRename(pairedOld, name)
		} else {
			w.reindexFile(name)
		}

	case event.Has(fsnotify.Write):
		w.reindexFile(name)

	case event.Has(fsnotify.Remove), event.Has(fsnotify.Rename):
		w.pendingMu.Lock()
		_, alreadyPending := w.pending[name]
		w.pending[name] = time.Now().Add(renamePairWindow)
		w.pendingMu.Unlock()
		// Only spawn one AfterFunc per path; if a timer is already running it will
		// find the updated deadline and handle it.
		if !alreadyPending {
			time.AfterFunc(renamePairWindow, func() {
				select {
				case <-w.done:
					return
				default:
				}
				w.pendingMu.Lock()
				_, stillPending := w.pending[name]
				if stillPending {
					delete(w.pending, name)
				}
				w.pendingMu.Unlock()
				if stillPending {
					w.removeFromIndex(name)
				}
			})
		}
	}
}

func (w *watcher) reindexFile(absPath string) {
	if !isMDFile(absPath) {
		return
	}
	rel, err := filepath.Rel(w.vault.root, absPath)
	if err != nil {
		return
	}
	np, err := domain.Normalize(w.vault.root, filepath.ToSlash(rel), w.vault.caps.CaseSensitive)
	if err != nil {
		return
	}
	w.vault.indexNote(absPath, np)
}

func (w *watcher) removeFromIndex(absPath string) {
	if !isMDFile(absPath) {
		return
	}
	rel, err := filepath.Rel(w.vault.root, absPath)
	if err != nil {
		return
	}
	np, err := domain.Normalize(w.vault.root, filepath.ToSlash(rel), w.vault.caps.CaseSensitive)
	if err != nil {
		return
	}
	w.vault.mu.Lock()
	delete(w.vault.notes, np.IndexKey)
	w.vault.mu.Unlock()
	w.vault.idx.Remove(domain.NoteRef{Scope: w.vault.scope, Path: np.Storage})
}

func (w *watcher) handleRename(oldAbs, newAbs string) {
	if !isMDFile(newAbs) {
		w.removeFromIndex(oldAbs)
		return
	}
	oldRel, err := filepath.Rel(w.vault.root, oldAbs)
	if err != nil {
		return
	}
	oldNP, err := domain.Normalize(w.vault.root, filepath.ToSlash(oldRel), w.vault.caps.CaseSensitive)
	if err != nil {
		w.reindexFile(newAbs)
		return
	}

	newRel, err := filepath.Rel(w.vault.root, newAbs)
	if err != nil {
		return
	}
	newNP, err := domain.Normalize(w.vault.root, filepath.ToSlash(newRel), w.vault.caps.CaseSensitive)
	if err != nil {
		w.removeFromIndex(oldAbs)
		return
	}

	note, err := w.vault.readNote(newNP.Storage)
	if err != nil {
		return
	}
	note.Ref.Path = newNP.Storage
	s := w.vault.noteToSummary(note)

	w.vault.mu.Lock()
	delete(w.vault.notes, oldNP.IndexKey)
	w.vault.notes[newNP.IndexKey] = s
	w.vault.mu.Unlock()

	w.vault.idx.Remove(domain.NoteRef{Scope: w.vault.scope, Path: oldNP.Storage})
	w.vault.idx.Add(summaryToDoc(s, note.Body))
}

// isSameBase is a heuristic to pair REMOVE+CREATE events from atomic saves —
// editors often write to a temp file then rename to the target.
func isSameBase(old, new string) bool {
	return strings.EqualFold(filepath.Base(old), filepath.Base(new))
}

// resolveCanonicalSibling returns empty string if no conflict suffix is recognized.
func resolveCanonicalSibling(conflictPath string) string {
	dir := filepath.Dir(conflictPath)
	base := filepath.Base(conflictPath)
	ext := filepath.Ext(base)
	stem := strings.TrimSuffix(base, ext)
	for _, re := range conflictPatterns {
		cleaned := strings.TrimSpace(re.ReplaceAllString(stem, ""))
		if cleaned != stem && cleaned != "" {
			return filepath.Join(dir, cleaned+ext)
		}
	}
	return ""
}
