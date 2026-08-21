package source

import (
	"archive/zip"
	"os"
	"path/filepath"
	"testing"
)

func TestReadManifestFromDir(t *testing.T) {
	info, err := ReadManifest("../runtime/testdata/payload")
	if err != nil {
		t.Fatalf("ReadManifest: %v", err)
	}
	if info.Key != "test" || info.Name != "Test" || info.Version != 1 {
		t.Errorf("ReadManifest = %+v, want Key=test Name=Test Version=1", info)
	}
	if len(info.Languages) != 1 || info.Languages[0] != "en" {
		t.Errorf("Languages = %v, want [en]", info.Languages)
	}
}

// writeTestAix packages dir's contents into a zip at path, nested under a
// top-level "Payload/" directory -- the same layout LoadZip expects,
// mirroring what Aidoku's own build tooling produces.
func writeTestAix(t *testing.T, path, dir string) {
	t.Helper()
	f, err := os.Create(path)
	if err != nil {
		t.Fatalf("creating %s: %v", path, err)
	}
	defer f.Close()
	w := zip.NewWriter(f)
	entries, err := os.ReadDir(dir)
	if err != nil {
		t.Fatalf("reading %s: %v", dir, err)
	}
	for _, e := range entries {
		if e.IsDir() {
			continue
		}
		data, err := os.ReadFile(filepath.Join(dir, e.Name()))
		if err != nil {
			t.Fatalf("reading %s: %v", e.Name(), err)
		}
		zf, err := w.Create("Payload/" + e.Name())
		if err != nil {
			t.Fatalf("creating zip entry %s: %v", e.Name(), err)
		}
		if _, err := zf.Write(data); err != nil {
			t.Fatalf("writing zip entry %s: %v", e.Name(), err)
		}
	}
	if err := w.Close(); err != nil {
		t.Fatalf("closing zip: %v", err)
	}
}

func TestReadManifestFromZip(t *testing.T) {
	aixPath := filepath.Join(t.TempDir(), "test-v1.aix")
	writeTestAix(t, aixPath, "../runtime/testdata/payload")

	info, err := ReadManifest(aixPath)
	if err != nil {
		t.Fatalf("ReadManifest: %v", err)
	}
	if info.Key != "test" || info.Name != "Test" || info.Version != 1 {
		t.Errorf("ReadManifest = %+v, want Key=test Name=Test Version=1", info)
	}
}

func TestReadManifestMissing(t *testing.T) {
	if _, err := ReadManifest(filepath.Join(t.TempDir(), "nope.aix")); err == nil {
		t.Fatal("expected error for a nonexistent path, got nil")
	}
}
