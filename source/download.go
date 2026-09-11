package source

import (
	"archive/zip"
	"bytes"
	"context"
	"errors"
	"fmt"
	"image"
	"image/png"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strings"

	"github.com/ohaiibuzzle/aidokurunner-go/cbz"
	"github.com/ohaiibuzzle/aidokurunner-go/models"
	"github.com/ohaiibuzzle/aidokurunner-go/runtime/host"
)

// defaultPageUserAgent is sent on page image requests when a page's
// Context doesn't already specify one. Plenty of image CDNs treat Go's
// default "Go-http-client" UA as a bot signal; Context (set by the source
// itself, e.g. for Referer/UA-sensitive hosts) always takes precedence
// since it's applied after this default.
const defaultPageUserAgent = "Mozilla/5.0 (iPad; CPU iPad OS 26_5_2 like Mac OS X) AppleWebKit/605.1.15 (KHTML, like Gecko) Version/26.5.2 Mobile/15E148 Safari/605.1.15"

// DownloadPage resolves a single Page's content to its raw image bytes,
// handling all three PageContent kinds: URL (HTTP GET, with Context
// applied as request headers), Image (a host.Store descriptor holding a
// previously-decoded image.Image or raw bytes), and ZipFile (a zip
// downloaded from URL, with the page being one entry inside it).
func (s *Source) DownloadPage(ctx context.Context, page models.Page) ([]byte, error) {
	return s.downloadPage(ctx, page, nil)
}

func (s *Source) downloadPage(ctx context.Context, page models.Page, zipCache map[string][]byte) ([]byte, error) {
	switch page.Content.Kind {
	case models.PageContentKindURL:
		return s.downloadURL(ctx, page.Content.URL, page.Content.Context)
	case models.PageContentKindImage:
		return s.resolveImageRef(page.Content.ImageRef)
	case models.PageContentKindZipFile:
		return s.downloadZipEntry(ctx, page.Content.URL, page.Content.FilePath, zipCache)
	default:
		return nil, fmt.Errorf("source: page content kind %d has no downloadable image", page.Content.Kind)
	}
}

func (s *Source) downloadURL(ctx context.Context, rawURL string, headers models.PageContext) ([]byte, error) {
	if rawURL == "" {
		return nil, fmt.Errorf("source: page has empty URL")
	}
	req, err := s.buildImageRequest(ctx, rawURL, headers)
	if err != nil {
		return nil, err
	}

	resp, err := host.SharedHTTPClient().Do(req)
	if err != nil {
		return nil, fmt.Errorf("source: fetching %s: %w", rawURL, err)
	}
	defer resp.Body.Close()
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return nil, fmt.Errorf("source: fetching %s: unexpected status %s", rawURL, resp.Status)
	}

	data, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, fmt.Errorf("source: reading %s: %w", rawURL, err)
	}
	return data, nil
}

// buildImageRequest builds the outgoing HTTP request for a page-content URL
// (a single image, or a ZipFile page's containing zip -- downloadZipEntry
// reaches here via downloadURL too, since it's the same class of fetch).
func (s *Source) buildImageRequest(ctx context.Context, rawURL string, headers models.PageContext) (*http.Request, error) {
	netReq, err := s.GetImageRequest(ctx, rawURL, &headers)
	if err == nil {
		req, err := netReq.ToHTTPRequest(ctx)
		if err != nil {
			return nil, fmt.Errorf("source: building guest-supplied request for %s: %w", rawURL, err)
		}
		// ToHTTPRequest returns (nil, nil) when the guest's NetRequest never
		// had its URL set (same sentinel net.go's send() already checks for)
		// -- fall through to the default rawURL-based request below instead
		// of dereferencing a nil *http.Request.
		if req != nil {
			if req.Header.Get("User-Agent") == "" {
				req.Header.Set("User-Agent", defaultPageUserAgent)
			}
			return req, nil
		}
	} else {
		var srcErr *models.SourceError
		if !errors.As(err, &srcErr) || srcErr.Kind != models.SourceErrorUnimplemented {
			return nil, fmt.Errorf("source: getting image request for %s: %w", rawURL, err)
		}
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, rawURL, nil)
	if err != nil {
		return nil, fmt.Errorf("source: building request for %s: %w", rawURL, err)
	}
	req.Header.Set("User-Agent", defaultPageUserAgent)
	for k, v := range headers {
		req.Header.Set(k, v)
	}
	return req, nil
}

