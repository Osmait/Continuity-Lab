package locks

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"sync"
	"time"

	"golang.org/x/sys/unix"
)

type Mode int

const (
	Shared Mode = iota
	Exclusive
)

type keyed struct {
	mu   sync.RWMutex
	refs int
}

type Manager struct {
	dir   string
	mu    sync.Mutex
	locks map[string]*keyed
}

type Guard struct {
	manager *Manager
	key     string
	entry   *keyed
	mode    Mode
	file    *os.File
}

func New(dir string) *Manager { return &Manager{dir: dir, locks: make(map[string]*keyed)} }

func (m *Manager) Acquire(ctx context.Context, key string, mode Mode) (*Guard, error) {
	m.mu.Lock()
	entry := m.locks[key]
	if entry == nil {
		entry = &keyed{}
		m.locks[key] = entry
	}
	entry.refs++
	m.mu.Unlock()
	locked := false
	for !locked {
		if mode == Exclusive {
			locked = entry.mu.TryLock()
		} else {
			locked = entry.mu.TryRLock()
		}
		if locked {
			break
		}
		select {
		case <-ctx.Done():
			m.releaseRef(key, entry)
			return nil, ctx.Err()
		case <-time.After(5 * time.Millisecond):
		}
	}
	if err := os.MkdirAll(m.dir, 0o750); err != nil {
		unlock(entry, mode)
		m.releaseRef(key, entry)
		return nil, err
	}
	file, err := os.OpenFile(filepath.Join(m.dir, key+".lock"), os.O_CREATE|os.O_RDWR, 0o640)
	if err != nil {
		unlock(entry, mode)
		m.releaseRef(key, entry)
		return nil, err
	}
	op := unix.LOCK_SH
	if mode == Exclusive {
		op = unix.LOCK_EX
	}
	for {
		err = unix.Flock(int(file.Fd()), op|unix.LOCK_NB)
		if err == nil {
			break
		}
		if !errors.Is(err, unix.EWOULDBLOCK) && !errors.Is(err, unix.EAGAIN) {
			_ = file.Close()
			unlock(entry, mode)
			m.releaseRef(key, entry)
			return nil, err
		}
		select {
		case <-ctx.Done():
			_ = file.Close()
			unlock(entry, mode)
			m.releaseRef(key, entry)
			return nil, ctx.Err()
		case <-time.After(5 * time.Millisecond):
		}
	}
	return &Guard{manager: m, key: key, entry: entry, mode: mode, file: file}, nil
}

func (g *Guard) Close() error {
	if g.file == nil {
		return nil
	}
	err := unix.Flock(int(g.file.Fd()), unix.LOCK_UN)
	closeErr := g.file.Close()
	g.file = nil
	unlock(g.entry, g.mode)
	g.manager.releaseRef(g.key, g.entry)
	if err != nil {
		return err
	}
	return closeErr
}

func (m *Manager) releaseRef(key string, entry *keyed) {
	m.mu.Lock()
	defer m.mu.Unlock()
	entry.refs--
	if entry.refs == 0 {
		delete(m.locks, key)
	}
}
func unlock(entry *keyed, mode Mode) {
	if mode == Exclusive {
		entry.mu.Unlock()
	} else {
		entry.mu.RUnlock()
	}
}
