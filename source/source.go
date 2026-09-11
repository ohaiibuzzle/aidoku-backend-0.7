// Package source implements AidokuRunner's Source.swift: loading a
// source's on-disk manifest (source.json/filters.json/settings.json plus
// main.wasm) and layering the static manifest data over the loaded
// runtime.Interpreter's dynamic (guest-provided) equivalents.
package source

import (
	"archive/zip"
	"context"
	"encoding/json"
	"fmt"
	"io/fs"
	"net/http"
	"net/url"
	"os"

	"github.com/ohaiibuzzle/aidokurunner-go/models"
	"github.com/ohaiibuzzle/aidokurunner-go/runtime"
	"github.com/ohaiibuzzle/aidokurunner-go/runtime/host"
)

// SettingsStore is the subset of settingsstore.Store Source needs to
// register manifest-derived setting defaults, kept as an interface so this
// package doesn't depend on settingsstore directly.
type SettingsStore interface {
	RegisterDefault(key string, value any)
}

const APIVersion = "0.7"

// AidokuRunnerRef pins the github.com/Aidoku/AidokuRunner commit this
// package was last audited against for behavioral parity (models, host
// namespace surface, error codes, ...). Bump it whenever upstream is
// re-diffed and any resulting gaps are ported, so the next audit knows
// exactly where to start `git log` from.
const AidokuRunnerRef = "cc4d06ff399e7169b9c647bccede7cb29bc805c6" // 2026-08-30: "feat: add cover image processing api"

type Source struct {
	// Path is whatever was passed to LoadPath/LoadDir/LoadZip: a directory
	// or an .aix/.zip archive path.
	Path          string
	Key           string
	Name          string
	Version       int
	Languages     []string
	URLs          []string
	ContentRating models.SourceContentRating
	// IconData holds icon.png's raw bytes, if present. Unlike a bare file
	// path, this works uniformly whether the source came from a plain
	// directory or was read out of a zip archive.
	IconData []byte
	Config   *models.SourceConfiguration

	staticListings []models.Listing
	staticFilters  []models.Filter
	StaticSettings []models.Setting

	Runner *runtime.Interpreter

	settings SettingsStore
}

// LoadPath transparently loads a source from either a plain directory
// (source.json/main.wasm/... as files) or a packaged .aix/.zip archive
// (the same files under a top-level "Payload/" directory, as produced by
// the Aidoku build tooling) — whichever path points to.
func LoadPath(ctx context.Context, path string, interpConfig runtime.Config, settings SettingsStore) (*Source, error) {
	info, err := os.Stat(path)
	if err != nil {
		return nil, fmt.Errorf("source: stat %s: %w", path, err)
	}
	if info.IsDir() {
		return LoadDir(ctx, path, interpConfig, settings)
	}
	return LoadZip(ctx, path, interpConfig, settings)
}

// LoadDir loads a source from a plain on-disk directory (source.json,
// main.wasm, and optionally filters.json/settings.json/icon.png as
// sibling files), mirroring Source.init(url:interpreterConfig:).
func LoadDir(ctx context.Context, dir string, interpConfig runtime.Config, settings SettingsStore) (*Source, error) {
	s, err := loadFS(ctx, os.DirFS(dir), interpConfig, settings)
	if err != nil {
		return nil, err
	}
	s.Path = dir
	return s, nil
}

// LoadZip loads a source from a packaged .aix (or plain .zip) archive: the
// same source.json/main.wasm/etc. files, nested under a top-level
// "Payload/" directory inside the zip.
func LoadZip(ctx context.Context, zipPath string, interpConfig runtime.Config, settings SettingsStore) (*Source, error) {
	r, err := zip.OpenReader(zipPath)
	if err != nil {
		return nil, fmt.Errorf("source: opening %s: %w", zipPath, err)
	}
	defer r.Close()

	sub, err := fs.Sub(r, "Payload")
	if err != nil {
		return nil, fmt.Errorf("source: %s has no Payload/ directory: %w", zipPath, err)
	}
	s, err := loadFS(ctx, sub, interpConfig, settings)
	if err != nil {
		return nil, err
	}
	s.Path = zipPath
	return s, nil
}