func (s *Source) resolveImageRef(ref models.ImageRef) ([]byte, error) {
	// Every other GlobalStore consumer frees its descriptor right after use
	// (see the defer i.RemoveValue(...) calls throughout runner.go); this
	// one didn't, so a decoded (uncompressed, often much bigger than the
	// compressed page bytes) image stayed pinned in memory for the rest of
	// the process for every page a descrambling source produced this way.
	defer s.Runner.RemoveValue(ref)
	switch v := s.Runner.Fetch(ref).(type) {
	case []byte:
		return v, nil
	case image.Image:
		var buf bytes.Buffer
		if err := png.Encode(&buf, v); err != nil {
			return nil, fmt.Errorf("source: encoding stored image %d: %w", ref, err)
		}
		return buf.Bytes(), nil
	default:
		return nil, fmt.Errorf("source: image ref %d not found", ref)
	}
}

func (s *Source) downloadZipEntry(ctx context.Context, zipURL, filePath string, cache map[string][]byte) ([]byte, error) {
	data, ok := cache[zipURL]
	if !ok {
		var err error
		data, err = s.downloadURL(ctx, zipURL, nil)
		if err != nil {
			return nil, fmt.Errorf("source: downloading zip %s: %w", zipURL, err)
		}
		if cache != nil {
			cache[zipURL] = data
		}
	}

	zr, err := zip.NewReader(bytes.NewReader(data), int64(len(data)))
	if err != nil {
		return nil, fmt.Errorf("source: opening zip %s: %w", zipURL, err)
	}
	for _, f := range zr.File {
		if f.Name != filePath {
			continue
		}
		rc, err := f.Open()
		if err != nil {
			return nil, fmt.Errorf("source: opening %s in zip %s: %w", filePath, zipURL, err)
		}
		defer rc.Close()
		return io.ReadAll(rc)
	}
	return nil, fmt.Errorf("source: entry %q not found in zip %s", filePath, zipURL)
}

// DownloadChapterCBZ downloads every page of chapter, in order, to a CBZ at
// outputPath (auto-generated from the manga/chapter title if ""). The base
// filename is always run through sanitizeFilename, even when outputPath is
// caller-supplied -- a manga/chapter title baked into that path can contain
// characters illegal on e-reader filesystems like FAT32/exFAT.
//
// mangaDirName, if given, inserts a new sanitized directory by that name
// between outputPath's (caller-owned, untouched) directory and the chapter
// file, since this function can't otherwise tell a title-derived segment
// needing sanitizing apart from a real caller-controlled directory.
//
// onProgress, if non-nil, is called after each page (1-indexed current,
// total). Returns the path written to. Pages stream straight to disk rather
// than buffering the whole chapter, to avoid OOM on memory-constrained
// devices (e.g. Kindle).
func (s *Source) DownloadChapterCBZ(ctx context.Context, manga models.Manga, chapter models.Chapter, outputPath, mangaDirName string, onProgress func(current, total int)) (string, error) {
	pages, err := s.GetPageList(ctx, manga, chapter)
	if err != nil {
		return "", fmt.Errorf("source: getting page list: %w", err)
	}
	if len(pages) == 0 {
		return "", fmt.Errorf("source: chapter %q has no pages", chapter.Key)
	}

	outputPath, err = resolveOutputPath(outputPath, mangaDirName, manga, chapter)
	if err != nil {
		return "", err
	}
	w, err := cbz.NewWriter(outputPath, len(pages))
	if err != nil {
		return "", err
	}

	zipCache := make(map[string][]byte)
	for i, page := range pages {
		img, err := s.downloadPage(ctx, page, zipCache)
		if err != nil {
			w.Abort()
			return "", fmt.Errorf("source: downloading page %d/%d: %w", i+1, len(pages), err)
		}
		if err := w.WritePage(img); err != nil {
			w.Abort()
			return "", fmt.Errorf("source: writing page %d/%d: %w", i+1, len(pages), err)
		}
		if onProgress != nil {
			onProgress(i+1, len(pages))
		}
	}

	if err := w.Close(); err != nil {
		return "", err
	}
	return outputPath, nil
}

