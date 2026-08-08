package runtime

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"testing"

	"github.com/ohaiibuzzle/aidokurunner-go/models"
)

// loadFixture mirrors AidokuRunnerTests.swift's approach: the bundled
// AidokuRunner test payload (copied into testdata/payload) is a minimal
// source that exports get_search_manga_list/get_manga_update/get_page_list,
// start, alloc, and free_result — enough to validate the load path, the
// alloc/free_result/result-buffer protocol, and feature detection, without
// needing the net/html/js/canvas namespaces yet.
func loadFixture(t *testing.T) ([]byte, models.SourceInfo) {
	t.Helper()
	wasmBytes, err := os.ReadFile(filepath.Join("testdata", "payload", "main.wasm"))
	if err != nil {
		t.Fatalf("reading main.wasm: %v", err)
	}
	jsonBytes, err := os.ReadFile(filepath.Join("testdata", "payload", "source.json"))
	if err != nil {
		t.Fatalf("reading source.json: %v", err)
	}
	var info models.SourceInfo
	if err := json.Unmarshal(jsonBytes, &info); err != nil {
		t.Fatalf("parsing source.json: %v", err)
	}
	return wasmBytes, info
}

func TestLoadFixture(t *testing.T) {
	ctx := context.Background()
	wasmBytes, info := loadFixture(t)

	if info.Info.ID != "test" || info.Info.Name != "Test" || info.Info.Version != 1 {
		t.Fatalf("unexpected source.json contents: %+v", info.Info)
	}

	interp, err := New(ctx, info.Info.ID, wasmBytes, Config{})
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	defer interp.Close(ctx)

	// This fixture exports none of the optional feature entry points.
	if interp.Features.ProvidesListings || interp.Features.ProvidesHome || interp.Features.DynamicFilters {
		t.Fatalf("unexpected features: %+v", interp.Features)
	}
}

func TestFixtureGetSearchMangaList(t *testing.T) {
	ctx := context.Background()
	wasmBytes, info := loadFixture(t)

	interp, err := New(ctx, info.Info.ID, wasmBytes, Config{})
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	defer interp.Close(ctx)

	query := "hello"
	result, err := interp.GetSearchMangaList(ctx, &query, 1, nil)
	// The fixture's get_search_manga_list is a minimal test stub; we only
	// assert that the full round trip (encode args -> store descriptors ->
	// guest call -> handleResult -> postcard decode) completes without a
	// protocol-level error. Whatever the fixture actually returns is
	// exercised as a decode smoke test.
	if err != nil {
		t.Fatalf("GetSearchMangaList: %v", err)
	}
	if result == nil {
		t.Fatalf("expected non-nil result")
	}
}
