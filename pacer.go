package pacer

import (
	"sync"
	"time"
)

// DynamicSpreader ensures requests are evenly distributed over the remaining
// time in a fixed window, adjusting automatically after idle periods.
type DynamicSpreader struct {
	mu          sync.Mutex
	windowSize  time.Duration
	maxRequests int

	windowStart   time.Time
	requestsDone  int
	lastRequestAt time.Time
}

// NewDynamicSpreader creates a new rate limiter.
func NewDynamicSpreader(maxRequests int, windowSize time.Duration) *DynamicSpreader {
	return &DynamicSpreader{
		windowSize:  windowSize,
		maxRequests: maxRequests,
		windowStart: time.Now(),
	}
}

// Take blocks the current goroutine until the dynamically calculated interval has passed.
func (s *DynamicSpreader) Take() time.Time {
	// 1. Calculate the delay instantly inside the mutex
	delay := s.reserve()

	// 2. Execute the wait OUTSIDE the mutex to prevent locking other goroutines
	if delay > 0 {
		time.Sleep(delay)
	}

	return time.Now()
}

// reserve calculates the required wait time in nanoseconds without blocking.
// This is the "Critical Section" and executes extremely fast.
func (s *DynamicSpreader) reserve() time.Duration {
	s.mu.Lock()
	defer s.mu.Unlock()

	now := time.Now()
	windowEnd := s.windowStart.Add(s.windowSize)

	// 1. Shift window if current time has passed the active window boundary.
	if now.After(windowEnd) || now.Equal(windowEnd) {
		intervalsPassed := now.Sub(s.windowStart) / s.windowSize
		s.windowStart = s.windowStart.Add(intervalsPassed * s.windowSize)
		windowEnd = s.windowStart.Add(s.windowSize)
		s.requestsDone = 0
		s.lastRequestAt = time.Time{}
	}

	requestsLeft := s.maxRequests - s.requestsDone

	// 2. Window exhausted: push this request to the start of the next window.
	if requestsLeft <= 0 {
		waitTime := windowEnd.Sub(now)

		// Pre-book the first slot of the upcoming window
		s.windowStart = windowEnd
		s.requestsDone = 1
		s.lastRequestAt = windowEnd

		return waitTime
	}

	// 3. Dynamic spacing: calculate the ideal interval for the remaining time.
	timeLeft := windowEnd.Sub(now)
	interval := timeLeft / time.Duration(requestsLeft)

	var delay time.Duration
	targetTime := now

	// 4. Determine target execution time based on the previous request.
	if !s.lastRequestAt.IsZero() {
		targetTime = s.lastRequestAt.Add(interval)

		// If target time is in the past (e.g., after an idle period),
		// we catch up by allowing it to fire immediately (delay = 0).
		if targetTime.Before(now) {
			targetTime = now
		} else {
			delay = targetTime.Sub(now)
		}
	}

	// 5. Update state for the next caller
	s.requestsDone++
	s.lastRequestAt = targetTime

	return delay
}
