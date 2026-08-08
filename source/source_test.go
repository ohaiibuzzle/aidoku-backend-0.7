package source

import (
	"context"
	"testing"

	"github.com/ohaiibuzzle/aidokurunner-go/runtime"
	"github.com/ohaiibuzzle/aidokurunner-go/settingsstore"
)

func TestLoadFixture(t *testing.T) {
	ctx := context.Background()
	store, err := settingsstore.Open("")
	if err != nil {
		t.Fatalf("settingsstore.Open: %v", err)
	}

	s, err := LoadDir(ctx, "../runtime/testdata/payload", runtime.Config{Settings: store}, store)
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	defer s.Runner.Close(ctx)

	if s.Key != "test" || s.Name != "Test" || s.Version != 1 {
		t.Fatalf("unexpected source: key=%q name=%q version=%d", s.Key, s.Name, s.Version)
	}
	if s.OnlySearch() != true {
		t.Fatalf("expected OnlySearch() true for a fixture with no home/listings")
	}
}
