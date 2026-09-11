package repo

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"
	"time"
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

// TestInstallReplacesOlderVersion checks that installing an updated version
// of an already-installed source removes the old file rather than leaving
// both "<id>-v6.aix" and "<id>-v7.aix" installed side by side -- which would
// otherwise strand anything (library bookmarks, downloaded chapters) still
// keyed to the old file's path.
func TestInstallReplacesOlderVersion(t *testing.T) {
	mux := http.NewServeMux()
	mux.HandleFunc("/index.min.json", func(w http.ResponseWriter, r *http.Request) {
		json.NewEncoder(w).Encode(Index{
			Sources: []Source{
				{ID: "en.example", Version: 7, DownloadURL: "new.bin"},
			},
		})
	})
	mux.HandleFunc("/new.bin", func(w http.ResponseWriter, r *http.Request) {
		w.Write([]byte("v7 contents"))
	})
	srv := httptest.NewServer(mux)
	defer srv.Close()

	destDir := t.TempDir()
	oldPath := filepath.Join(destDir, "en.example-v6.aix")
	if err := os.WriteFile(oldPath, []byte("v6 contents"), 0o644); err != nil {
		t.Fatalf("seeding old version: %v", err)
	}
	// A different source's file, sharing the "en.example" prefix only
	// coincidentally, must survive untouched.
	unrelatedPath := filepath.Join(destDir, "en.example-extra-v1.aix")
	if err := os.WriteFile(unrelatedPath, []byte("unrelated"), 0o644); err != nil {
		t.Fatalf("seeding unrelated file: %v", err)
	}

	path, err := Install(context.Background(), srv.URL+"/index.min.json", "en.example", destDir)
	if err != nil {
		t.Fatalf("Install: %v", err)
	}
	if want := filepath.Join(destDir, "en.example-v7.aix"); path != want {
		t.Errorf("Install path = %q, want %q", path, want)
	}
	if _, err := os.Stat(oldPath); !os.IsNotExist(err) {
		t.Errorf("old version still present: err=%v", err)
	}
	if _, err := os.Stat(unrelatedPath); err != nil {
		t.Errorf("unrelated file was removed: %v", err)
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

// TestInstallRejectsMaliciousID guards against a compromised or malicious
// repository index (the id comes from the fetched index.min.json, not the
// caller) using a source id crafted to escape destDir via filepath.Join, or
// to inject glob metacharacters into removeOtherVersions' delete pattern.
func TestInstallRejectsMaliciousID(t *testing.T) {
	outsideDir := t.TempDir()
	destDir := filepath.Join(outsideDir, "installed")
	if err := os.MkdirAll(destDir, 0o755); err != nil {
		t.Fatalf("MkdirAll: %v", err)
	}
	canary := filepath.Join(outsideDir, "canary.txt")
	if err := os.WriteFile(canary, []byte("do not touch"), 0o644); err != nil {
		t.Fatalf("seeding canary: %v", err)
	}

	cases := []string{"../canary", "../../etc/passwd", "en.example/../../canary", "en.*", "en.example\x00"}
	for _, id := range cases {
		t.Run(id, func(t *testing.T) {
			mux := http.NewServeMux()
			mux.HandleFunc("/index.min.json", func(w http.ResponseWriter, r *http.Request) {
				json.NewEncoder(w).Encode(Index{
					Sources: []Source{{ID: id, Version: 1, DownloadURL: "payload.bin"}},
				})
			})
			mux.HandleFunc("/payload.bin", func(w http.ResponseWriter, r *http.Request) {
				w.Write([]byte("malicious payload"))
			})
			srv := httptest.NewServer(mux)
			defer srv.Close()

			if _, err := Install(context.Background(), srv.URL+"/index.min.json", id, destDir); err == nil {
				t.Fatalf("Install with id %q: expected error, got nil", id)
			}
		})
	}

	data, err := os.ReadFile(canary)
	if err != nil || string(data) != "do not touch" {
		t.Fatalf("canary file was modified: data=%q err=%v", data, err)
	}
}

// TestInstallLeavesNoTempFileOnSuccess guards the download()
// temp-file-then-rename fix: a leftover .aix-*.tmp next to the installed
// file would mean the write isn't actually atomic.
func TestInstallLeavesNoTempFileOnSuccess(t *testing.T) {
	mux := http.NewServeMux()
	mux.HandleFunc("/index.min.json", func(w http.ResponseWriter, r *http.Request) {
		json.NewEncoder(w).Encode(Index{
			Sources: []Source{{ID: "en.example", Version: 1, DownloadURL: "payload.bin"}},
		})
	})
	mux.HandleFunc("/payload.bin", func(w http.ResponseWriter, r *http.Request) {
		w.Write([]byte("payload"))
	})
	srv := httptest.NewServer(mux)
	defer srv.Close()

	destDir := t.TempDir()
	if _, err := Install(context.Background(), srv.URL+"/index.min.json", "en.example", destDir); err != nil {
		t.Fatalf("Install: %v", err)
	}
	entries, err := os.ReadDir(destDir)
	if err != nil {
		t.Fatalf("ReadDir: %v", err)
	}
	if len(entries) != 1 || entries[0].Name() != "en.example-v1.aix" {
		t.Fatalf("unexpected directory contents: %v", entries)
	}
}

// TestInstallDoesNotTouchFinalPathUntilComplete proves the actual
// crash-safety property temp-file-then-rename buys: the final "<id>-vN.aix"
// name must not exist at all while the download is still in flight. A
// process kill (or power loss) can't be caught by any Go error path -- no
// defer/os.Remove-on-error runs -- so the only way to guarantee a kill mid-
// download never leaves a truncated archive at the real name is for
// nothing to have ever written to that name until the final os.Rename.
//
// The handler sends headers (so the client's http.Client.Do returns and
// download() proceeds to create its destination file) but withholds every
// body byte until release, so by the time destDir has gained any file at
// all, the client is guaranteed to be blocked inside io.Copy -- past file
// creation, before any write -- for the whole window this test inspects
// destDir in. Polling for that first file (rather than a fixed sleep)
// keeps this deterministic regardless of scheduling.
func TestInstallDoesNotTouchFinalPathUntilComplete(t *testing.T) {
	release := make(chan struct{})

	mux := http.NewServeMux()
	mux.HandleFunc("/index.min.json", func(w http.ResponseWriter, r *http.Request) {
		json.NewEncoder(w).Encode(Index{
			Sources: []Source{{ID: "en.example", Version: 1, DownloadURL: "payload.bin"}},
		})
	})
	mux.HandleFunc("/payload.bin", func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
		w.(http.Flusher).Flush()
		<-release
		w.Write([]byte("payload"))
	})
	srv := httptest.NewServer(mux)
	defer srv.Close()

	destDir := t.TempDir()
	installErr := make(chan error, 1)
	go func() {
		_, err := Install(context.Background(), srv.URL+"/index.min.json", "en.example", destDir)
		installErr <- err
	}()

	deadline := time.Now().Add(2 * time.Second)
	sawAnyFile := false
	for time.Now().Before(deadline) {
		if entries, err := os.ReadDir(destDir); err == nil && len(entries) > 0 {
			sawAnyFile = true
			break
		}
		time.Sleep(5 * time.Millisecond)
	}
	if !sawAnyFile {
		close(release)
		t.Fatal("destDir never gained a file while the download was blocked -- test didn't reach the state it needs to inspect")
	}

	finalPath := filepath.Join(destDir, "en.example-v1.aix")
	if _, err := os.Stat(finalPath); !os.IsNotExist(err) {
		t.Errorf("final path exists while download still in flight (stat err=%v) -- a kill right now would leave a truncated archive at the real name", err)
	}
	close(release)

	if err := <-installErr; err != nil {
		t.Fatalf("Install: %v", err)
	}
	if _, err := os.Stat(finalPath); err != nil {
		t.Fatalf("final path missing after successful Install: %v", err)
	}
}
