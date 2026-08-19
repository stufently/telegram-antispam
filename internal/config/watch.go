package config

import (
	"context"
	"log"
	"path/filepath"
	"time"

	"github.com/fsnotify/fsnotify"
)

// Watch reloads the config on writes to path until ctx is cancelled. A candidate
// that fails to parse or validate is logged and the running config is kept
// (tryReload never swaps in a bad config). Writes are debounced because editors
// often emit several events per save. Returns when ctx is cancelled.
func (s *Store) Watch(ctx context.Context, path string) error {
	w, err := fsnotify.NewWatcher()
	if err != nil {
		return err
	}
	defer func() { _ = w.Close() }()

	// Watch the parent directory, not the file: many editors replace the file
	// via rename, which invalidates a watch registered on the file itself.
	if err := w.Add(filepath.Dir(path)); err != nil {
		return err
	}

	target := filepath.Clean(path)
	var timer *time.Timer
	reload := func() {
		if err := s.tryReload(path); err != nil {
			log.Printf("config reload rejected, keeping running config: %v", err)
			return
		}
		log.Printf("config reloaded from %s", path)
	}

	for {
		select {
		case <-ctx.Done():
			return nil
		case ev, ok := <-w.Events:
			if !ok {
				return nil
			}
			if filepath.Clean(ev.Name) != target {
				continue
			}
			if ev.Op&(fsnotify.Write|fsnotify.Create|fsnotify.Rename) == 0 {
				continue
			}
			if timer != nil {
				timer.Stop()
			}
			timer = time.AfterFunc(150*time.Millisecond, reload)
		case err, ok := <-w.Errors:
			if !ok {
				return nil
			}
			log.Printf("config watcher error: %v", err)
		}
	}
}
