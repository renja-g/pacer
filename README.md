# pacer

Dynamic request pacing with a fixed window that spreads requests evenly over the window and adapts after idle periods.

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