// loadFS mirrors Source.init(url:interpreterConfig:): reads source.json
// (and optionally filters.json/settings.json/icon.png) from fsys, loads
// main.wasm, and synthesizes the language/base-URL settings Source.swift
// adds on top of the manifest.
func loadFS(ctx context.Context, fsys fs.FS, interpConfig runtime.Config, settings SettingsStore) (*Source, error) {
	infoBytes, err := fs.ReadFile(fsys, "source.json")
	if err != nil {
		return nil, fmt.Errorf("source: reading source.json: %w", err)
	}
	var info models.SourceInfo
	if err := json.Unmarshal(infoBytes, &info); err != nil {
		return nil, fmt.Errorf("source: parsing source.json: %w", err)
	}

	s := &Source{
		Key:       info.Info.ID,
		Name:      info.Info.Name,
		Version:   info.Info.Version,
		Languages: info.Info.Languages,
		Config:    info.Config,
		settings:  settings,
	}
	if info.Info.ContentRating != nil {
		s.ContentRating = *info.Info.ContentRating
	} else {
		s.ContentRating = models.SourceContentRatingSafe
	}

	if iconBytes, err := fs.ReadFile(fsys, "icon.png"); err == nil {
		s.IconData = iconBytes
	}

	var urls []string
	if info.Info.URL != nil {
		urls = append(urls, *info.Info.URL)
	}
	urls = append(urls, info.Info.URLs...)

	for _, l := range info.Listings {
		s.staticListings = append(s.staticListings, l.Listing)
	}

	if filtersBytes, err := fs.ReadFile(fsys, "filters.json"); err == nil {
		if err := json.Unmarshal(filtersBytes, &s.staticFilters); err != nil {
			return nil, fmt.Errorf("source: parsing filters.json: %w", err)
		}
	}

	var staticSettings []models.Setting
	if settingsBytes, err := fs.ReadFile(fsys, "settings.json"); err == nil {
		if err := json.Unmarshal(settingsBytes, &staticSettings); err != nil {
			return nil, fmt.Errorf("source: parsing settings.json: %w", err)
		}
	}

	wasmBytes, err := fs.ReadFile(fsys, "main.wasm")
	if err != nil {
		return nil, fmt.Errorf("source: reading main.wasm: %w", err)
	}
	runner, err := runtime.New(ctx, s.Key, wasmBytes, interpConfig)
	if err != nil {
		return nil, fmt.Errorf("source: loading interpreter: %w", err)
	}
	s.Runner = runner

	if runner.Features.ProvidesBaseURL {
		if baseURL, err := runner.GetBaseURL(ctx); err == nil && baseURL != nil {
			if !containsString(urls, *baseURL) {
				urls = append([]string{*baseURL}, urls...)
			}
		}
	}
	s.URLs = urls

	extra := extraSettings(s.Config, s.Languages, s.URLs)
	s.StaticSettings = append(extra, staticSettings...)
	s.loadSettingsDefaults(s.StaticSettings)

	return s, nil
}

func containsString(haystack []string, needle string) bool {
	for _, h := range haystack {
		if h == needle {
			return true
		}
	}
	return false
}

// Features passes through the loaded Interpreter's detected feature flags.
func (s *Source) Features() models.SourceFeatures { return s.Runner.Features }

// --- Cookie injection -> Interpreter ---

// SetCookie adds a single name=value cookie scoped to u for this source.
// See runtime.Interpreter.SetCookie.
func (s *Source) SetCookie(u *url.URL, name, value string) { s.Runner.SetCookie(u, name, value) }

// SetCookieHeader parses a raw "a=1; b=2" Cookie header and injects each
// cookie for u. See runtime.Interpreter.SetCookieHeader.
func (s *Source) SetCookieHeader(u *url.URL, header string) { s.Runner.SetCookieHeader(u, header) }

// SetCookies injects the given cookies for u. See runtime.Interpreter.SetCookies.
func (s *Source) SetCookies(u *url.URL, cookies []*http.Cookie) { s.Runner.SetCookies(u, cookies) }

// Cookies returns the cookies this source's jar would send for u.
func (s *Source) Cookies(u *url.URL) []*http.Cookie { return s.Runner.Cookies(u) }

// LoadCookiesFile reads a Netscape cookies.txt file and injects its cookies
// into this source's jar. See runtime.Interpreter.LoadCookiesFile.
func (s *Source) LoadCookiesFile(path string) (int, error) { return s.Runner.LoadCookiesFile(path) }

// LoadNetscapeCookies injects cookies from Netscape cookies.txt bytes into
// this source's jar. See runtime.Interpreter.LoadNetscapeCookies.
func (s *Source) LoadNetscapeCookies(data []byte) (int, error) {
	return s.Runner.LoadNetscapeCookies(data)
}

// OnlySearch mirrors Source.onlySearch: whether the source should default
// to a search page instead of a home/listings page.
func (s *Source) OnlySearch() bool {
	return !s.Runner.Features.ProvidesHome && !s.HasListings()
}

// HasListings mirrors Source.hasListings.
func (s *Source) HasListings() bool {
	return s.Runner.Features.DynamicListings || len(s.staticListings) > 0
}

