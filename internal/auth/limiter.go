package auth

import (
	"strings"
	"sync"
	"time"
)

type LoginLimiter struct {
	mu        sync.Mutex
	attempts  map[string]loginAttempt
	baseDelay time.Duration
	maxDelay  time.Duration
	retention time.Duration
	clock     func() time.Time
	updates   uint64
}

type loginAttempt struct {
	failures   uint8
	next       time.Time
	lastUpdate time.Time
}

func NewLoginLimiter(clock func() time.Time) *LoginLimiter {
	if clock == nil {
		clock = time.Now
	}
	return &LoginLimiter{
		attempts:  make(map[string]loginAttempt),
		baseDelay: 250 * time.Millisecond,
		maxDelay:  30 * time.Second,
		retention: time.Hour,
		clock:     clock,
	}
}

func loginLimitKey(sourceIP, username string) string {
	return sourceIP + "\x00" + strings.ToLower(strings.TrimSpace(username))
}

func (l *LoginLimiter) RetryAfter(key string) time.Duration {
	l.mu.Lock()
	defer l.mu.Unlock()

	now := l.clock()
	attempt, exists := l.attempts[key]
	if !exists || !now.Before(attempt.next) {
		return 0
	}
	return attempt.next.Sub(now)
}

func (l *LoginLimiter) Failure(key string) time.Duration {
	l.mu.Lock()
	defer l.mu.Unlock()

	now := l.clock()
	attempt := l.attempts[key]
	if attempt.failures < 31 {
		attempt.failures++
	}
	delay := l.baseDelay
	for index := uint8(1); index < attempt.failures; index++ {
		if delay >= l.maxDelay/2 {
			delay = l.maxDelay
			break
		}
		delay *= 2
	}
	if delay > l.maxDelay {
		delay = l.maxDelay
	}
	attempt.next = now.Add(delay)
	attempt.lastUpdate = now
	l.attempts[key] = attempt
	l.updates++
	if l.updates%256 == 0 {
		l.cleanup(now)
	}
	return delay
}

func (l *LoginLimiter) Success(key string) {
	l.mu.Lock()
	defer l.mu.Unlock()
	delete(l.attempts, key)
}

func (l *LoginLimiter) cleanup(now time.Time) {
	for key, attempt := range l.attempts {
		if now.Sub(attempt.lastUpdate) > l.retention {
			delete(l.attempts, key)
		}
	}
}
