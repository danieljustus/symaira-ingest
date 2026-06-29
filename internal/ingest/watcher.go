package ingest

import (
	"context"
	"fmt"
	"io/fs"
	"log"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"github.com/fsnotify/fsnotify"
	"github.com/danieljustus/symaira-ingest/internal/extract"
	"github.com/danieljustus/symaira-ingest/internal/store"
)

// Watcher monitors a directory for files to ingest.
type Watcher struct {
	store    *store.Store
	inboxDir string
	watcher  *fsnotify.Watcher
	pending  map[string]*fileState
	mu       sync.Mutex
}

type fileState struct {
	lastSize    int64
	lastModTime time.Time
	createdAt   time.Time
	timer       *time.Timer
}

// NewWatcher creates a new Watcher instance for the given inbox directory.
func NewWatcher(s *store.Store, inboxDir string) (*Watcher, error) {
	inboxDir = filepath.Clean(inboxDir)
	fw, err := fsnotify.NewWatcher()
	if err != nil {
		return nil, fmt.Errorf("create fsnotify watcher: %w", err)
	}

	return &Watcher{
		store:    s,
		inboxDir: inboxDir,
		watcher:  fw,
		pending:  make(map[string]*fileState),
	}, nil
}

// Close closes the underlying fsnotify watcher.
func (w *Watcher) Close() error {
	return w.watcher.Close()
}

// Start initiates directory watching and processes events until context cancellation.
func (w *Watcher) Start(ctx context.Context) error {
	// 1. Initial recursive scan and watch setup
	err := filepath.WalkDir(w.inboxDir, func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if d.IsDir() {
			if isIgnored(path) {
				return filepath.SkipDir
			}
			log.Printf("[Watcher] Watching directory: %s", path)
			if err := w.watcher.Add(path); err != nil {
				return fmt.Errorf("watch dir %s: %w", path, err)
			}
		} else {
			if !isIgnored(path) {
				w.debounceFile(ctx, path)
			}
		}
		return nil
	})
	if err != nil {
		w.watcher.Close()
		return fmt.Errorf("initial watch setup walk: %w", err)
	}

	// 2. Event loop
	go func() {
		defer w.watcher.Close()
		defer func() {
			w.mu.Lock()
			for _, state := range w.pending {
				if state.timer != nil {
					state.timer.Stop()
				}
			}
			w.pending = make(map[string]*fileState)
			w.mu.Unlock()
		}()

		for {
			select {
			case <-ctx.Done():
				log.Println("[Watcher] Shutting down event loop...")
				return
			case event, ok := <-w.watcher.Events:
				if !ok {
					return
				}
				if isIgnored(event.Name) {
					continue
				}

				// If a new directory is created, watch it recursively
				if event.Op&fsnotify.Create == fsnotify.Create {
					info, err := os.Stat(event.Name)
					if err == nil && info.IsDir() {
						log.Printf("[Watcher] New directory detected, adding to watch: %s", event.Name)
						w.watchDirectoryRecursive(ctx, event.Name)
						continue
					}
				}

				// Handle file modifications/creations
				if event.Op&(fsnotify.Create|fsnotify.Write) != 0 {
					w.debounceFile(ctx, event.Name)
				}

				// Handle removals
				if event.Op&(fsnotify.Remove|fsnotify.Rename) != 0 {
					w.cancelDebounce(event.Name)
				}

			case err, ok := <-w.watcher.Errors:
				if !ok {
					return
				}
				log.Printf("[Watcher] fsnotify error: %v", err)
			}
		}
	}()

	return nil
}

func (w *Watcher) watchDirectoryRecursive(ctx context.Context, dir string) {
	_ = filepath.WalkDir(dir, func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return nil
		}
		if d.IsDir() {
			if isIgnored(path) {
				return filepath.SkipDir
			}
			log.Printf("[Watcher] Recursively watching directory: %s", path)
			_ = w.watcher.Add(path)
		} else {
			if !isIgnored(path) {
				w.debounceFile(ctx, path)
			}
		}
		return nil
	})
}

