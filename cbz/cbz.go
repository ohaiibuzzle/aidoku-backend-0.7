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
	"path/filepath"
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
//
// Write requires every page's bytes up front, so it holds the whole
// chapter in memory at once. Callers assembling pages incrementally (e.g.
// downloading them one at a time) should use NewWriter instead, which
// writes each page to disk as soon as it arrives.
func Write(path string, pages [][]byte) error {
	w, err := NewWriter(path, len(pages))
	if err != nil {
		return err
	}
	for _, data := range pages {
		if err := w.WritePage(data); err != nil {
			w.Abort()
			return err
		}
	}
	return w.Close()
}

// Writer builds a CBZ archive one page at a time, so a caller streaming
// pages in (e.g. downloading them from a source) never needs to hold more
// than one page's bytes in memory -- unlike Write, which requires the
// whole chapter up front. This matters on memory-constrained devices
// (e.g. Kindle) where a long, high-resolution chapter buffered entirely in
// RAM before the first byte hits disk can be enough to get the process
// OOM-killed mid-download.
//
// The archive is built at a temporary path in the same directory as the
// final path and only renamed into place by Close, so a process death or
// write error partway through never leaves a corrupt file sitting at the
// final path under its final name -- callers (e.g. a download index) that
// treat that path's existence as "this chapter is downloaded" would
// otherwise be lied to. Same-directory keeps the rename same-filesystem,
// so it's a metadata-only operation rather than a second copy of the data.
//
// Every Writer must end with exactly one call to Close (success) or Abort
// (WritePage failed, or the download was cancelled) -- never both.
type Writer struct {
	path    string
	tmpPath string
	tmp     *os.File
	zw      *zip.Writer
	width   int
	n       int
}

// NewWriter creates a new Writer that will produce an archive at path once
// Close is called. totalPages is the chapter's known page count, used only
// to size the zero-padded entry names (e.g. 3 digits for "004.jpg") so
// they stay lexicographically sorted the way every CBZ reader expects;
// WritePage may be called more or fewer times than totalPages without
// affecting correctness, since the padding width isn't otherwise load-bearing.
func NewWriter(path string, totalPages int) (*Writer, error) {
	dir := filepath.Dir(path)
	tmp, err := os.CreateTemp(dir, ".cbz-*.tmp")
	if err != nil {
		return nil, fmt.Errorf("cbz: creating temp file in %s: %w", dir, err)
	}
	width := len(strconv.Itoa(totalPages))
	if width < 3 {
		width = 3
	}
	return &Writer{
		path:    path,
		tmpPath: tmp.Name(),
		tmp:     tmp,
		zw:      zip.NewWriter(tmp),
		width:   width,
	}, nil
}

// WritePage appends the next page, in reading order, to the archive.
// On error the Writer must not be reused -- the caller should call Abort.
func (w *Writer) WritePage(data []byte) error {
	w.n++
	name := fmt.Sprintf("%0*d%s", w.width, w.n, extensionFor(data))
	zf, err := w.zw.CreateHeader(&zip.FileHeader{Name: name, Method: zip.Store})
	if err != nil {
		return fmt.Errorf("cbz: adding %s: %w", name, err)
	}
	if _, err := zf.Write(data); err != nil {
		return fmt.Errorf("cbz: writing %s: %w", name, err)
	}
	return nil
}

// Close finishes the archive and atomically renames it into place at the
// path passed to NewWriter. Call it only once every page has been written
// successfully; on any WritePage error, call Abort instead so a partial
// archive is never published under the final name.
func (w *Writer) Close() error {
	if err := w.zw.Close(); err != nil {
		w.tmp.Close()
		os.Remove(w.tmpPath)
		return fmt.Errorf("cbz: finishing %s: %w", w.tmpPath, err)
	}
	if err := w.tmp.Close(); err != nil {
		os.Remove(w.tmpPath)
		return fmt.Errorf("cbz: closing %s: %w", w.tmpPath, err)
	}
	if err := os.Rename(w.tmpPath, w.path); err != nil {
		os.Remove(w.tmpPath)
		return fmt.Errorf("cbz: renaming %s to %s: %w", w.tmpPath, w.path, err)
	}
	return nil
}

// Abort discards the in-progress archive and removes its temp file. Call
// it instead of Close when WritePage returned an error or the download
// was cancelled partway through.
func (w *Writer) Abort() {
	w.tmp.Close()
	os.Remove(w.tmpPath)
}
