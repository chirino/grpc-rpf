package utils

import (
	"github.com/bep/debounce"
	"github.com/fsnotify/fsnotify"
	"time"
)

func WatchDir(dir string, checkForNewVersion func()) (Close func() error, err error) {

	watcher, err := fsnotify.NewWatcher()
	if err != nil {
		return nil, err
	}

	// Lets debounce to avoid generating too many change events when lots
	// of files are being updated in short time.
	debounced := debounce.New(500 * time.Millisecond)

	go func() {
		defer watcher.Close()
		for {
			select {
			case _, ok := <-watcher.Events:
				if !ok {
					return
				}
				debounced(checkForNewVersion)
			case <-watcher.Errors:
				return
			}
		}
	}()

	//// Watch all the dir for changes
	//dir, err := filepath.EvalSymlinks(dir)
	//if err != nil {
	//	_ = watcher.Close()
	//	return nil, err
	//}

	err = watcher.Add(dir)
	if err != nil {
		_ = watcher.Close()
		return nil, err
	}

	return watcher.Close, nil
}