func (w *Watcher) debounceFile(ctx context.Context, path string) {
	w.mu.Lock()
	defer w.mu.Unlock()

	info, err := os.Stat(path)
	if err != nil {
		// File might have been deleted, clean up
		if state, ok := w.pending[path]; ok {
			if state.timer != nil {
				state.timer.Stop()
			}
			delete(w.pending, path)
		}
		return
	}

	if state, ok := w.pending[path]; ok {
		if state.timer != nil {
			state.timer.Stop()
		}
		state.lastSize = info.Size()
		state.lastModTime = info.ModTime()
	} else {
		w.pending[path] = &fileState{
			lastSize:    info.Size(),
			lastModTime: info.ModTime(),
			createdAt:   time.Now(),
		}
	}

	state := w.pending[path]
	state.timer = time.AfterFunc(1*time.Second, func() {
		w.checkStability(ctx, path)
	})
}

func (w *Watcher) checkStability(ctx context.Context, path string) {
	w.mu.Lock()
	state, exists := w.pending[path]
	if !exists {
		w.mu.Unlock()
		return
	}

	const maxPendingAge = 5 * time.Minute
	if time.Since(state.createdAt) > maxPendingAge {
		delete(w.pending, path)
		if state.timer != nil {
			state.timer.Stop()
		}
		w.mu.Unlock()

		log.Printf("[Watcher] File pending longer than %v, force-enqueuing: %s", maxPendingAge, path)
		if err := w.enqueueFile(ctx, path); err != nil {
			log.Printf("[Watcher] Error force-enqueuing file %s: %v", path, err)
		}
		return
	}

	info, err := os.Stat(path)
	if err != nil {
		delete(w.pending, path)
		w.mu.Unlock()
		return
	}

	if info.IsDir() {
		delete(w.pending, path)
		w.mu.Unlock()
		return
	}

	currentSize := info.Size()
	currentModTime := info.ModTime()

	if currentSize == state.lastSize && currentModTime.Equal(state.lastModTime) {
		delete(w.pending, path)
		w.mu.Unlock()

		log.Printf("[Watcher] File is stable, submitting to queue: %s", path)
		if err := w.enqueueFile(ctx, path); err != nil {
			log.Printf("[Watcher] Error enqueuing file %s: %v", path, err)
		}
		return
	}

	state.lastSize = currentSize
	state.lastModTime = currentModTime
	state.timer = time.AfterFunc(1*time.Second, func() {
		w.checkStability(ctx, path)
	})
	w.mu.Unlock()
}

func (w *Watcher) cancelDebounce(path string) {
	w.mu.Lock()
	defer w.mu.Unlock()

	if state, ok := w.pending[path]; ok {
		if state.timer != nil {
			state.timer.Stop()
		}
		delete(w.pending, path)
	}
}

func (w *Watcher) enqueueFile(ctx context.Context, path string) error {
	kind, err := extract.Detect(path)
	if err != nil {
		return fmt.Errorf("detect file kind: %w", err)
	}

	hash, err := hashFile(path)
	if err != nil {
		return fmt.Errorf("hash file: %w", err)
	}

	doc, created, err := w.store.CreateOrGet(ctx, path, hash, string(kind))
	if err != nil {
		return fmt.Errorf("create or get document: %w", err)
	}

	if !created {
		log.Printf("[Watcher] File %s (hash %s) already ingested or enqueued. Recording skipped job.", path, hash)
		if _, err := w.store.EnqueueSkippedJob(ctx, doc.ID, string(kind), "duplicate"); err != nil {
			return fmt.Errorf("enqueue skipped job: %w", err)
		}
		return nil
	}

	_, err = w.store.EnqueueJob(ctx, doc.ID, string(kind))
	if err != nil {
		return fmt.Errorf("enqueue job: %w", err)
	}

	return nil
}

func isIgnored(path string) bool {
	base := filepath.Base(path)
	if strings.HasPrefix(base, ".") {
		return true
	}
	if strings.HasSuffix(base, ".swp") || strings.HasSuffix(base, ".swx") {
		return true
	}
	if strings.HasSuffix(base, ".tmp") || strings.HasSuffix(base, "~") {
		return true
	}
	return false
}
