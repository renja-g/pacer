// Package pacer provides dynamic request pacing within fixed windows.
package pacer

import (
	"sync"
	"time"
)

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
	mu              sync.Mutex
	windowSize      time.Duration
	windowSizeNanos int64
	maxRequests     int

	windowStartNanos int64
	windowEndNanos   int64
	requestsDone     int
	lastRequestNanos int64
	hasLastRequest   bool
	slack            int
	slackRemaining   int
	clock            Clock
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

	now := cfg.clock.Now()
	windowSizeNanos := cfg.per.Nanoseconds()

	return &DynamicPacer{
		windowSize:       cfg.per,
		windowSizeNanos:  windowSizeNanos,
		maxRequests:      rate,
		windowStartNanos: now.UnixNano(),
		windowEndNanos:   now.UnixNano() + windowSizeNanos,
		slack:            cfg.slack,
		slackRemaining:   cfg.slack,
		clock:            cfg.clock,
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
	nowNanos := now.UnixNano()

	// 2. Pass now into the math function.
	delay := s.reserve(nowNanos, spread)

	// 3. Sleep outside the lock.
	if delay > 0 {
		clock.Sleep(delay)
		return now.Add(delay)
	}

	return now
}

// reserve calculates the required wait time without blocking.
// reserve takes now as an argument so the critical section is pure math.
func (s *DynamicPacer) reserve(nowNanos int64, spread bool) time.Duration {
	s.mu.Lock()
	defer s.mu.Unlock()

	// 1. Shift window if current time has passed the active window boundary.
	if nowNanos >= s.windowEndNanos {
		intervalsPassed := (nowNanos - s.windowStartNanos) / s.windowSizeNanos
		s.windowStartNanos += intervalsPassed * s.windowSizeNanos
		s.windowEndNanos = s.windowStartNanos + s.windowSizeNanos
		s.requestsDone = 0
		s.lastRequestNanos = 0
		s.hasLastRequest = false
		s.slackRemaining = s.slack
	}

	effectiveNowNanos := nowNanos
	var waitBeforeWindow int64
	if nowNanos < s.windowStartNanos {
		waitBeforeWindow = s.windowStartNanos - nowNanos
		effectiveNowNanos = s.windowStartNanos
	}

	requestsLeft := s.maxRequests - s.requestsDone

	// 2. Window exhausted
	if requestsLeft <= 0 {
		waitTime := s.windowEndNanos - effectiveNowNanos
		s.windowStartNanos = s.windowEndNanos
		s.windowEndNanos = s.windowStartNanos + s.windowSizeNanos
		s.requestsDone = 1
		s.lastRequestNanos = s.windowStartNanos
		s.hasLastRequest = true
		s.slackRemaining = s.slack
		return time.Duration(waitBeforeWindow + waitTime)
	}

	if !spread {
		s.requestsDone++
		s.lastRequestNanos = effectiveNowNanos
		s.hasLastRequest = true
		return time.Duration(waitBeforeWindow)
	}

	// 3. Dynamic spacing
	timeLeft := s.windowEndNanos - effectiveNowNanos
	interval := timeLeft / int64(requestsLeft)

	var delay int64
	targetTimeNanos := effectiveNowNanos

	// 4. Target calculation
	if s.hasLastRequest {
		targetTimeNanos = s.lastRequestNanos + interval
		if targetTimeNanos < effectiveNowNanos {
			targetTimeNanos = effectiveNowNanos
		} else {
			delay = targetTimeNanos - effectiveNowNanos
		}
	}

	if delay > 0 && s.slackRemaining > 0 {
		s.slackRemaining--
		delay = 0
		targetTimeNanos = effectiveNowNanos
	}

	s.requestsDone++
	s.lastRequestNanos = targetTimeNanos
	s.hasLastRequest = true

	return time.Duration(waitBeforeWindow + delay)
}
