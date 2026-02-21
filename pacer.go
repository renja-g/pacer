// Package pacer provides dynamic request pacing within fixed windows.
package pacer

import (
	"sync"
	"time"
)

// DynamicPacer ensures requests are evenly distributed over the remaining
// time in a fixed window, adjusting automatically after idle periods.
type DynamicPacer struct {
	mu          sync.Mutex
	windowSize  time.Duration
	maxRequests int

	windowStart   time.Time
	requestsDone  int
	lastRequestAt time.Time
}

// NewDynamicPacer creates a new rate limiter.
// It panics if maxRequests <= 0 or windowSize <= 0.
func NewDynamicPacer(maxRequests int, windowSize time.Duration) *DynamicPacer {
	if maxRequests <= 0 {
		panic("pacer: maxRequests must be positive")
	}
	if windowSize <= 0 {
		panic("pacer: windowSize must be positive")
	}
	return &DynamicPacer{
		windowSize:  windowSize,
		maxRequests: maxRequests,
		windowStart: time.Now(),
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

func (s *DynamicPacer) take(spread bool) time.Time {
	// 1. Get the current time outside the lock.
	now := time.Now()

	// 2. Pass now into the math function.
	delay := s.reserve(now, spread)

	// 3. Sleep outside the lock.
	if delay > 0 {
		time.Sleep(delay)
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

	s.requestsDone++
	s.lastRequestAt = targetTime

	return waitBeforeWindow + delay
}
