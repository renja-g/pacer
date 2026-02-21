package main

import (
	"fmt"
	"time"

	"github.com/renja-g/pacer"
)

func main() {
	// Setup: 20 requests per 10-second window
	rl := pacer.NewDynamicSpreader(20, 10*time.Second)
	programStart := time.Now()
	var prevRequestTime time.Time

	fmt.Println("--- Phase 1: 10 requests (Calculated Spacing) ---")
	// Expected: first request is immediate, then spacing is recalculated as
	// remainingTime/remainingRequests. Early on this is ~10s/19 ~= 526ms.
	// So 10 requests typically take ~4.7s, not ~5s.
	for i := 1; i <= 10; i++ {
		now := rl.Take()
		delta := "N/A"
		if !prevRequestTime.IsZero() {
			delta = now.Sub(prevRequestTime).Round(time.Millisecond).String()
		}

		fmt.Printf("Req %2d | Elapsed: %v | +%s\n", i, now.Sub(programStart).Round(time.Millisecond), delta)
		prevRequestTime = now
	}

	fmt.Println("\n--- Phase 2: Idle for 2 seconds ---")
	time.Sleep(2 * time.Second)

	fmt.Println("\n--- Phase 3: Remaining 10 requests (Recalculated Spacing) ---")
	// Expected: after idling, req 11 can fire immediately (catch-up behavior).
	// Then the remaining requests are spread over the remaining window time
	// (roughly ~360ms between requests in this demo run).
	for i := 11; i <= 20; i++ {
		now := rl.Take()
		delta := "N/A"
		if !prevRequestTime.IsZero() {
			delta = now.Sub(prevRequestTime).Round(time.Millisecond).String()
		}

		fmt.Printf("Req %2d | Elapsed: %v | +%s\n", i, now.Sub(programStart).Round(time.Millisecond), delta)
		prevRequestTime = now
	}
}
