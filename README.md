# pacer

Dynamic request pacing with a fixed window that spreads requests evenly over the window and adapts after idle periods.

## How it works

This pacer uses a fixed window and dynamically recalculates spacing based on
how much time remains in the current window.

- The window length is `Per` (default 1s). Each window allows `rate` requests.
- When `Take()` is called, the pacer shifts the window forward if the current
  time is past the window end, resetting per-window counters.
- It computes `requestsLeft` and `timeLeft`, then uses
  `interval = timeLeft / requestsLeft` to keep the remaining requests evenly
  spaced over the remaining time.
- The next target time is `lastRequest + interval` (clamped to now); the caller
  sleeps until that target. This means spacing adapts if the caller was idle.
- `WithSlack(n)` allows up to `n` requests per window to skip the spacing delay
  (still respecting the max requests per window), which helps absorb small
  bursts without drifting the overall window limit.

`TakeBurst()` enforces the total per-window limit but does not spread requests;
use it when you only need a hard cap per window and don't care about even
spacing.

## Usage

```go
rl := pacer.New(100) // 100 per second by default
_ = rl.Take()

rl = pacer.New(20, pacer.Per(10*time.Second))
_ = rl.Take()

rl = pacer.New(200, pacer.Per(1*time.Minute), pacer.WithSlack(50))
_ = rl.Take()
```

Options:
- `Per(d time.Duration)` sets the window (default 1s)
- `WithClock(clock)` swaps the time source (useful for tests)
- `WithSlack(n)` allows up to `n` requests to skip spacing delays per window (default 0)

## Try it

Run the demo that mirrors the phased example:

```sh
cd /path/to/pacer
go run ./cmd/pacer-demo
```

## Inspiration

This project is inspired by the rate limiting libraries [go.uber.org/ratelimit](https://pkg.go.dev/go.uber.org/ratelimit) and [golang.org/x/time/rate](https://pkg.go.dev/golang.org/x/time/rate) but implements a different rate limiting algorithm.
