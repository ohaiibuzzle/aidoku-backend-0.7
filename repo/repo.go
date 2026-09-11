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
	"strings"
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
// convention Aidoku's own build tooling uses). Any other "<id>-v*.aix"
// already in destDir (an older version of the same source) is removed
// afterward, so updating a source replaces it in place instead of
// accumulating both versions side by side -- which would otherwise leave
// two installed entries for what the user thinks of as one source, and
// silently strand anything (library bookmarks, downloaded chapters) still
// keyed to the old file's path. It returns the path to the downloaded file.
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
	// src.ID comes from the fetched index.min.json, not from the caller --
	// a malicious or compromised repository could set it to something like
	// "../../etc/cron.d/x" to write outside destDir via the filepath.Join
	// below, or to a string containing glob metacharacters to make
	// removeOtherVersions delete unrelated files. Reject anything that
	// isn't a plain filename component before it reaches either.
	if !isValidSourceID(src.ID) {
		return "", fmt.Errorf("repo: source %q has an invalid id %q", sourceID, src.ID)
	}
	name := fmt.Sprintf("%s-v%d.aix", src.ID, src.Version)
	path, err := download(ctx, src.DownloadURL, destDir, name)
	if err != nil {
		return "", err
	}
	removeOtherVersions(destDir, src.ID, name)
	return path, nil
}

// isValidSourceID reports whether id is safe to use as a filename
// component: non-empty and containing only characters that can't be
// interpreted as a path separator, a traversal segment, or (in
// removeOtherVersions) a glob metacharacter.
func isValidSourceID(id string) bool {
	if id == "" {
		return false
	}
	for _, r := range id {
		switch {
		case r >= 'a' && r <= 'z', r >= 'A' && r <= 'Z', r >= '0' && r <= '9':
		case r == '.' || r == '_' || r == '-':
		default:
			return false
		}
	}
	return true
}

// removeOtherVersions deletes every "<id>-v*.aix" in dir except keepName,
// best-effort (a removal failure is ignored rather than failing the
// install that already succeeded). Uses a plain prefix/suffix match over
// os.ReadDir rather than filepath.Glob -- id is validated by the caller,
// but matching this way means removeOtherVersions never depends on that
// happening upstream to be safe.
func removeOtherVersions(dir, id, keepName string) {
	entries, err := os.ReadDir(dir)
	if err != nil {
		return
	}
	prefix, suffix := id+"-v", ".aix"
	for _, e := range entries {
		name := e.Name()
		if name == keepName || e.IsDir() {
			continue
		}
		if strings.HasPrefix(name, prefix) && strings.HasSuffix(name, suffix) {
			_ = os.Remove(filepath.Join(dir, name))
		}
	}
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

	// Download to a temp file and rename into place on success (same
	// pattern as settingsstore.save()/cbz.Writer.Close()), so a kill or
	// crash mid-copy never leaves a truncated .aix sitting at the final
	// name -- source.LoadPath would then fail to open it, and
	// removeOtherVersions above already ran, so there'd be no older
	// version left to fall back to either.
	tmp, err := os.CreateTemp(destDir, ".aix-*.tmp")
	if err != nil {
		return "", fmt.Errorf("repo: creating temp file in %s: %w", destDir, err)
	}
	tmpPath := tmp.Name()
	// os.CreateTemp always creates with 0600, not the 0644 os.Create used
	// previously (0666 minus the typical 022 umask); chmod explicitly so
	// the rename doesn't silently tighten the installed .aix's permissions
	// as a side effect of this change (same reasoning as
	// settingsstore.save()'s identical chmod).
	if err := tmp.Chmod(0o644); err != nil {
		tmp.Close()
		os.Remove(tmpPath)
		return "", fmt.Errorf("repo: setting permissions on %s: %w", dest, err)
	}
	if _, err := io.Copy(tmp, resp.Body); err != nil {
		tmp.Close()
		os.Remove(tmpPath)
		return "", fmt.Errorf("repo: writing %s: %w", dest, err)
	}
	if err := tmp.Close(); err != nil {
		os.Remove(tmpPath)
		return "", fmt.Errorf("repo: closing %s: %w", dest, err)
	}
	if err := os.Rename(tmpPath, dest); err != nil {
		os.Remove(tmpPath)
		return "", fmt.Errorf("repo: renaming into %s: %w", dest, err)
	}
	return dest, nil
}
