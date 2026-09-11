package runtime

import (
	"context"
	"encoding/json"
	"net/http"
	"net/url"
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

// TestRestart doesn't have a fixture export that actually traps the guest
// (see loadFixture's doc comment on what main.wasm exports), so this only
// exercises the re-instantiation path itself: that Restart() tears down and
// recreates the module against the same wasm bytes, re-probes features, and
// leaves the interpreter able to serve calls again afterward — mirroring
// AidokuRunnerTests.swift's testSourcePanic minus the actual panic trigger.
func TestRestart(t *testing.T) {
	ctx := context.Background()
	wasmBytes, info := loadFixture(t)

	interp, err := New(ctx, info.Info.ID, wasmBytes, Config{})
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	defer interp.Close(ctx)

	if err := interp.Restart(ctx); err != nil {
		t.Fatalf("Restart: %v", err)
	}

	if interp.Features.ProvidesListings || interp.Features.ProvidesHome || interp.Features.DynamicFilters {
		t.Fatalf("unexpected features after restart: %+v", interp.Features)
	}

	query := "hello"
	result, err := interp.GetSearchMangaList(ctx, &query, 1, nil)
	if err != nil {
		t.Fatalf("GetSearchMangaList after restart: %v", err)
	}
	if result == nil {
		t.Fatalf("expected non-nil result after restart")
	}
}

// Cookie injection lets a host app paste browser cookies (e.g. a CF
// cf_clearance) into the jar that backs the source's net.* requests. Verifies
// the Interpreter.SetCookieHeader/SetCookie/Cookies wiring against the shared
// client's jar.
func TestFixtureCookieInjection(t *testing.T) {
	ctx := context.Background()
	wasmBytes, info := loadFixture(t)
	interp, err := New(ctx, info.Info.ID, wasmBytes, Config{})
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	defer interp.Close(ctx)

	// WebView/net use the shared client by default; confirm the jar is live.
	jar := interp.CookieJar()
	if jar == nil {
		t.Fatalf("expected a non-nil cookie jar on the shared client")
	}

	u, err := url.Parse("https://example.com/path")
	if err != nil {
		t.Fatal(err)
	}

	// Header-string injection (the user-facing path for pasting cookies).
	interp.SetCookieHeader(u, "cf_clearance=abc123; __cf_bm=xyz")
	got := interp.Cookies(u)
	if !hasCookie(got, "cf_clearance", "abc123") || !hasCookie(got, "__cf_bm", "xyz") {
		t.Fatalf("SetCookieHeader not reflected in Cookies: %v", got)
	}

	// Single-cookie injection.
	interp.SetCookie(u, "custom", "val")
	got = interp.Cookies(u)
	if !hasCookie(got, "custom", "val") {
		t.Fatalf("SetCookie not reflected in Cookies: %v", got)
	}

	// A cookie added for example.com must not leak to another host.
	other, _ := url.Parse("https://other.com/")
	if c := interp.Cookies(other); len(c) != 0 {
		t.Fatalf("cookie leaked to unrelated host: %v", c)
	}
}

func hasCookie(cs []*http.Cookie, name, value string) bool {
	for _, c := range cs {
		if c.Name == name && c.Value == value {
			return true
		}
	}
	return false
}
