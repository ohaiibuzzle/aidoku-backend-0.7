package source

import (
	"archive/zip"
	"bytes"
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	goruntime "runtime"

	"github.com/ohaiibuzzle/aidokurunner-go/runtime"
	"github.com/ohaiibuzzle/aidokurunner-go/settingsstore"
)

// writeBombAix writes an .aix whose "Payload/<name>" member inflates to
// exactly size bytes of a single repeated byte. deflate shrinks that to a
// few tens of KB on disk -- the shape of a zip bomb: a tiny archive whose
// member would otherwise be read straight into memory at full inflated
// size, before any validation runs.
func writeBombAix(t *testing.T, path, name string, size int) {
	t.Helper()
	f, err := os.Create(path)
	if err != nil {
		t.Fatalf("creating %s: %v", path, err)
	}
	defer f.Close()

	w := zip.NewWriter(f)
	// A valid source.json so the load gets past the manifest and actually
	// reaches the oversized member.
	sf, err := w.Create("Payload/source.json")
	if err != nil {
		t.Fatalf("creating zip entry source.json: %v", err)
	}
	if _, err := sf.Write([]byte(`{"info":{"id":"test","name":"Test","version":1}}`)); err != nil {
		t.Fatalf("writing zip entry source.json: %v", err)
	}
	zf, err := w.Create("Payload/" + name)
	if err != nil {
		t.Fatalf("creating zip entry %s: %v", name, err)
	}
	buf := bytes.Repeat([]byte{'A'}, 1<<20)
	for written := 0; written < size; {
		n := len(buf)
		if remaining := size - written; remaining < n {
			n = remaining
		}
		if _, err := zf.Write(buf[:n]); err != nil {
			t.Fatalf("writing zip entry %s: %v", name, err)
		}
		written += n
	}
	if err := w.Close(); err != nil {
		t.Fatalf("closing zip: %v", err)
	}
}

// TestLoadZipRejectsOversizedMember is the regression test for loading a
// source's main.wasm with no size cap: a ~65KB archive whose member inflates
// to far more than the cap must fail loudly instead of materialising the
// whole thing in memory (an OOM on the 256-512MB target devices).
func TestLoadZipRejectsOversizedMember(t *testing.T) {
	const inflated = 80 << 20 // > the 64MB main.wasm cap, but a few tens of KB on disk

	aixPath := filepath.Join(t.TempDir(), "bomb.aix")
	writeBombAix(t, aixPath, "main.wasm", inflated)

	ctx := context.Background()
	store, err := settingsstore.Open("")
	if err != nil {
		t.Fatalf("settingsstore.Open: %v", err)
	}

	var before, after goruntime.MemStats
	goruntime.ReadMemStats(&before)
	_, err = LoadZip(ctx, aixPath, runtime.Config{Settings: store}, store)
	goruntime.ReadMemStats(&after)
	grown := after.TotalAlloc - before.TotalAlloc
	t.Logf("TotalAlloc grew by %d bytes while loading an %d-byte inflated member", grown, inflated)

	if err == nil {
		t.Fatal("LoadZip accepted an oversized main.wasm, want a size-limit error")
	}
	// Assert the cap error specifically, so an unrelated failure (e.g. the
	// 80MB blob failing to parse as wasm) can't masquerade as a pass.
	if !strings.Contains(err.Error(), "main.wasm") || !strings.Contains(err.Error(), "larger than") {
		t.Errorf("LoadZip error = %v, want a size-limit error naming main.wasm", err)
	}
	// Pre-fix this grows by ~80MB, because the member is read into memory
	// before any size check runs.
	if grown > 32<<20 {
		t.Errorf("LoadZip allocated %d bytes reading an %d-byte member, want < %d (data was materialised before validation)", grown, inflated, 32<<20)
	}
}

// TestLoadZipRejectsOversizedOptionalMember covers the optional members:
// an oversized icon.png/filters.json/settings.json must fail loudly, not be
// swallowed by the "absent is fine" check the way a missing file is.
func TestLoadZipRejectsOversizedOptionalMember(t *testing.T) {
	aixPath := filepath.Join(t.TempDir(), "bomb.aix")
	writeBombAix(t, aixPath, "icon.png", 20<<20) // > the 16MB manifest cap

	ctx := context.Background()
	store, err := settingsstore.Open("")
	if err != nil {
		t.Fatalf("settingsstore.Open: %v", err)
	}

	if _, err := LoadZip(ctx, aixPath, runtime.Config{Settings: store}, store); err == nil {
		t.Fatal("LoadZip accepted an oversized icon.png, want a size-limit error")
	} else if !strings.Contains(err.Error(), "icon.png") || !strings.Contains(err.Error(), "larger than") {
		t.Errorf("LoadZip error = %v, want a size-limit error naming icon.png", err)
	}
}

// TestLoadZipFixture is the positive counterpart: a normal, small bundle
// must still load after the caps are in place.
func TestLoadZipFixture(t *testing.T) {
	aixPath := filepath.Join(t.TempDir(), "test-v1.aix")
	writeTestAix(t, aixPath, "../runtime/testdata/payload")

	ctx := context.Background()
	store, err := settingsstore.Open("")
	if err != nil {
		t.Fatalf("settingsstore.Open: %v", err)
	}

	s, err := LoadZip(ctx, aixPath, runtime.Config{Settings: store}, store)
	if err != nil {
		t.Fatalf("LoadZip: %v", err)
	}
	defer s.Runner.Close(ctx)

	if s.Key != "test" || s.Name != "Test" || s.Version != 1 {
		t.Fatalf("unexpected source: key=%q name=%q version=%d", s.Key, s.Name, s.Version)
	}
}
