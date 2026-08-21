// Command aidoku-run is a minimal CLI test harness for exercising a
// compiled Aidoku source directory (source.json + main.wasm, optionally
// filters.json/settings.json) from the terminal, since AidokuRunner itself
// has no standalone binary — it's a library the Aidoku app embeds.
package main

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"github.com/ohaiibuzzle/aidokurunner-go/models"
	"github.com/ohaiibuzzle/aidokurunner-go/repo"
	"github.com/ohaiibuzzle/aidokurunner-go/runtime"
	"github.com/ohaiibuzzle/aidokurunner-go/runtime/host"
	"github.com/ohaiibuzzle/aidokurunner-go/settingsstore"
	"github.com/ohaiibuzzle/aidokurunner-go/source"
)

func main() {
	if len(os.Args) < 2 {
		usage()
		os.Exit(2)
	}
	dir := os.Args[1]
	command := "info"
	var rest []string
	if len(os.Args) > 2 {
		command = os.Args[2]
		rest = os.Args[3:]
	}

	if err := run(dir, command, rest); err != nil {
		fmt.Fprintln(os.Stderr, "error:", err)
		os.Exit(1)
	}
}

func usage() {
	fmt.Fprintln(os.Stderr, `usage: aidoku-run <source-dir> [command] [args...]

commands:
  info                              print source metadata and detected features (default)
  search <query> [page]             call get_search_manga_list
  home                              call get_home
  listings                          list static + dynamic listings
  filters                           list static + dynamic filters
  settings                          list static + dynamic settings
  manga <key>                       call get_manga_update (details + chapters)
  pages <manga-key> <chapter-key>   call get_manga_update then get_page_list for the matching chapter
  download <manga-key> <chapter-key> [output.cbz]
                                     download every page of a chapter and combine them into a CBZ archive
  cookie load <cookies.txt>          load cookies from a Netscape cookies.txt file (cf_clearance etc.)
  cookie list                        show stored cookie domains
  repo list <index-url>              fetch a source-repository index (index.min.json) and list its sources
  repo install <index-url> <source-id> <dest-dir>
                                     download a source's .aix from a repository index into dest-dir
Cookies stored with `+"`cookie load`"+` are injected into a source's requests
for the matching domain on every command, helping get past Cloudflare.

The <source-dir> argument is required but ignored by the `+"`cookie`"+` and `+"`repo`"+`
subcommands, which don't operate on a loaded source.`)
}

