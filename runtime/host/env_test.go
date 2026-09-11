package host

import (
	"context"
	"testing"
	"time"
)

func TestCapSleepDuration(t *testing.T) {
	cases := []struct {
		seconds int32
		want    time.Duration
	}{
		{1, 1 * time.Second},
		{5, 5 * time.Second},
		{30, 30 * time.Second},
		{31, maxEnvSleep},
		{1 << 30, maxEnvSleep}, // guest passing a huge value used to hang the interpreter
	}
	for _, c := range cases {
		if got := capSleepDuration(c.seconds); got != c.want {
			t.Errorf("capSleepDuration(%d) = %v, want %v", c.seconds, got, c.want)
		}
	}
}

// TestEnvSleepRespectsContextCancellation guards the ctx-blind time.Sleep
// this replaced: env.sleep must return promptly when ctx is cancelled,
// not block for the full (possibly capped-but-still-long) duration.
func TestEnvSleepRespectsContextCancellation(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	go func() {
		time.Sleep(20 * time.Millisecond)
		cancel()
	}()

	start := time.Now()
	envSleep(ctx, 30) // would otherwise block for the full 30s cap
	if elapsed := time.Since(start); elapsed > 2*time.Second {
		t.Fatalf("envSleep took %v, expected it to return shortly after ctx cancellation", elapsed)
	}
}

func TestEnvSleepIgnoresNonPositiveSeconds(t *testing.T) {
	start := time.Now()
	envSleep(context.Background(), 0)
	envSleep(context.Background(), -5)
	if elapsed := time.Since(start); elapsed > time.Second {
		t.Fatalf("envSleep(<=0) took %v, expected an immediate return", elapsed)
	}
}
