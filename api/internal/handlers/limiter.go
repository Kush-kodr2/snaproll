package handlers

import (
	"sync"
	"time"
)

const (
	maxFailures = 5
	lockoutTime = 15 * time.Minute
	maxTracked  = 10000
)

type attemptState struct {
	fails       int
	lockedUntil time.Time
}

type attemptLimiter struct {
	mu sync.Mutex
	m  map[string]*attemptState
}

// In memory only, so it resets when the server restarts. Fine for a project on one
// machine; a real deployment would keep these counters in the database or Redis.
var enrollLimiter = &attemptLimiter{m: map[string]*attemptState{}}

func (l *attemptLimiter) locked(key string) bool {
	l.mu.Lock()
	defer l.mu.Unlock()
	s := l.m[key]
	return s != nil && time.Now().Before(s.lockedUntil)
}

func (l *attemptLimiter) fail(key string) {
	l.mu.Lock()
	defer l.mu.Unlock()
	if len(l.m) >= maxTracked {
		l.m = map[string]*attemptState{} // crude cap so random keys can't grow memory forever
	}
	s := l.m[key]
	if s == nil {
		s = &attemptState{}
		l.m[key] = s
	}
	s.fails++
	if s.fails >= maxFailures {
		s.lockedUntil = time.Now().Add(lockoutTime)
		s.fails = 0
	}
}

func (l *attemptLimiter) reset(key string) {
	l.mu.Lock()
	defer l.mu.Unlock()
	delete(l.m, key)
}