func run(dir, command string, args []string) error {
	ctx := context.Background()

	settingsPath := filepath.Join(os.TempDir(), "aidoku-run-settings.json")
	settings, err := settingsstore.Open(settingsPath)
	if err != nil {
		return fmt.Errorf("opening settings store: %w", err)
	}

	cookiePath := filepath.Join(os.TempDir(), "aidoku-run-cookies.json")

	// If command is "cookie", handle it before loading the source (which is
	// otherwise only needed so its base URLs are known for display/command
	// dispatch).
	if command == "cookie" {
		return handleCookie(cookiePath, args)
	}
	if command == "repo" {
		return handleRepo(ctx, args)
	}

	src, err := source.LoadPath(ctx, dir, runtime.Config{
		PrintHandler: func(s string) { fmt.Fprintln(os.Stderr, "[print]", s) },
		Settings:     settings,
		// When FLARESOLVERR_HOST is set and a source's request hits a
		// Cloudflare challenge, the resulting cookies get persisted here
		// too (in addition to the in-memory jar), same as `cookie load`.
		OnFlareSolverrCookies: func(u *url.URL, cookies []*http.Cookie) {
			persistCookies(cookiePath, cookies)
		},
	}, settings)
	if err != nil {
		return fmt.Errorf("loading source: %w", err)
	}
	defer src.Runner.Close(ctx)

	// Inject stored cookies (e.g. a browser's cf_clearance) into the source's
	// jar for any of its base-url domains before running the requested
	// command.
	injectCookies(src, cookiePath)

	switch command {
	case "info", "":
		return printJSON(map[string]any{
			"key":           src.Key,
			"name":          src.Name,
			"version":       src.Version,
			"languages":     src.Languages,
			"urls":          src.URLs,
			"contentRating": src.ContentRating,
			"onlySearch":    src.OnlySearch(),
			"hasListings":   src.HasListings(),
			"features":      src.Features(),
		})

	case "search":
		if len(args) < 1 {
			return fmt.Errorf("usage: search <query> [page]")
		}
		page := 1
		if len(args) > 1 {
			fmt.Sscanf(args[1], "%d", &page)
		}
		query := args[0]
		result, err := src.GetSearchMangaList(ctx, &query, page, nil)
		if err != nil {
			return err
		}
		return printJSON(result)

	case "home":
		result, err := src.GetHome(ctx, nil)
		if err != nil {
			return err
		}
		return printJSON(result)

	case "listings":
		result, err := src.GetListings(ctx)
		if err != nil {
			return err
		}
		return printJSON(result)

	case "filters":
		result, err := src.GetSearchFilters(ctx)
		if err != nil {
			return err
		}
		return printJSON(result)

	case "settings":
		result, err := src.GetSettings(ctx)
		if err != nil {
			return err
		}
		return printJSON(result)

	case "manga":
		if len(args) < 1 {
			return fmt.Errorf("usage: manga <key>")
		}
		manga := models.Manga{Key: args[0]}
		result, err := src.GetMangaUpdate(ctx, manga, true, true, nil)
		if err != nil {
			return err
		}
		return printJSON(result)

	case "pages":
		if len(args) < 2 {
			return fmt.Errorf("usage: pages <manga-key> <chapter-key>")
		}
		manga := models.Manga{Key: args[0]}
		updated, err := src.GetMangaUpdate(ctx, manga, true, true, nil)
		if err != nil {
			return err
		}
		var chapter *models.Chapter
		for i := range updated.Chapters {
			if updated.Chapters[i].Key == args[1] {
				chapter = &updated.Chapters[i]
				break
			}
		}
		if chapter == nil {
			return fmt.Errorf("chapter %q not found on manga %q", args[1], args[0])
		}
		pages, err := src.GetPageList(ctx, *updated, *chapter)
		if err != nil {
			return err
		}
		return printJSON(pages)

	case "download":
		if len(args) < 2 {
			return fmt.Errorf("usage: download <manga-key> <chapter-key> [output.cbz]")
		}
		manga := models.Manga{Key: args[0]}
		updated, err := src.GetMangaUpdate(ctx, manga, true, true, nil)
		if err != nil {
			return err
		}
		var chapter *models.Chapter
		for i := range updated.Chapters {
			if updated.Chapters[i].Key == args[1] {
				chapter = &updated.Chapters[i]
				break
			}
		}
		if chapter == nil {
			return fmt.Errorf("chapter %q not found on manga %q", args[1], args[0])
		}
		outputPath := ""
		if len(args) > 2 {
			outputPath = args[2]
		}
		path, err := src.DownloadChapterCBZ(ctx, *updated, *chapter, outputPath, func(current, total int) {
			fmt.Fprintf(os.Stderr, "[download] page %d/%d\n", current, total)
		})
		if err != nil {
			return err
		}
		fmt.Println(path)
		return nil

	default:
		usage()
		return fmt.Errorf("unknown command %q", command)
	}
}

func printJSON(v any) error {
	enc := json.NewEncoder(os.Stdout)
	enc.SetIndent("", "  ")
	return enc.Encode(v)
}

// cookieEntry is one stored cookie. Domain is the bare host (no scheme, no
// leading ".", no "www."); IncludeSubdomains=true makes it a domain cookie
// (sent to the host and its subdomains), false makes it host-only.
type cookieEntry struct {
	Domain            string `json:"domain"`
	IncludeSubdomains bool   `json:"include_subdomains"`
	Path              string `json:"path,omitempty"`
	Secure            bool   `json:"secure"`
	Expires           int64  `json:"expires,omitempty"` // unix seconds; 0 = no expiry
	Name              string `json:"name"`
	Value             string `json:"value"`
}

// cookie maps a normalized domain to its stored cookies. Persisted so cookies
// added in one invocation are injected on the next.
type cookieFile map[string][]cookieEntry

func readCookieFile(path string) cookieFile {
	b, err := os.ReadFile(path)
	if err != nil {
		return cookieFile{}
	}
	var f cookieFile
	if err := json.Unmarshal(b, &f); err != nil {
		return cookieFile{}
	}
	if f == nil {
		f = cookieFile{}
	}
	return f
}

func writeCookieFile(path string, f cookieFile) error {
	b, err := json.MarshalIndent(f, "", "  ")
	if err != nil {
		return err
	}
	return os.WriteFile(path, b, 0o600)
}

// cookie returns the *http.Cookie corresponding to this stored entry.
func (e cookieEntry) cookie() *http.Cookie {
	c := &http.Cookie{Name: e.Name, Value: e.Value, Path: e.Path}
	if c.Path == "" {
		c.Path = "/"
	}
	c.Secure = e.Secure
	if e.Expires > 0 {
		c.Expires = time.Unix(e.Expires, 0)
	}
	if e.IncludeSubdomains {
		c.Domain = "." + e.Domain
	} else {
		c.Domain = e.Domain
	}
	return c
}

// entriesFromNetscape converts parsed cookies.txt cookies into storage
// entries, preserving domain/subdomain/path/secure/expiry.
func entriesFromNetscape(cs []*http.Cookie) []cookieEntry {
	var out []cookieEntry
	for _, c := range cs {
		e := cookieEntry{Name: c.Name, Value: c.Value, Path: c.Path, Secure: c.Secure}
		if c.Domain != "" {
			e.IncludeSubdomains = strings.HasPrefix(c.Domain, ".")
			e.Domain = strings.TrimPrefix(c.Domain, ".")
		}
		if !c.Expires.IsZero() {
			e.Expires = c.Expires.Unix()
		}
		out = append(out, e)
	}
	return out
}

