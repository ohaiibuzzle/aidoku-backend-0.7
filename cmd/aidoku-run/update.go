// update.go implements the "update" command: checking the KOReader plugin's
// own version against the latest GitHub release and applying it in place.
// See koreader-release.yml for how the release assets this talks to are
// built and named ("aidoku-koplugin-<bin_arch>-<tag>.zip", one per
// koreader/build.sh target, with a VERSION file stamped inside each bundle).
package main

import (
	"archive/zip"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"
)

// defaultUpdateRepo is the GitHub "owner/repo" slug releases are fetched
// from. Overridable via AIDOKU_UPDATE_REPO (threaded from Lua's
// sourceEnv(), same mechanism as FLARESOLVERR_HOST/AIDOKU_SETTINGS_DIR) for
// forks that publish their own releases under a different slug.
const defaultUpdateRepo = "ohaiibuzzle/aidoku-backend-0.7"

// maxUpdateAssetBytes bounds both the downloaded zip and its extracted
// contents -- this is a small Lua+binary bundle (a few hundred KB of Lua
// plus one ~10MB stripped Go binary), so anything wildly larger than that
// signals a bad release asset or a compromised/spoofed response rather than
// a legitimate one, and Kindle-class devices have very little RAM headroom
// to spare on decompressing an oversized payload (see the Memory
// constraints section in CLAUDE.md).
const maxUpdateAssetBytes = 64 << 20 // 64MB

func handleUpdate(ctx context.Context, args []string) error {
	if len(args) < 1 {
		return fmt.Errorf("usage: update check <bin-arch> <current-version> | update apply <plugin-dir> <asset-url>")
	}
	switch args[0] {
	case "check":
		if len(args) < 3 {
			return fmt.Errorf("usage: update check <bin-arch> <current-version>")
		}
		result, err := checkUpdate(ctx, args[1], args[2])
		if err != nil {
			return err
		}
		return printJSON(result)

	case "apply":
		if len(args) < 3 {
			return fmt.Errorf("usage: update apply <plugin-dir> <asset-url>")
		}
		result, err := applyUpdate(ctx, args[1], args[2])
		if err != nil {
			return err
		}
		return printJSON(result)

	default:
		return fmt.Errorf("unknown update command %q", args[0])
	}
}

type githubRelease struct {
	TagName string        `json:"tag_name"`
	HTMLURL string        `json:"html_url"`
	Assets  []githubAsset `json:"assets"`
}

type githubAsset struct {
	Name               string `json:"name"`
	BrowserDownloadURL string `json:"browser_download_url"`
}

type updateCheckResult struct {
	// Status is one of "up_to_date", "update_available", "unknown_current"
	// (current couldn't be parsed as a version -- e.g. a dev build, or a
	// pre-VERSION-file install -- so latest is still reported but nothing
	// claims to know whether it's actually newer).
	Status     string `json:"status"`
	Current    string `json:"current"`
	Latest     string `json:"latest"`
	ReleaseURL string `json:"releaseUrl"`
	AssetName  string `json:"assetName"`
	AssetURL   string `json:"assetUrl"`
}

func updateRepo() string {
	if r := os.Getenv("AIDOKU_UPDATE_REPO"); r != "" {
		return r
	}
	return defaultUpdateRepo
}

func checkUpdate(ctx context.Context, binArch, currentVersion string) (*updateCheckResult, error) {
	release, err := fetchLatestRelease(ctx)
	if err != nil {
		return nil, err
	}

	prefix := "aidoku-koplugin-" + binArch + "-"
	var asset *githubAsset
	for i := range release.Assets {
		if strings.HasPrefix(release.Assets[i].Name, prefix) {
			asset = &release.Assets[i]
			break
		}
	}
	if asset == nil {
		return nil, fmt.Errorf("update: no release asset for arch %q in latest release %s", binArch, release.TagName)
	}

	result := &updateCheckResult{
		Current:    currentVersion,
		Latest:     release.TagName,
		ReleaseURL: release.HTMLURL,
		AssetName:  asset.Name,
		AssetURL:   asset.BrowserDownloadURL,
	}

	cur, curOK := parseVersion(currentVersion)
	latest, latestOK := parseVersion(release.TagName)
	switch {
	case !curOK || !latestOK:
		result.Status = "unknown_current"
	case compareVersions(cur, latest) < 0:
		result.Status = "update_available"
	default:
		result.Status = "up_to_date"
	}
	return result, nil
}

func fetchLatestRelease(ctx context.Context) (*githubRelease, error) {
	apiURL := "https://api.github.com/repos/" + updateRepo() + "/releases/latest"
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, apiURL, nil)
	if err != nil {
		return nil, fmt.Errorf("update: building request: %w", err)
	}
	// GitHub's API rejects requests with no User-Agent outright (403).
	req.Header.Set("User-Agent", "aidoku-run")
	req.Header.Set("Accept", "application/vnd.github+json")

	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return nil, fmt.Errorf("update: fetching latest release: %w", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("update: fetching latest release: HTTP %d", resp.StatusCode)
	}

	var release githubRelease
	if err := json.NewDecoder(io.LimitReader(resp.Body, 1<<20)).Decode(&release); err != nil {
		return nil, fmt.Errorf("update: decoding release response: %w", err)
	}
	return &release, nil
}

