// Command aidoku-downloads is a small CLI over the downloads package's
// SQLite-backed index of locally downloaded chapters (which local CBZ file
// backs a given source/manga/chapter, its size, and when it was fetched).
// Kept as its own binary, separate from aidoku-run (a source-execution test
// harness), since this is a different concern entirely -- managing already
// -downloaded files, not running a source. Every JSON field that could be
// "absent" is emitted as a zero value (empty string/array/0) rather than
// null, so callers never have to deal with JSON null.
//
// Deliberately query/deletion only -- no "record" command. Indexing a
// download happens inside aidoku-run's own "download" command instead, in
// the same process that writes the CBZ, so a failed or killed download can
// never leave this index out of sync with what's actually on disk the way
// a separate "download, then separately tell this binary about it" step
// could.
package main

import (
	"encoding/json"
	"fmt"
	"os"
	"strconv"

	"github.com/ohaiibuzzle/aidokurunner-go/downloads"
)

func main() {
	if len(os.Args) < 3 {
		usage()
		os.Exit(2)
	}
	dir := os.Args[1]
	command := os.Args[2]
	args := os.Args[3:]

	if err := run(dir, command, args); err != nil {
		fmt.Fprintln(os.Stderr, "error:", err)
		os.Exit(1)
	}
}

func usage() {
	fmt.Fprintln(os.Stderr, `usage: aidoku-downloads <downloads-dir> <command> [args...]

commands:
  path <source-path> <manga-key> <chapter-key>
                                     print {"path": "..."}, or {"path": ""} if not downloaded
  by-path <path>                    reverse lookup: print the entry for a local file, or {} if not indexed
  remove <source-path> <manga-key> <chapter-key>
                                     delete the index entry and its file (no-op if absent)
  list                               print every indexed download, most recent first
  total                              print {"totalBytes": N}
  prune <limit-bytes>                delete oldest downloads until under limit-bytes; print what was removed`)
}

func run(dir, command string, args []string) error {
	store, err := downloads.Open(dir)
	if err != nil {
		return fmt.Errorf("opening downloads index: %w", err)
	}
	defer store.Close()

	switch command {
	case "path":
		if len(args) < 3 {
			return fmt.Errorf("usage: path <source-path> <manga-key> <chapter-key>")
		}
		path, err := store.Path(args[0], args[1], args[2])
		if err != nil {
			return err
		}
		return printJSON(struct {
			Path string `json:"path"`
		}{path})

	case "by-path":
		if len(args) < 1 {
			return fmt.Errorf("usage: by-path <path>")
		}
		entry, err := store.ByPath(args[0])
		if err != nil {
			return err
		}
		if entry == nil {
			entry = &downloads.Entry{}
		}
		return printJSON(entry)

	case "remove":
		if len(args) < 3 {
			return fmt.Errorf("usage: remove <source-path> <manga-key> <chapter-key>")
		}
		return store.Remove(args[0], args[1], args[2])

	case "list":
		entries, err := store.List()
		if err != nil {
			return err
		}
		if entries == nil {
			entries = []downloads.Entry{}
		}
		return printJSON(entries)

	case "total":
		total, err := store.TotalBytes()
		if err != nil {
			return err
		}
		return printJSON(struct {
			TotalBytes int64 `json:"totalBytes"`
		}{total})

	case "prune":
		if len(args) < 1 {
			return fmt.Errorf("usage: prune <limit-bytes>")
		}
		limit, err := strconv.ParseInt(args[0], 10, 64)
		if err != nil {
			return fmt.Errorf("parsing limit-bytes %q: %w", args[0], err)
		}
		removed, err := store.Prune(limit)
		if err != nil {
			return err
		}
		if removed == nil {
			removed = []downloads.Entry{}
		}
		return printJSON(removed)

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