func defaultCBZName(manga models.Manga, chapter models.Chapter) string {
	return sanitizeFilename(fmt.Sprintf("%s - %s", MangaLabel(manga), ChapterLabel(chapter))) + ".cbz"
}

// resolveOutputPath computes the final, sanitized path DownloadChapterCBZ
// should write to and makes sure its directory exists, creating a
// mangaDirName subdirectory along the way if one was requested. Split out
// from DownloadChapterCBZ so this path/filesystem logic -- the only new
// behavior a manga-folder feature actually needs -- can be unit tested
// without a working page-fetching Source (network/WASM), which this
// package's existing tests have no fixture for.
func resolveOutputPath(outputPath, mangaDirName string, manga models.Manga, chapter models.Chapter) (string, error) {
	if outputPath == "" {
		outputPath = defaultCBZName(manga, chapter)
	} else {
		dir, base := filepath.Split(outputPath)
		if mangaDirName != "" {
			dir = filepath.Join(dir, sanitizeFilename(mangaDirName))
		}
		ext := filepath.Ext(base)
		outputPath = filepath.Join(dir, sanitizeFilename(strings.TrimSuffix(base, ext))+ext)
	}
	if dir := filepath.Dir(outputPath); dir != "." {
		if err := os.MkdirAll(dir, 0o755); err != nil {
			return "", fmt.Errorf("source: creating %s: %w", dir, err)
		}
	}
	return outputPath, nil
}

// MangaLabel is manga's display title, falling back to its key when it has
// none.
func MangaLabel(manga models.Manga) string {
	if manga.Title != "" {
		return manga.Title
	}
	return manga.Key
}

// ChapterLabel is chapter's display label: its own title if it has one,
// else "Chapter N" from ChapterNumber, else its key. Title takes priority
// over ChapterNumber (matching the KOReader plugin's own chapterLabel in
// mangabrowser.lua, which this must agree with for a chapter's displayed
// name to be consistent between the plugin's UI and a downloads index
// entry recorded by the "download" command).
func ChapterLabel(chapter models.Chapter) string {
	if chapter.Title != nil && *chapter.Title != "" {
		return *chapter.Title
	}
	if chapter.ChapterNumber != nil {
		n := *chapter.ChapterNumber
		if n == float32(int64(n)) {
			return fmt.Sprintf("Chapter %d", int64(n))
		}
		return fmt.Sprintf("Chapter %g", n)
	}
	return chapter.Key
}

var filenameSanitizer = strings.NewReplacer(
	"/", "-",
	"\\", "-",
	":", "-",
	"*", "-",
	"?", "-",
	"\"", "'",
	"<", "-",
	">", "-",
	"|", "-",
)

// sanitizeFilename is the single authoritative point that turns a
// manga/chapter title into a safe path component. filenameSanitizer only
// strips characters illegal on FAT32/exFAT -- "." and ".." contain none of
// those, so they'd otherwise pass through untouched. resolveOutputPath's
// mangaDirName is used as a bare path component with nothing appended to
// neutralize that (unlike the chapter filename, which always gets a
// ".cbz" suffix), so a manga title of exactly ".." there would resolve
// outside the downloads directory via filepath.Join. Reject both here so
// every caller is safe, not just the ones that happen to append a fixed
// suffix.
func sanitizeFilename(s string) string {
	s = strings.TrimSpace(filenameSanitizer.Replace(s))
	// ASCII control characters (0x00-0x1F, 0x7F) are illegal on the same
	// FAT32/exFAT filesystems filenameSanitizer targets, but contain none of
	// the punctuation it replaces, so they'd otherwise pass through
	// untouched and can still fail cbz.Writer.Close()'s os.Rename. Dropped
	// rather than replaced with a placeholder -- a run of them shouldn't
	// inflate the filename with dashes.
	s = strings.Map(func(r rune) rune {
		if r < 0x20 || r == 0x7F {
			return -1
		}
		return r
	}, s)
	if s == "." || s == ".." {
		return "-"
	}
	return s
}