func (s *Source) SupportsArtistSearch() bool {
	if s.Config != nil && s.Config.SupportsArtistSearch != nil {
		return *s.Config.SupportsArtistSearch
	}
	for _, f := range s.staticFilters {
		if f.Kind == models.FilterKindText && f.ID == "artist" {
			return true
		}
	}
	return false
}

func (s *Source) SupportsAuthorSearch() bool {
	if s.Config != nil && s.Config.SupportsAuthorSearch != nil {
		return *s.Config.SupportsAuthorSearch
	}
	for _, f := range s.staticFilters {
		if f.Kind == models.FilterKindText && f.ID == "author" {
			return true
		}
	}
	return false
}

// MatchingGenreFilter mirrors Source.matchingGenreFilter(for:).
func (s *Source) MatchingGenreFilter(tag string) *models.FilterValue {
	if s.Config != nil && s.Config.SupportsTagSearch != nil && *s.Config.SupportsTagSearch {
		return &models.FilterValue{Kind: models.FilterValueKindSelect, ID: "genre", Value: tag}
	}
	for _, filter := range s.staticFilters {
		switch filter.Kind {
		case models.FilterKindMultiselect:
			if !filter.MultiSelect.IsGenre {
				continue
			}
			for idx, opt := range filter.MultiSelect.Options {
				if opt != tag {
					continue
				}
				value := opt
				if filter.MultiSelect.IDs != nil && idx < len(filter.MultiSelect.IDs) {
					value = filter.MultiSelect.IDs[idx]
				}
				return &models.FilterValue{
					Kind: models.FilterValueKindMultiselect, ID: filter.ID,
					Included: []string{value}, Excluded: []string{},
				}
			}
		case models.FilterKindSelect:
			if !filter.Select.IsGenre {
				continue
			}
			for idx, opt := range filter.Select.Options {
				if opt != tag {
					continue
				}
				value := opt
				if filter.Select.IDs != nil && idx < len(filter.Select.IDs) {
					value = filter.Select.IDs[idx]
				}
				return &models.FilterValue{Kind: models.FilterValueKindSelect, ID: filter.ID, Value: value}
			}
		}
	}
	return nil
}

// --- Delegation to the loaded Interpreter, layering static manifest data
// over dynamic guest-provided data, matching Source.swift's `public
// extension Source` block. ---

func (s *Source) GetSearchMangaList(ctx context.Context, query *string, page int, filters []models.FilterValue) (*models.MangaPageResult, error) {
	if query != nil && *query != "" && s.Config != nil && s.Config.HidesFiltersWhileSearching != nil && *s.Config.HidesFiltersWhileSearching {
		filters = nil
	}
	result, err := s.Runner.GetSearchMangaList(ctx, query, page, filters)
	if err != nil {
		return nil, err
	}
	result.SetSourceKey(s.Key)
	return result, nil
}

func (s *Source) GetMangaUpdate(ctx context.Context, manga models.Manga, needsDetails, needsChapters bool, onPartial func(models.Manga)) (*models.Manga, error) {
	result, err := s.Runner.GetMangaUpdate(ctx, manga, needsDetails, needsChapters, onPartial)
	if err != nil {
		return nil, err
	}
	result.SourceKey = s.Key
	if len(s.Languages) == 1 && result.Chapters != nil {
		lang := s.Languages[0]
		for i := range result.Chapters {
			if result.Chapters[i].Language == nil {
				result.Chapters[i].Language = &lang
			}
		}
	}
	return result, nil
}

func (s *Source) GetPageList(ctx context.Context, manga models.Manga, chapter models.Chapter) ([]models.Page, error) {
	return s.Runner.GetPageList(ctx, manga, chapter)
}

func (s *Source) GetMangaList(ctx context.Context, listing models.Listing, page int) (*models.MangaPageResult, error) {
	if !s.Runner.Features.ProvidesListings {
		return nil, models.ErrUnimplemented()
	}
	result, err := s.Runner.GetMangaList(ctx, listing, page)
	if err != nil {
		return nil, err
	}
	result.SetSourceKey(s.Key)
	return result, nil
}

func (s *Source) GetHome(ctx context.Context, onPartial func(models.Home)) (*models.Home, error) {
	if !s.Runner.Features.ProvidesHome {
		return nil, models.ErrUnimplemented()
	}
	result, err := s.Runner.GetHome(ctx, onPartial)
	if err != nil {
		return nil, err
	}
	result.SetSourceKey(s.Key)
	return result, nil
}

