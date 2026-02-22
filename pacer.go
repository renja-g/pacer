// Package pacer provides dynamic request pacing within fixed windows.
package pacer

import (
	"sync"
	"time"
)

type Limiter interface {
	Take() time.Time
}

// Clock allows swapping time sources in tests.
type Clock interface {
	Now() time.Time
	Sleep(time.Duration)
}

type realClock struct{}

func (realClock) Now() time.Time {
	return time.Now()
}

func (realClock) Sleep(d time.Duration) {
	time.Sleep(d)
}

// Option configures a DynamicPacer created by New.
type Option func(*config)

type config struct {
	per   time.Duration
	clock Clock
	slack int
}

func defaultConfig() config {
	return config{
		per:   time.Second,
		clock: realClock{},
		slack: 0,
	}
}

// Per sets the pacing window. The default is one second.
func Per(per time.Duration) Option {
	return func(cfg *config) {
		cfg.per = per
	}
}

// WithClock overrides the clock used by the pacer.
func WithClock(clock Clock) Option {
	return func(cfg *config) {
		cfg.clock = clock
	}
}

// WithSlack allows up to slack requests to skip spacing delays per window.
func WithSlack(slack int) Option {
	return func(cfg *config) {
		cfg.slack = slack
	}
}

// DynamicPacer ensures requests are evenly distributed over the remaining
// time in a fixed window, adjusting automatically after idle periods.
type DynamicPacer struct {
	mu          sync.Mutex
	windowSize  time.Duration
	maxRequests int

	windowStart    time.Time
	requestsDone   int
	lastRequestAt  time.Time
	slack          int
	slackRemaining int
	clock          Clock
}

// New creates a new pacer for the given rate.
// It panics if rate <= 0, the configured window <= 0, or slack < 0.
func New(rate int, opts ...Option) *DynamicPacer {
	if rate <= 0 {
		panic("pacer: rate must be positive")
	}

	cfg := defaultConfig()
	for _, opt := range opts {
		opt(&cfg)
	}

	if cfg.per <= 0 {
		panic("pacer: window must be positive")
	}
	if cfg.clock == nil {
		panic("pacer: clock must be set")
	}
	if cfg.slack < 0 {
		panic("pacer: slack must be non-negative")
	}

	return &DynamicPacer{
		windowSize:     cfg.per,
		maxRequests:    rate,
		windowStart:    cfg.clock.Now(),
		slack:          cfg.slack,
		slackRemaining: cfg.slack,
		clock:          cfg.clock,
	}
}

// Take blocks until the calculated interval passes and returns the execution time.
func (s *DynamicPacer) Take() time.Time {
	return s.take(true)
}

// TakeBurst enforces the total window limit but does not spread requests.
func (s *DynamicPacer) TakeBurst() time.Time {
	return s.take(false)
}

func (s *DynamicPacer) activeClock() Clock {
	if s.clock == nil {
		return realClock{}
	}
	return s.clock
}

func (s *DynamicPacer) take(spread bool) time.Time {
	// 1. Get the current time outside the lock.
	clock := s.activeClock()
	now := clock.Now()

	// 2. Pass now into the math function.
	delay := s.reserve(now, spread)

	// 3. Sleep outside the lock.
	if delay > 0 {
		clock.Sleep(delay)
		return now.Add(delay) // Return the exact simulated execution time.
	}

	return now
}

// reserve calculates the required wait time without blocking.
// reserve takes now as an argument so the critical section is pure math.
func (s *DynamicPacer) reserve(now time.Time, spread bool) time.Duration {
	s.mu.Lock()
	defer s.mu.Unlock()

	windowEnd := s.windowStart.Add(s.windowSize)

	// 1. Shift window if current time has passed the active window boundary.
	if now.After(windowEnd) || now.Equal(windowEnd) {
		intervalsPassed := now.Sub(s.windowStart) / s.windowSize
		s.windowStart = s.windowStart.Add(intervalsPassed * s.windowSize)
		windowEnd = s.windowStart.Add(s.windowSize)
		s.requestsDone = 0
		s.lastRequestAt = time.Time{}
		s.slackRemaining = s.slack
	}

	effectiveNow := now
	var waitBeforeWindow time.Duration
	if now.Before(s.windowStart) {
		waitBeforeWindow = s.windowStart.Sub(now)
		effectiveNow = s.windowStart
	}

	requestsLeft := s.maxRequests - s.requestsDone

	// 2. Window exhausted
	if requestsLeft <= 0 {
		waitTime := windowEnd.Sub(effectiveNow)
		s.windowStart = windowEnd
		s.requestsDone = 1
		s.lastRequestAt = windowEnd
		s.slackRemaining = s.slack
		return waitBeforeWindow + waitTime
	}

	if !spread {
		s.requestsDone++
		s.lastRequestAt = effectiveNow
		return waitBeforeWindow
	}

	// 3. Dynamic spacing
	timeLeft := windowEnd.Sub(effectiveNow)
	interval := timeLeft / time.Duration(requestsLeft)

	var delay time.Duration
	targetTime := effectiveNow

	// 4. Target calculation
	if !s.lastRequestAt.IsZero() {
		targetTime = s.lastRequestAt.Add(interval)
		if targetTime.Before(effectiveNow) {
			targetTime = effectiveNow
		} else {
			delay = targetTime.Sub(effectiveNow)
		}
	}

	if delay > 0 && s.slackRemaining > 0 {
		s.slackRemaining--
		delay = 0
		targetTime = effectiveNow
	}

	s.requestsDone++
	s.lastRequestAt = targetTime

	return waitBeforeWindow + delay
}