type version [3]int

// parseVersion accepts "v1.2.3", "1.2.3", or "1.2.3-suffix" (a trailing
// "-something" on the patch component, as a real git describe/prerelease
// tag might carry, is ignored for comparison purposes). Anything else
// (a raw commit hash, "dev-<sha>", an empty string) reports ok=false.
func parseVersion(s string) (v version, ok bool) {
	s = strings.TrimPrefix(s, "v")
	parts := strings.SplitN(s, ".", 3)
	if len(parts) != 3 {
		return v, false
	}
	for i, p := range parts {
		if i == 2 {
			if dash := strings.IndexByte(p, '-'); dash >= 0 {
				p = p[:dash]
			}
		}
		n, err := strconv.Atoi(p)
		if err != nil {
			return v, false
		}
		v[i] = n
	}
	return v, true
}

func compareVersions(a, b version) int {
	for i := range a {
		if a[i] != b[i] {
			if a[i] < b[i] {
				return -1
			}
			return 1
		}
	}
	return 0
}

type updateApplyResult struct {
	Version string `json:"version"`
	Path    string `json:"path"`
}

// applyUpdate downloads assetURL (a release zip built by
// koreader-release.yml, containing a single top-level aidoku.koplugin/
// directory) and atomically replaces pluginDir with its contents:
// extract-to-sibling-temp-dir, then rename-swap, with rollback if the
// second rename fails. This is deliberately not a copy-in-place -- Lua
// require() caches modules process-wide for the plugin's lifetime, so the
// already-running process never sees these files again until KOReader is
// restarted regardless of how they're replaced; renaming instead of
// overwriting file-by-file means a reader mid-read never observes a
// half-updated plugin directory, and renaming pluginDir out from under
// aidoku-run's own currently-executing binary (which lives inside it, at
// bin/<arch>/aidoku-run) is safe on Linux -- a running executable stays
// valid after its backing path is renamed or unlinked, the same property
// the cross-process download lock's never-deleted marker files rely on.
func applyUpdate(ctx context.Context, pluginDir, assetURL string) (*updateApplyResult, error) {
	if !strings.HasPrefix(assetURL, "https://") && !strings.HasPrefix(assetURL, "http://") {
		return nil, fmt.Errorf("update: refusing non-http(s) asset URL %q", assetURL)
	}
	pluginDir = filepath.Clean(pluginDir)
	parent := filepath.Dir(pluginDir)

	zipPath, err := downloadToTemp(ctx, assetURL)
	if err != nil {
		return nil, err
	}
	defer os.Remove(zipPath)

	newDir := filepath.Join(parent, ".aidoku-update-new")
	if err := os.RemoveAll(newDir); err != nil {
		return nil, fmt.Errorf("update: clearing stale %s: %w", newDir, err)
	}
	defer os.RemoveAll(newDir) // no-op once the swap below renames it away

	if err := extractPluginZip(zipPath, newDir); err != nil {
		return nil, err
	}
	if _, err := os.Stat(filepath.Join(newDir, "main.lua")); err != nil {
		return nil, fmt.Errorf("update: downloaded bundle is missing main.lua, refusing to install: %w", err)
	}
	if _, err := os.Stat(filepath.Join(newDir, "_meta.lua")); err != nil {
		return nil, fmt.Errorf("update: downloaded bundle is missing _meta.lua, refusing to install: %w", err)
	}

	version := "unknown"
	if raw, err := os.ReadFile(filepath.Join(newDir, "VERSION")); err == nil {
		version = strings.TrimSpace(string(raw))
	}

	backupDir := filepath.Join(parent, ".aidoku-update-old-"+strconv.FormatInt(time.Now().UnixNano(), 10))
	if err := os.Rename(pluginDir, backupDir); err != nil {
		return nil, fmt.Errorf("update: backing up %s: %w", pluginDir, err)
	}
	if err := os.Rename(newDir, pluginDir); err != nil {
		// Roll back best-effort so a failed update doesn't leave the
		// plugin directory missing entirely.
		_ = os.Rename(backupDir, pluginDir)
		return nil, fmt.Errorf("update: installing new version: %w", err)
	}
	_ = os.RemoveAll(backupDir) // best-effort; a leftover backup is harmless clutter, not a correctness problem

	return &updateApplyResult{Version: version, Path: pluginDir}, nil
}