func (s *Source) ProcessPageImage(ctx context.Context, response models.Response, pageContext *models.PageContext) (*int32, error) {
	if !s.Runner.Features.ProcessesPages {
		return nil, nil
	}
	ref, err := s.Runner.ProcessPageImage(ctx, response, pageContext)
	if err != nil {
		return nil, err
	}
	return &ref, nil
}

// Restart recovers this source's runtime after a WASM trap (e.g. a guest
// panic) by re-instantiating the loaded main.wasm bytes fresh, without
// reloading the .aix from disk. See runtime.Interpreter.Restart for what
// this does and doesn't reset.
func (s *Source) Restart(ctx context.Context) error {
	return s.Runner.Restart(ctx)
}

func (s *Source) ProcessCoverImage(ctx context.Context, response models.Response) (*int32, error) {
	if !s.Runner.Features.ProcessesCovers {
		return nil, nil
	}
	ref, err := s.Runner.ProcessCoverImage(ctx, response)
	if err != nil {
		return nil, err
	}
	return &ref, nil
}

func (s *Source) GetListings(ctx context.Context) ([]models.Listing, error) {
	if !s.Runner.Features.DynamicListings {
		return s.staticListings, nil
	}
	dynamic, err := s.Runner.GetListings(ctx)
	if err != nil {
		return nil, err
	}
	return append(append([]models.Listing{}, s.staticListings...), dynamic...), nil
}

func (s *Source) GetSearchFilters(ctx context.Context) ([]models.Filter, error) {
	if !s.Runner.Features.DynamicFilters {
		return s.staticFilters, nil
	}
	dynamic, err := s.Runner.GetSearchFilters(ctx)
	if err != nil {
		return nil, err
	}
	return append(append([]models.Filter{}, s.staticFilters...), dynamic...), nil
}

func (s *Source) GetSettings(ctx context.Context) ([]models.Setting, error) {
	if !s.Runner.Features.DynamicSettings {
		return s.StaticSettings, nil
	}
	dynamic, err := s.Runner.GetSettings(ctx)
	if err != nil {
		return nil, err
	}
	s.loadSettingsDefaults(dynamic)
	return append(append([]models.Setting{}, s.StaticSettings...), dynamic...), nil
}

func (s *Source) GetImageRequest(ctx context.Context, url string, pageContext *models.PageContext) (*host.NetRequest, error) {
	if !s.Runner.Features.ProvidesImageRequests {
		return nil, models.ErrUnimplemented()
	}
	return s.Runner.GetImageRequest(ctx, url, pageContext)
}

func (s *Source) GetPageDescription(ctx context.Context, page models.Page) (*string, error) {
	if page.Description != nil {
		return page.Description, nil
	}
	if !s.Runner.Features.ProvidesPageDescriptions {
		return nil, nil
	}
	return s.Runner.GetPageDescription(ctx, page)
}

func (s *Source) GetAlternateCovers(ctx context.Context, manga models.Manga) ([]string, error) {
	if !s.Runner.Features.ProvidesAlternateCovers {
		return nil, nil
	}
	return s.Runner.GetAlternateCovers(ctx, manga)
}

func (s *Source) GetBaseURL(ctx context.Context) (*string, error) {
	if !s.Runner.Features.ProvidesBaseURL {
		return nil, nil
	}
	return s.Runner.GetBaseURL(ctx)
}

func (s *Source) HandleNotification(ctx context.Context, notification string) error {
	if !s.Runner.Features.HandlesNotifications {
		return nil
	}
	return s.Runner.HandleNotification(ctx, notification)
}

func (s *Source) HandleDeepLink(ctx context.Context, url string) (*models.DeepLinkResult, error) {
	if !s.Runner.Features.HandlesDeepLinks {
		return nil, nil
	}
	return s.Runner.HandleDeepLink(ctx, url)
}

func (s *Source) HandleBasicLogin(ctx context.Context, key, username, password string) (bool, error) {
	if !s.Runner.Features.HandlesBasicLogin {
		return true, nil
	}
	return s.Runner.HandleBasicLogin(ctx, key, username, password)
}

func (s *Source) HandleWebLogin(ctx context.Context, key string, cookies map[string]string) (bool, error) {
	if !s.Runner.Features.HandlesWebLogin {
		return true, nil
	}
	return s.Runner.HandleWebLogin(ctx, key, cookies)
}

func (s *Source) HandleMigration(ctx context.Context, kind models.KeyKind, mangaKey string, chapterKey *string) (*string, error) {
	if !s.Runner.Features.HandlesMigration {
		return nil, nil
	}
	result, err := s.Runner.HandleMigration(ctx, kind, mangaKey, chapterKey)
	if err != nil {
		return nil, err
	}
	return &result, nil
}
