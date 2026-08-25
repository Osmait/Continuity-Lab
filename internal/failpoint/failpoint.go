package failpoint

import (
	"errors"
	"sync"
)

type Mode string

const (
	Off    Mode = "off"
	Once   Mode = "once"
	Always Mode = "always"
)

type Registry struct {
	mu     sync.Mutex
	points map[string]Mode
}

func New() *Registry { return &Registry{points: make(map[string]Mode)} }

func (r *Registry) Set(name string, mode Mode) error {
	if mode != Off && mode != Once && mode != Always {
		return errors.New("invalid failpoint mode")
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	if mode == Off {
		delete(r.points, name)
	} else {
		r.points[name] = mode
	}
	return nil
}

func (r *Registry) Hit(name string) bool {
	r.mu.Lock()
	defer r.mu.Unlock()
	mode := r.points[name]
	if mode == Once {
		delete(r.points, name)
	}
	return mode == Once || mode == Always
}

func (r *Registry) Clear(name string) { r.mu.Lock(); delete(r.points, name); r.mu.Unlock() }
