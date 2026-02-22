package pacer

import (
	"sync"
	"testing"
	"time"
)

type fakeClock struct {
	now   time.Time
	slept []time.Duration
}

func (f *fakeClock) Now() time.Time {
	return f.now
}

func (f *fakeClock) Sleep(d time.Duration) {
	f.slept = append(f.slept, d)
	f.now = f.now.Add(d)
}

func TestNewOptions(t *testing.T) {
	baseTime := time.Unix(0, 0)

	type testCase struct {
		name           string
		rate           int
		per            time.Duration
		slack          int
		useFakeClock   bool
		wantWindowSize time.Duration
		wantSlack      int
		wantClockType  string
	}

	cases := []testCase{
		{
			name:           "defaults",
			rate:           10,
			wantWindowSize: time.Second,
			wantSlack:      0,
			wantClockType:  "real",
		},
		{
			name:           "custom-per-slack-clock",
			rate:           5,
			per:            10 * time.Second,
			slack:          2,
			useFakeClock:   true,
			wantWindowSize: 10 * time.Second,
			wantSlack:      2,
			wantClockType:  "fake",
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			var opts []Option
			var fc *fakeClock

			if tc.per != 0 {
				opts = append(opts, Per(tc.per))
			}
			if tc.slack != 0 {
				opts = append(opts, WithSlack(tc.slack))
			}
			if tc.useFakeClock {
				fc = &fakeClock{now: baseTime}
				opts = append(opts, WithClock(fc))
			}

			pacer := New(tc.rate, opts...)

			if pacer.windowSize != tc.wantWindowSize {
				t.Fatalf("windowSize = %v, want %v", pacer.windowSize, tc.wantWindowSize)
			}
			if pacer.slack != tc.wantSlack {
				t.Fatalf("slack = %d, want %d", pacer.slack, tc.wantSlack)
			}
			if pacer.slackRemaining != tc.wantSlack {
				t.Fatalf("slackRemaining = %d, want %d", pacer.slackRemaining, tc.wantSlack)
			}

			if tc.wantClockType == "real" {
				if _, ok := pacer.clock.(realClock); !ok {
					t.Fatalf("clock = %T, want realClock", pacer.clock)
				}
				return
			}

			if pacer.clock != fc {
				t.Fatalf("clock = %v, want %v", pacer.clock, fc)
			}
			if !pacer.windowStart.Equal(baseTime) {
				t.Fatalf("windowStart = %v, want %v", pacer.windowStart, baseTime)
			}
		})
	}
}

func TestNewPanics(t *testing.T) {
	type testCase struct {
		name string
		rate int
		opts []Option
	}

	cases := []testCase{
		{
			name: "rate-zero",
			rate: 0,
		},
		{
			name: "per-zero",
			rate: 1,
			opts: []Option{Per(0)},
		},
		{
			name: "slack-negative",
			rate: 1,
			opts: []Option{WithSlack(-1)},
		},
		{
			name: "clock-nil",
			rate: 1,
			opts: []Option{WithClock(nil)},
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			defer func() {
				if r := recover(); r == nil {
					t.Fatalf("expected panic")
				}
			}()

			_ = New(tc.rate, tc.opts...)
		})
	}
}

func TestDynamicPacerReserveSlack(t *testing.T) {
	windowStart := time.Unix(0, 0)

	type testCase struct {
		name               string
		slack              int
		calls              []time.Duration
		wantDelays         []time.Duration
		wantSlackRemaining int
		wantLastRequest    time.Time
	}

	cases := []testCase{
		{
			name:               "no-slack",
			slack:              0,
			calls:              []time.Duration{0, 1 * time.Second},
			wantDelays:         []time.Duration{0, 8 * time.Second},
			wantSlackRemaining: 0,
			wantLastRequest:    windowStart.Add(9 * time.Second),
		},
		{
			name:               "slack-skips-delay",
			slack:              1,
			calls:              []time.Duration{0, 1 * time.Second},
			wantDelays:         []time.Duration{0, 0},
			wantSlackRemaining: 0,
			wantLastRequest:    windowStart.Add(1 * time.Second),
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			pacer := &DynamicPacer{
				windowSize:     10 * time.Second,
				maxRequests:    2,
				windowStart:    windowStart,
				slack:          tc.slack,
				slackRemaining: tc.slack,
			}

			if len(tc.calls) != len(tc.wantDelays) {
				t.Fatalf("test case %q has mismatched calls and delays", tc.name)
			}

			for i, offset := range tc.calls {
				now := windowStart.Add(offset)
				delay := pacer.reserve(now, true)
				if delay != tc.wantDelays[i] {
					t.Fatalf("call %d delay = %v, want %v", i, delay, tc.wantDelays[i])
				}
			}

			if pacer.slackRemaining != tc.wantSlackRemaining {
				t.Fatalf("slackRemaining = %d, want %d", pacer.slackRemaining, tc.wantSlackRemaining)
			}
			if !pacer.lastRequestAt.Equal(tc.wantLastRequest) {
				t.Fatalf("lastRequestAt = %v, want %v", pacer.lastRequestAt, tc.wantLastRequest)
			}
		})
	}
}

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

func TestDynamicPacerReserveFutureWindowStart(t *testing.T) {
	windowStart := time.Unix(0, 0).Add(10 * time.Second)
	now := windowStart.Add(-5 * time.Second)

	type testCase struct {
		name             string
		spread           bool
		wantDelay        time.Duration
		wantRequestsDone int
		wantLastRequest  time.Time
		wantWindowStart  time.Time
	}

	cases := []testCase{
		{
			name:             "burst-waits-until-window-start",
			spread:           false,
			wantDelay:        5 * time.Second,
			wantRequestsDone: 2,
			wantLastRequest:  windowStart,
			wantWindowStart:  windowStart,
		},
		{
			name:             "spread-uses-window-start",
			spread:           true,
			wantDelay:        15 * time.Second,
			wantRequestsDone: 2,
			wantLastRequest:  windowStart.Add(10 * time.Second),
			wantWindowStart:  windowStart,
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			pacer := &DynamicPacer{
				windowSize:    10 * time.Second,
				maxRequests:   2,
				windowStart:   windowStart,
				requestsDone:  1,
				lastRequestAt: windowStart,
			}

			delay := pacer.reserve(now, tc.spread)
			if delay != tc.wantDelay {
				t.Fatalf("delay = %v, want %v", delay, tc.wantDelay)
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

// --- 1. The "before" implementation (time.Now inside the lock). ---
type pacerInside struct {
	mu            sync.Mutex
	windowSize    time.Duration
	maxRequests   int
	windowStart   time.Time
	requestsDone  int
	lastRequestAt time.Time
}

func (s *pacerInside) reserve() time.Duration {
	s.mu.Lock()
	defer s.mu.Unlock()

	// The bottleneck is a system call inside the critical section.
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

// --- 2. The "after" implementation (time.Now outside the lock). ---
type pacerOutside struct {
	mu            sync.Mutex
	windowSize    time.Duration
	maxRequests   int
	windowStart   time.Time
	requestsDone  int
	lastRequestAt time.Time
}

// reserve accepts now as an argument, making the lock pure math.
func (s *pacerOutside) reserve(now time.Time) time.Duration {
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

// --- 3. The benchmarks. ---

// We use 1 billion requests over a 1-hour window so the window never
// actually resets during the benchmark, keeping us purely on the "hot path".
const benchmarkRequests = 1_000_000_000
const benchmarkWindow = 1 * time.Hour

func BenchmarkTimeNowInsideLock(b *testing.B) {
	rl := &pacerInside{
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
	rl := &pacerOutside{
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
