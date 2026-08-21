package repo

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"
)

// TestFetchIndexResolvesRelativeURLs exercises the common real-world shape:
// an index.min.json whose iconURL/downloadURL are relative to the index
// itself (as aidoku-community.github.io/sources/index.min.json does).
func TestFetchIndexResolvesRelativeURLs(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/sources/index.min.json" {
			t.Fatalf("unexpected request path %q", r.URL.Path)
		}
		json.NewEncoder(w).Encode(Index{
			Name: "Test Repo",
			Sources: []Source{
				{
					ID:          "en.example",
					Name:        "Example",
					Version:     3,
					IconURL:     "icons/en.example-v3.png",
					DownloadURL: "sources/en.example-v3.aix",
					Languages:   []string{"en"},
					BaseURL:     "https://example.com",
				},
			},
		})
	}))
	defer srv.Close()

	idx, err := FetchIndex(context.Background(), srv.URL+"/sources/index.min.json")
	if err != nil {
		t.Fatalf("FetchIndex: %v", err)
	}
	if len(idx.Sources) != 1 {
		t.Fatalf("expected 1 source, got %d", len(idx.Sources))
	}
	src := idx.Sources[0]
	if want := srv.URL + "/sources/icons/en.example-v3.png"; src.IconURL != want {
		t.Errorf("IconURL = %q, want %q", src.IconURL, want)
	}
	if want := srv.URL + "/sources/sources/en.example-v3.aix"; src.DownloadURL != want {
		t.Errorf("DownloadURL = %q, want %q", src.DownloadURL, want)
	}

	if got := idx.Find("en.example"); got == nil || got.Name != "Example" {
		t.Errorf("Find(en.example) = %+v", got)
	}
	if got := idx.Find("missing"); got != nil {
		t.Errorf("Find(missing) = %+v, want nil", got)
	}
}

// TestInstallDownloadsAndNamesByIDAndVersion checks that Install saves the
// downloaded .aix using the "<id>-v<version>.aix" convention regardless of
// what the downloadURL itself is named.
func TestInstallDownloadsAndNamesByIDAndVersion(t *testing.T) {
	const payload = "fake aix contents"
	mux := http.NewServeMux()
	mux.HandleFunc("/index.min.json", func(w http.ResponseWriter, r *http.Request) {
		json.NewEncoder(w).Encode(Index{
			Sources: []Source{
				{ID: "en.example", Version: 5, DownloadURL: "weirdname.bin"},
			},
		})
	})
	mux.HandleFunc("/weirdname.bin", func(w http.ResponseWriter, r *http.Request) {
		w.Write([]byte(payload))
	})
	srv := httptest.NewServer(mux)
	defer srv.Close()

	destDir := t.TempDir()
	path, err := Install(context.Background(), srv.URL+"/index.min.json", "en.example", destDir)
	if err != nil {
		t.Fatalf("Install: %v", err)
	}
	wantPath := filepath.Join(destDir, "en.example-v5.aix")
	if path != wantPath {
		t.Errorf("Install path = %q, want %q", path, wantPath)
	}
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("reading installed file: %v", err)
	}
	if string(data) != payload {
		t.Errorf("installed content = %q, want %q", data, payload)
	}
}

// TestInstallUnknownSource ensures a source id absent from the index
// produces a clear error rather than a nil-pointer panic.
func TestInstallUnknownSource(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		json.NewEncoder(w).Encode(Index{})
	}))
	defer srv.Close()

	if _, err := Install(context.Background(), srv.URL, "en.missing", t.TempDir()); err == nil {
		t.Fatal("expected error for unknown source id, got nil")
	}
}
