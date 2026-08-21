// Package repo fetches and installs Aidoku sources from a source-repository
// index (e.g. https://aidoku-community.github.io/sources/index.min.json),
// the same JSON format the Aidoku app itself consumes when a repository URL
// is added in its settings.
package repo

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
)

// Index is a parsed repository index.
type Index struct {
	Name    string   `json:"name"`
	Sources []Source `json:"sources"`
}

// Source is one entry in a repository index. IconURL and DownloadURL are
// resolved to absolute URLs by FetchIndex (the index itself stores them
// relative to its own location).
type Source struct {
	ID            string   `json:"id"`
	Name          string   `json:"name"`
	Version       int      `json:"version"`
	IconURL       string   `json:"iconURL"`
	DownloadURL   string   `json:"downloadURL"`
	Languages     []string `json:"languages"`
	ContentRating int      `json:"contentRating"`
	BaseURL       string   `json:"baseURL"`
	MinAppVersion string   `json:"minAppVersion,omitempty"`
	AltNames      []string `json:"altNames,omitempty"`
}

// Find returns the source with the given id, or nil if the index has none.
func (idx *Index) Find(id string) *Source {
	for i := range idx.Sources {
		if idx.Sources[i].ID == id {
			return &idx.Sources[i]
		}
	}
	return nil
}

// FetchIndex downloads and parses the repository index at indexURL.
func FetchIndex(ctx context.Context, indexURL string) (*Index, error) {
	base, err := url.Parse(indexURL)
	if err != nil {
		return nil, fmt.Errorf("repo: parsing index URL %q: %w", indexURL, err)
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, indexURL, nil)
	if err != nil {
		return nil, fmt.Errorf("repo: building request: %w", err)
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return nil, fmt.Errorf("repo: fetching %s: %w", indexURL, err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("repo: fetching %s: HTTP %d", indexURL, resp.StatusCode)
	}

	var idx Index
	if err := json.NewDecoder(resp.Body).Decode(&idx); err != nil {
		return nil, fmt.Errorf("repo: decoding %s: %w", indexURL, err)
	}

	for i := range idx.Sources {
		idx.Sources[i].IconURL = resolve(base, idx.Sources[i].IconURL)
		idx.Sources[i].DownloadURL = resolve(base, idx.Sources[i].DownloadURL)
	}
	return &idx, nil
}

// resolve interprets ref relative to base, mirroring how a browser would
// resolve a relative link found in a document served from base. An empty or
// unparseable ref is returned unchanged.
func resolve(base *url.URL, ref string) string {
	if ref == "" {
		return ""
	}
	u, err := url.Parse(ref)
	if err != nil {
		return ref
	}
	return base.ResolveReference(u).String()
}

// Install fetches the repository index at indexURL, downloads the .aix for
// sourceID, and saves it into destDir as "<id>-v<version>.aix" (the naming
// convention Aidoku's own build tooling uses). It returns the path to the
// downloaded file.
func Install(ctx context.Context, indexURL, sourceID, destDir string) (string, error) {
	idx, err := FetchIndex(ctx, indexURL)
	if err != nil {
		return "", err
	}
	src := idx.Find(sourceID)
	if src == nil {
		return "", fmt.Errorf("repo: no source %q in %s", sourceID, indexURL)
	}
	if src.DownloadURL == "" {
		return "", fmt.Errorf("repo: source %q has no downloadURL", sourceID)
	}
	return download(ctx, src.DownloadURL, destDir, fmt.Sprintf("%s-v%d.aix", src.ID, src.Version))
}

// download streams the body of url into destDir/name, returning the
// resulting path. The destination directory is created if needed, and a
// partial file is removed if the copy fails partway through.
func download(ctx context.Context, url, destDir, name string) (string, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return "", fmt.Errorf("repo: building request: %w", err)
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return "", fmt.Errorf("repo: downloading %s: %w", url, err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return "", fmt.Errorf("repo: downloading %s: HTTP %d", url, resp.StatusCode)
	}

	if err := os.MkdirAll(destDir, 0o755); err != nil {
		return "", fmt.Errorf("repo: creating %s: %w", destDir, err)
	}
	dest := filepath.Join(destDir, name)

	f, err := os.Create(dest)
	if err != nil {
		return "", fmt.Errorf("repo: creating %s: %w", dest, err)
	}
	if _, err := io.Copy(f, resp.Body); err != nil {
		f.Close()
		os.Remove(dest)
		return "", fmt.Errorf("repo: writing %s: %w", dest, err)
	}
	if err := f.Close(); err != nil {
		return "", fmt.Errorf("repo: closing %s: %w", dest, err)
	}
	return dest, nil
}