func downloadToTemp(ctx context.Context, url string) (string, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return "", fmt.Errorf("update: building request: %w", err)
	}
	req.Header.Set("User-Agent", "aidoku-run")

	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return "", fmt.Errorf("update: downloading %s: %w", url, err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return "", fmt.Errorf("update: downloading %s: HTTP %d", url, resp.StatusCode)
	}

	tmp, err := os.CreateTemp("", "aidoku-update-*.zip")
	if err != nil {
		return "", fmt.Errorf("update: creating temp file: %w", err)
	}
	tmpPath := tmp.Name()

	n, err := io.Copy(tmp, io.LimitReader(resp.Body, maxUpdateAssetBytes+1))
	if err != nil {
		tmp.Close()
		os.Remove(tmpPath)
		return "", fmt.Errorf("update: downloading %s: %w", url, err)
	}
	if closeErr := tmp.Close(); closeErr != nil {
		os.Remove(tmpPath)
		return "", fmt.Errorf("update: writing temp file: %w", closeErr)
	}
	if n > maxUpdateAssetBytes {
		os.Remove(tmpPath)
		return "", fmt.Errorf("update: release asset exceeds %d bytes, refusing", maxUpdateAssetBytes)
	}
	return tmpPath, nil
}

// extractPluginZip extracts zipPath into destDir, stripping the single
// top-level "aidoku.koplugin/" directory the release workflow's `zip -r`
// always produces (so destDir itself ends up holding main.lua etc.
// directly, matching pluginDir's own layout). Every entry path is
// validated to stay within destDir before it's ever joined onto a real
// filesystem path -- the zip comes from a network response, so even though
// it's normally our own CI output, nothing here assumes that rather than
// checks it (same posture as the rest of this file's network-input
// handling).
func extractPluginZip(zipPath, destDir string) error {
	r, err := zip.OpenReader(zipPath)
	if err != nil {
		return fmt.Errorf("update: opening downloaded zip: %w", err)
	}
	defer r.Close()

	if len(r.File) == 0 || len(r.File) > 4096 {
		return fmt.Errorf("update: downloaded zip has an implausible entry count (%d)", len(r.File))
	}

	if err := os.MkdirAll(destDir, 0o755); err != nil {
		return fmt.Errorf("update: creating %s: %w", destDir, err)
	}

	var totalUncompressed uint64
	for _, f := range r.File {
		rel := stripTopLevelDir(f.Name)
		if rel == "" {
			continue // the top-level directory entry itself
		}
		cleaned := filepath.Clean(rel)
		if cleaned == ".." || strings.HasPrefix(cleaned, "../") || filepath.IsAbs(cleaned) {
			return fmt.Errorf("update: zip entry %q escapes the extraction directory", f.Name)
		}
		target := filepath.Join(destDir, cleaned)

		if f.FileInfo().IsDir() {
			if err := os.MkdirAll(target, 0o755); err != nil {
				return fmt.Errorf("update: creating %s: %w", target, err)
			}
			continue
		}

		totalUncompressed += f.UncompressedSize64
		if totalUncompressed > maxUpdateAssetBytes {
			return fmt.Errorf("update: downloaded zip's extracted contents exceed %d bytes, refusing", maxUpdateAssetBytes)
		}

		if err := os.MkdirAll(filepath.Dir(target), 0o755); err != nil {
			return fmt.Errorf("update: creating %s: %w", filepath.Dir(target), err)
		}
		if err := extractZipFile(f, target); err != nil {
			return err
		}
	}

	// Belt-and-suspenders: koreader/build.sh chmod +x's every built
	// binary and the CI zip step preserves unix file modes, but explicitly
	// re-asserting the exec bit here means a zip creation quirk can never
	// silently ship a non-executable aidoku-run that only fails once
	// invoked, well after the update already reported success.
	binDir := filepath.Join(destDir, "bin")
	entries, _ := os.ReadDir(binDir)
	for _, e := range entries {
		if !e.IsDir() {
			continue
		}
		bin := filepath.Join(binDir, e.Name(), "aidoku-run")
		if info, err := os.Stat(bin); err == nil {
			_ = os.Chmod(bin, info.Mode()|0o111)
		}
	}

	return nil
}

func extractZipFile(f *zip.File, target string) (err error) {
	rc, err := f.Open()
	if err != nil {
		return fmt.Errorf("update: opening zip entry %q: %w", f.Name, err)
	}
	defer rc.Close()

	mode := f.Mode()
	if mode == 0 {
		mode = 0o644
	}
	out, err := os.OpenFile(target, os.O_WRONLY|os.O_CREATE|os.O_TRUNC, mode)
	if err != nil {
		return fmt.Errorf("update: creating %s: %w", target, err)
	}
	defer func() {
		if cerr := out.Close(); err == nil {
			err = cerr
		}
	}()

	if _, err := io.Copy(out, rc); err != nil {
		return fmt.Errorf("update: writing %s: %w", target, err)
	}
	return nil
}

// stripTopLevelDir removes the first path component ("aidoku.koplugin/")
// from a zip entry name, returning "" for the top-level directory entry
// itself and for anything unexpectedly outside it.
func stripTopLevelDir(name string) string {
	name = strings.TrimPrefix(name, "/")
	idx := strings.IndexByte(name, '/')
	if idx < 0 {
		return "" // a stray top-level file, not something we expect to install
	}
	return name[idx+1:]
}
