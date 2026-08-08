// Package cbz writes a set of already-downloaded page images out as a
// CBZ (Comic Book ZIP) archive: a plain zip whose entries are the page
// images in reading order, named so that lexicographic order (which is
// what every comic reader sorts by) matches page order.
package cbz

import (
	"archive/zip"
	"bytes"
	"fmt"
	"os"
	"strconv"
)

// extensionFor sniffs an image's format from its magic bytes and returns
// the matching file extension (including the leading dot). This is a
// hand-rolled sniffer rather than net/http.DetectContentType because that
// function doesn't recognize AVIF or HEIC — both of which real sources
// (e.g. Hitomi) serve pages as — and would silently mislabel them as
// ".jpg", which some CBZ readers refuse to open. Falls back to ".jpg" for
// anything unrecognized, since an unlabeled extension is worse than a
// wrong-but-usually-right one.
func extensionFor(data []byte) string {
	switch {
	case len(data) >= 3 && data[0] == 0xFF && data[1] == 0xD8 && data[2] == 0xFF:
		return ".jpg"
	case len(data) >= 8 && bytes.Equal(data[:8], []byte{0x89, 'P', 'N', 'G', '\r', '\n', 0x1a, '\n'}):
		return ".png"
	case len(data) >= 6 && (bytes.Equal(data[:6], []byte("GIF87a")) || bytes.Equal(data[:6], []byte("GIF89a"))):
		return ".gif"
	case len(data) >= 12 && bytes.Equal(data[:4], []byte("RIFF")) && bytes.Equal(data[8:12], []byte("WEBP")):
		return ".webp"
	case len(data) >= 2 && data[0] == 'B' && data[1] == 'M':
		return ".bmp"
	case len(data) >= 12 && bytes.Equal(data[4:8], []byte("ftyp")):
		switch string(data[8:12]) {
		case "avif", "avis":
			return ".avif"
		case "heic", "heix", "hevc", "hevx", "mif1", "msf1":
			return ".heic"
		}
	}
	return ".jpg"
}

// Write creates a CBZ archive at path from pages, in order. Each entry is
// stored (not deflated) since page images are already compressed formats;
// deflating them again would only cost CPU for no size benefit.
func Write(path string, pages [][]byte) (err error) {
	f, err := os.Create(path)
	if err != nil {
		return fmt.Errorf("cbz: creating %s: %w", path, err)
	}
	defer func() {
		if cerr := f.Close(); err == nil {
			err = cerr
		}
	}()

	zw := zip.NewWriter(f)
	defer func() {
		if cerr := zw.Close(); err == nil {
			err = cerr
		}
	}()

	width := len(strconv.Itoa(len(pages)))
	if width < 3 {
		width = 3
	}
	for i, data := range pages {
		name := fmt.Sprintf("%0*d%s", width, i+1, extensionFor(data))
		w, err := zw.CreateHeader(&zip.FileHeader{Name: name, Method: zip.Store})
		if err != nil {
			return fmt.Errorf("cbz: adding %s: %w", name, err)
		}
		if _, err := w.Write(data); err != nil {
			return fmt.Errorf("cbz: writing %s: %w", name, err)
		}
	}
	return nil
}
