package host

import (
	"context"
	"strings"
	"testing"
	"time"
)

func TestRateLimitWaitHonorsContext(t *testing.T) {
	var r RateLimit
	r.Set(1, time.Hour)
	if err := r.Wait(context.Background()); err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Millisecond)
	defer cancel()
	if err := r.Wait(ctx); err == nil {
		t.Fatal("second Wait inside the period returned nil, want ctx error")
	}
}

func TestReadAllLimited(t *testing.T) {
	if b, err := ReadAllLimited(strings.NewReader("abcd"), 4); err != nil || string(b) != "abcd" {
		t.Fatalf("at limit: %q %v", b, err)
	}
	if _, err := ReadAllLimited(strings.NewReader("abcde"), 4); err == nil {
		t.Fatal("over limit: want error")
	}
}
