package source

import (
	"archive/zip"
	"bytes"
	"context"
	"fmt"
	"image"
	"image/png"
	"io"
	"net/http"
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
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, rawURL, nil)
	if err != nil {
		return nil, fmt.Errorf("source: building request for %s: %w", rawURL, err)
	}
	req.Header.Set("User-Agent", defaultPageUserAgent)
	for k, v := range headers {
		req.Header.Set(k, v)
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

func (s *Source) resolveImageRef(ref models.ImageRef) ([]byte, error) {
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

// DownloadChapterCBZ downloads every page of chapter and writes them, in
// order, to a CBZ archive at outputPath. If outputPath is "", a filename
// is generated from the manga title and chapter number/title. onProgress,
// if non-nil, is called after each page finishes downloading (1-indexed
// current, plus total). Returns the path the archive was written to.
//
// Pages are streamed straight to disk as they download rather than
// buffered in memory for the whole chapter -- on a memory-constrained
// device (e.g. Kindle), holding every page of a long, high-resolution
// chapter in RAM at once can be enough to get the process OOM-killed
// before a single byte reaches disk.
func (s *Source) DownloadChapterCBZ(ctx context.Context, manga models.Manga, chapter models.Chapter, outputPath string, onProgress func(current, total int)) (string, error) {
	pages, err := s.GetPageList(ctx, manga, chapter)
	if err != nil {
		return "", fmt.Errorf("source: getting page list: %w", err)
	}
	if len(pages) == 0 {
		return "", fmt.Errorf("source: chapter %q has no pages", chapter.Key)
	}

	if outputPath == "" {
		outputPath = defaultCBZName(manga, chapter)
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

func sanitizeFilename(s string) string {
	return strings.TrimSpace(filenameSanitizer.Replace(s))
}
