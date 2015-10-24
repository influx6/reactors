package fs

import (
	"os"
	"sync"
	"testing"
	"time"

	"github.com/influx6/flux"
)

func TestWatch(t *testing.T) {
	ws := new(sync.WaitGroup)
	ws.Add(2)

	watcher := Watch(WatchConfig{
		Path: "../fixtures",
	})

	watcher.React(func(r flux.Reactor, err error, ev interface{}) {
		ws.Done()
	}, true)

	go func() {
		if md, err := os.Create("../fixtures/read.md"); err == nil {
			md.Close()
		}
		<-time.After(3 * time.Second)
		os.Remove("../fixtures/read.md")
		ws.Done()
	}()

	ws.Wait()
	watcher.Close()
}

func TestWatchSet(t *testing.T) {
	ws := new(sync.WaitGroup)
	ws.Add(2)

	watcher := WatchSet(WatchSetConfig{
		Path: []string{"../fixtures"},
	})

	watcher.React(func(r flux.Reactor, err error, ev interface{}) {
		ws.Done()
	}, true)

	go func() {
		if md, err := os.Create("../fixtures/dust.md"); err == nil {
			md.Close()
		}
		<-time.After(2 * time.Second)
		os.Remove("../fixtures/dust.md")
		ws.Done()
	}()

	ws.Wait()
	watcher.Close()
}
