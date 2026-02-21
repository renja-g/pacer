package pacer

import (
	"sync"
	"testing"
	"time"
)

// --- 1. The "Before" Implementation (time.Now inside the lock) ---
type PacerInside struct {
	mu            sync.Mutex
	windowSize    time.Duration
	maxRequests   int
	windowStart   time.Time
	requestsDone  int
	lastRequestAt time.Time
}

func (s *PacerInside) reserve() time.Duration {
	s.mu.Lock()
	defer s.mu.Unlock()

	// THE BOTTLENECK: System call inside the critical section
	now := time.Now()
	windowEnd := s.windowStart.Add(s.windowSize)

	if now.After(windowEnd) || now.Equal(windowEnd) {
		s.windowStart = now
		windowEnd = s.windowStart.Add(s.windowSize)
		s.requestsDone = 0
		s.lastRequestAt = time.Time{}
	}

	requestsLeft := s.maxRequests - s.requestsDone
	if requestsLeft <= 0 {
		return windowEnd.Sub(now)
	}

	interval := windowEnd.Sub(now) / time.Duration(requestsLeft)
	var delay time.Duration
	targetTime := now

	if !s.lastRequestAt.IsZero() {
		targetTime = s.lastRequestAt.Add(interval)
		if targetTime.After(now) {
			delay = targetTime.Sub(now)
		} else {
			targetTime = now
		}
	}

	s.requestsDone++
	s.lastRequestAt = targetTime
	return delay
}

// --- 2. The "After" Implementation (time.Now outside the lock) ---
type PacerOutside struct {
	mu            sync.Mutex
	windowSize    time.Duration
	maxRequests   int
	windowStart   time.Time
	requestsDone  int
	lastRequestAt time.Time
}

// reserve accepts 'now' as an argument, making the lock pure math
func (s *PacerOutside) reserve(now time.Time) time.Duration {
	s.mu.Lock()
	defer s.mu.Unlock()

	windowEnd := s.windowStart.Add(s.windowSize)

	if now.After(windowEnd) || now.Equal(windowEnd) {
		s.windowStart = now
		windowEnd = s.windowStart.Add(s.windowSize)
		s.requestsDone = 0
		s.lastRequestAt = time.Time{}
	}

	requestsLeft := s.maxRequests - s.requestsDone
	if requestsLeft <= 0 {
		return windowEnd.Sub(now)
	}

	interval := windowEnd.Sub(now) / time.Duration(requestsLeft)
	var delay time.Duration
	targetTime := now

	if !s.lastRequestAt.IsZero() {
		targetTime = s.lastRequestAt.Add(interval)
		if targetTime.After(now) {
			delay = targetTime.Sub(now)
		} else {
			targetTime = now
		}
	}

	s.requestsDone++
	s.lastRequestAt = targetTime
	return delay
}

// --- 3. The Benchmarks ---

// We use 1 billion requests over a 1-hour window so the window never
// actually resets during the benchmark, keeping us purely on the "hot path".
const benchmarkRequests = 1_000_000_000
const benchmarkWindow = 1 * time.Hour

func BenchmarkTimeNowInsideLock(b *testing.B) {
	rl := &PacerInside{
		windowSize:  benchmarkWindow,
		maxRequests: benchmarkRequests,
		windowStart: time.Now(),
	}

	b.ResetTimer()
	b.RunParallel(func(pb *testing.PB) {
		for pb.Next() {
			// Simulating Take() without the sleep
			_ = rl.reserve()
		}
	})
}

func BenchmarkTimeNowOutsideLock(b *testing.B) {
	rl := &PacerOutside{
		windowSize:  benchmarkWindow,
		maxRequests: benchmarkRequests,
		windowStart: time.Now(),
	}

	b.ResetTimer()
	b.RunParallel(func(pb *testing.PB) {
		for pb.Next() {
			// Simulating Take() without the sleep
			now := time.Now()
			_ = rl.reserve(now)
		}
	})
}