// persistCookies merges cookies (e.g. a solved cf_clearance from
// FlareSolverr) into the cookie store at path, so they're injected again on
// the next invocation. Per domain, incoming cookies replace any existing
// entry of the same name (a fresh solve supersedes a stale one) while
// leaving other cookies for that domain untouched.
func persistCookies(path string, cookies []*http.Cookie) {
	if len(cookies) == 0 {
		return
	}
	f := readCookieFile(path)
	byDomain := map[string][]cookieEntry{}
	for _, e := range entriesFromNetscape(cookies) {
		if e.Domain == "" {
			continue
		}
		byDomain[e.Domain] = append(byDomain[e.Domain], e)
	}
	for domain, incoming := range byDomain {
		merged := make([]cookieEntry, 0, len(f[domain])+len(incoming))
		for _, e := range f[domain] {
			if !cookieEntriesContainName(incoming, e.Name) {
				merged = append(merged, e)
			}
		}
		f[domain] = append(merged, incoming...)
	}
	_ = writeCookieFile(path, f)
}

func cookieEntriesContainName(entries []cookieEntry, name string) bool {
	for _, e := range entries {
		if e.Name == name {
			return true
		}
	}
	return false
}

func handleRepo(ctx context.Context, args []string) error {
	if len(args) < 1 {
		return fmt.Errorf("usage: repo list <index-url> | repo install <index-url> <source-id> <dest-dir>")
	}
	switch args[0] {
	case "list":
		if len(args) < 2 {
			return fmt.Errorf("usage: repo list <index-url>")
		}
		idx, err := repo.FetchIndex(ctx, args[1])
		if err != nil {
			return err
		}
		return printJSON(idx)

	case "install":
		if len(args) < 4 {
			return fmt.Errorf("usage: repo install <index-url> <source-id> <dest-dir>")
		}
		path, err := repo.Install(ctx, args[1], args[2], args[3])
		if err != nil {
			return err
		}
		fmt.Println(path)
		return nil

	default:
		return fmt.Errorf("unknown repo command %q", args[0])
	}
}

func handleCookie(path string, args []string) error {
	if len(args) < 1 {
		return fmt.Errorf("usage: cookie load <cookies.txt> | cookie list")
	}
	f := readCookieFile(path)
	switch args[0] {
	case "load":
		if len(args) < 2 {
			return fmt.Errorf("usage: cookie load <cookies.txt>")
		}
		data, err := os.ReadFile(args[1])
		if err != nil {
			return err
		}
		cookies, err := host.ParseNetscapeCookies(data)
		if err != nil {
			return err
		}
		entries := entriesFromNetscape(cookies)
		if len(entries) == 0 {
			fmt.Println("no usable (non-expired) cookies in that file")
			return nil
		}
		byDomain := map[string][]cookieEntry{}
		for _, e := range entries {
			byDomain[e.Domain] = append(byDomain[e.Domain], e)
		}
		for d, es := range byDomain {
			f[d] = es
		}
		if err := writeCookieFile(path, f); err != nil {
			return err
		}
		fmt.Printf("loaded %d cookie(s) from %s\n", len(entries), args[1])
		return nil

	case "list":
		if len(f) == 0 {
			fmt.Println("(no cookies stored)")
			return nil
		}
		domains := make([]string, 0, len(f))
		for d := range f {
			domains = append(domains, d)
		}
		sort.Strings(domains)
		for _, d := range domains {
			for _, e := range f[d] {
				scoped := e.Domain
				if e.IncludeSubdomains {
					scoped = "." + scoped
				}
				fmt.Printf("%s\t%s\n", scoped, e.Name+"="+e.Value)
			}
		}
		return nil

	default:
		return fmt.Errorf("unknown cookie command %q", args[0])
	}
}

// injectCookies pushes every stored cookie into src's shared jar. Because the
// jar is shared process-wide but scoped per-domain, each cookie goes in under
// its own domain URL and is then sent on any request to that host (from any
// source), which is exactly the URLSession.shared behavior upstream relies on.
func injectCookies(src *source.Source, path string) {
	f := readCookieFile(path)
	if len(f) == 0 {
		return
	}
	jar := src.Runner.CookieJar()
	if jar == nil {
		return
	}
	for domain, entries := range f {
		if domain == "" {
			continue
		}
		cookies := make([]*http.Cookie, 0, len(entries))
		for _, e := range entries {
			cookies = append(cookies, e.cookie())
		}
		jar.SetCookies(&url.URL{Scheme: "https", Host: domain, Path: "/"}, cookies)
	}
}
