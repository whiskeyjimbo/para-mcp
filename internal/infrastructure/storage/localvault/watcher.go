package localvault

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
)

const defaultRescanInterval = 60 * time.Second
const defaultRenamePairWindow = 50 * time.Millisecond

// DefaultConflictPatterns is the default set of filename patterns treated as
// sync-conflict or OS-metadata files that should be ignored by the watcher.
var DefaultConflictPatterns = []*regexp.Regexp{
	regexp.MustCompile(`(?i) \(.*conflicted copy.*\)`),
	regexp.MustCompile(`(?i)\.sync-conflict-`),
	regexp.MustCompile(`(?i) \(Google Docs\)`),
	regexp.MustCompile(`(?i)~\$`),
	regexp.MustCompile(`(?i)\.~lock\.`),
	regexp.MustCompile(`(?i)\.DS_Store$`),
	regexp.MustCompile(`(?i)\.dropbox$`),
	regexp.MustCompile(`(?i)desktop\.ini$`),
}

// VaultIndexer is the narrow interface the watcher needs from LocalVault.
type VaultIndexer interface {
	IndexFile(absPath string)
	RemoveFile(absPath string)
	RescanVault() error
}

type watcher struct {
	v             VaultIndexer
	root          string
	log           *slog.Logger
	fw            *fsnotify.Watcher
	ticker        *time.Ticker
	done          chan struct{}
	wg            sync.WaitGroup
	syncConflicts atomic.Int64
	watcherStatus atomic.Value
	rescanActive  atomic.Bool
	// dirty is set when fsnotify errors or missed events are suspected; the
	// periodic rescan is skipped when false (watcher is healthy).
	dirty            atomic.Bool
	conflicts        *ConflictDetector
	rescanInterval   time.Duration
	renamePairWindow time.Duration

	renames *renamePairTracker
}

func newWatcher(v VaultIndexer, log *slog.Logger, root string, conflictPatterns []*regexp.Regexp, rescanInterval, renamePairWin time.Duration) *watcher {
	w := &watcher{
		v:                v,
		root:             root,
		log:              log,
		done:             make(chan struct{}),
		renames:          newRenamePairTracker(renamePairWin),
		conflicts:        newConflictDetector(conflictPatterns),
		rescanInterval:   rescanInterval,
		renamePairWindow: renamePairWin,
	}
	w.watcherStatus.Store("ok")
	return w
}

func (w *watcher) start() {
	fw, err := fsnotify.NewWatcher()
	if err != nil {
		w.log.Warn("fsnotify unavailable, falling back to rescan-only", "err", err)
		w.watcherStatus.Store("limit_exceeded")
		w.startRescanOnly()
		return
	}

	if err := w.addDirs(fw, w.root); err != nil {
		w.log.Warn("fsnotify watch failed, falling back to rescan-only", "err", err)
		fw.Close()
		w.watcherStatus.Store("limit_exceeded")
		w.startRescanOnly()
		return
	}

	w.fw = fw
	w.ticker = time.NewTicker(w.rescanInterval)

	w.wg.Add(1)
	go w.loop()
}

func (w *watcher) startRescanOnly() {
	w.ticker = time.NewTicker(w.rescanInterval)
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
			// Skip rescan when the fsnotify watcher is healthy and no events
			// have been missed. dirty is set on fsnotify errors or suspected misses.
			if !w.dirty.CompareAndSwap(true, false) {
				continue
			}
			if w.rescanActive.CompareAndSwap(false, true) {
				go func() {
					defer w.rescanActive.Store(false)
					w.v.RescanVault() //nolint:errcheck
				}()
			}
		case event, ok := <-w.fw.Events:
			if !ok {
				return
			}
			w.handleEvent(event)
		case err, ok := <-w.fw.Errors:
			if !ok {
				return
			}
			w.log.Warn("fsnotify error", "err", err)
			w.dirty.Store(true)
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
			w.v.RescanVault() //nolint:errcheck
		}
	}
}

func (w *watcher) handleEvent(event fsnotify.Event) {
	name := event.Name
	base := filepath.Base(name)

	if w.conflicts.IsConflict(base) {
		w.syncConflicts.Add(1)
		if event.Has(fsnotify.Remove) {
			if canonical := w.conflicts.ResolveCanonical(name); canonical != "" {
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
		if pairedOld := w.renames.FindPairedRemoval(name); pairedOld != "" {
			w.handleRename(pairedOld, name)
		} else {
			w.reindexFile(name)
		}

	case event.Has(fsnotify.Write):
		w.reindexFile(name)

	case event.Has(fsnotify.Remove), event.Has(fsnotify.Rename):
		// Rename/remove events may indicate the watcher missed subsequent events
		// (e.g., rapid create after rename). Mark dirty to trigger a safety rescan.
		w.dirty.Store(true)
		if !w.renames.MarkRemoved(name) {
			time.AfterFunc(w.renamePairWindow, func() {
				select {
				case <-w.done:
					return
				default:
				}
				if w.renames.ClaimIfPending(name) {
					w.removeFromIndex(name)
				}
			})
		}
	}
}

func (w *watcher) reindexFile(absPath string) {
	w.v.IndexFile(absPath)
}

func (w *watcher) removeFromIndex(absPath string) {
	w.v.RemoveFile(absPath)
}

func (w *watcher) handleRename(oldAbs, newAbs string) {
	w.v.RemoveFile(oldAbs)
	w.v.IndexFile(newAbs)
}

func isSameBase(old, new string) bool {
	return strings.EqualFold(filepath.Base(old), filepath.Base(new))
}
