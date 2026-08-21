package cbz

import (
	"archive/zip"
	"os"
	"path/filepath"
	"testing"
)

var (
	jpegMagic = []byte{0xFF, 0xD8, 0xFF, 0x00}
	pngMagic  = []byte{0x89, 'P', 'N', 'G', '\r', '\n', 0x1a, '\n', 0x00}
)

func TestWriteCreatesReadableArchive(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "chapter.cbz")
	pages := [][]byte{jpegMagic, pngMagic}

	if err := Write(path, pages); err != nil {
		t.Fatalf("Write: %v", err)
	}

	r, err := zip.OpenReader(path)
	if err != nil {
		t.Fatalf("opening written archive: %v", err)
	}
	defer r.Close()

	if len(r.File) != len(pages) {
		t.Fatalf("archive has %d entries, want %d", len(r.File), len(pages))
	}
	wantNames := []string{"001.jpg", "002.png"}
	for i, f := range r.File {
		if f.Name != wantNames[i] {
			t.Errorf("entry %d name = %q, want %q", i, f.Name, wantNames[i])
		}
		if f.Method != zip.Store {
			t.Errorf("entry %d method = %d, want zip.Store (%d)", i, f.Method, zip.Store)
		}
	}
}

// TestWriteLeavesNoTempFileOnSuccess guards the atomic-write behavior added
// so a download index never sees a partial file under its final name: once
// Write returns successfully, the only .cbz-*.tmp artifact it created
// should be gone (renamed away), not left behind alongside the real file.
func TestWriteLeavesNoTempFileOnSuccess(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "chapter.cbz")
	if err := Write(path, [][]byte{jpegMagic}); err != nil {
		t.Fatalf("Write: %v", err)
	}

	entries, err := os.ReadDir(dir)
	if err != nil {
		t.Fatalf("reading dir: %v", err)
	}
	if len(entries) != 1 || entries[0].Name() != "chapter.cbz" {
		var names []string
		for _, e := range entries {
			names = append(names, e.Name())
		}
		t.Errorf("directory contains %v, want only chapter.cbz", names)
	}
}

// TestWriteFailureLeavesNoFileAtFinalPath checks the other half of the
// atomic-write guarantee: if writing fails partway through (here, an
// unwritable destination directory), the final path must not end up with a
// partial/corrupt file -- a caller that later checks "does path exist" to
// mean "is this chapter downloaded" must never be lied to.
func TestWriteFailureLeavesNoFileAtFinalPath(t *testing.T) {
	path := filepath.Join(t.TempDir(), "does-not-exist", "chapter.cbz")
	if err := Write(path, [][]byte{jpegMagic}); err == nil {
		t.Fatal("Write into a nonexistent directory succeeded, want an error")
	}
	if _, err := os.Stat(path); !os.IsNotExist(err) {
		t.Errorf("Stat(path) after failed Write: err=%v, want IsNotExist", err)
	}
}

func TestExtensionFor(t *testing.T) {
	tests := []struct {
		name string
		data []byte
		want string
	}{
		{"jpeg", jpegMagic, ".jpg"},
		{"png", pngMagic, ".png"},
		{"gif87", []byte("GIF87a\x00\x00"), ".gif"},
		{"gif89", []byte("GIF89a\x00\x00"), ".gif"},
		{"webp", append([]byte("RIFF\x00\x00\x00\x00WEBP"), 0), ".webp"},
		{"bmp", []byte("BM\x00\x00"), ".bmp"},
		{"avif", append([]byte{0, 0, 0, 0}, []byte("ftypavif")...), ".avif"},
		{"heic", append([]byte{0, 0, 0, 0}, []byte("ftypheic")...), ".heic"},
		{"unrecognized", []byte("not an image"), ".jpg"},
		{"empty", []byte{}, ".jpg"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := extensionFor(tt.data); got != tt.want {
				t.Errorf("extensionFor(%q) = %q, want %q", tt.name, got, tt.want)
			}
		})
	}
}
