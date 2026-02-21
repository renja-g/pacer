package pacer

import (
	"sync"
	"testing"
	"time"
)

func TestDynamicPacerReserve(t *testing.T) {
	windowStart := time.Unix(0, 0)

	type testCase struct {
		name             string
		spread           bool
		calls            []time.Duration
		wantDelays       []time.Duration
		wantRequestsDone int
		wantLastRequest  time.Time
		wantWindowStart  time.Time
	}

	cases := []testCase{
		{
			name:             "spread-true",
			spread:           true,
			calls:            []time.Duration{0, 1 * time.Second},
			wantDelays:       []time.Duration{0, 8 * time.Second},
			wantRequestsDone: 2,
			wantLastRequest:  windowStart.Add(9 * time.Second),
			wantWindowStart:  windowStart,
		},
		{
			name:             "spread-false",
			spread:           false,
			calls:            []time.Duration{0, 1 * time.Second, 2 * time.Second},
			wantDelays:       []time.Duration{0, 0, 8 * time.Second},
			wantRequestsDone: 1,
			wantLastRequest:  windowStart.Add(10 * time.Second),
			wantWindowStart:  windowStart.Add(10 * time.Second),
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			pacer := &DynamicPacer{
				windowSize:  10 * time.Second,
				maxRequests: 2,
				windowStart: windowStart,
			}

			if len(tc.calls) != len(tc.wantDelays) {
				t.Fatalf("test case %q has mismatched calls and delays", tc.name)
			}

			for i, offset := range tc.calls {
				now := windowStart.Add(offset)
				delay := pacer.reserve(now, tc.spread)
				if delay != tc.wantDelays[i] {
					t.Fatalf("call %d delay = %v, want %v", i, delay, tc.wantDelays[i])
				}
			}

			if pacer.requestsDone != tc.wantRequestsDone {
				t.Fatalf("requestsDone = %d, want %d", pacer.requestsDone, tc.wantRequestsDone)
			}
			if !pacer.lastRequestAt.Equal(tc.wantLastRequest) {
				t.Fatalf("lastRequestAt = %v, want %v", pacer.lastRequestAt, tc.wantLastRequest)
			}
			if !pacer.windowStart.Equal(tc.wantWindowStart) {
				t.Fatalf("windowStart = %v, want %v", pacer.windowStart, tc.wantWindowStart)
			}
		})
	}
}

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
