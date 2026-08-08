// Command aidoku-run is a minimal CLI test harness for exercising a
// compiled Aidoku source directory (source.json + main.wasm, optionally
// filters.json/settings.json) from the terminal, since AidokuRunner itself
// has no standalone binary — it's a library the Aidoku app embeds.
package main

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"

	"github.com/ohaiibuzzle/aidokurunner-go/models"
	"github.com/ohaiibuzzle/aidokurunner-go/runtime"
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
                                     download every page of a chapter and combine them into a CBZ archive`)
}

func run(dir, command string, args []string) error {
	ctx := context.Background()

	settingsPath := filepath.Join(os.TempDir(), "aidoku-run-settings.json")
	settings, err := settingsstore.Open(settingsPath)
	if err != nil {
		return fmt.Errorf("opening settings store: %w", err)
	}

	src, err := source.LoadPath(ctx, dir, runtime.Config{
		PrintHandler: func(s string) { fmt.Fprintln(os.Stderr, "[print]", s) },
		Settings:     settings,
	}, settings)
	if err != nil {
		return fmt.Errorf("loading source: %w", err)
	}
	defer src.Runner.Close(ctx)

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
