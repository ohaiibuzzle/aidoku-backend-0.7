// Package downloads is the persistent index of locally downloaded
// chapters: which (source, manga, chapter) maps to which local CBZ file,
// its size, and when it was fetched -- backed by SQLite (modernc.org/sqlite,
// pure Go, no cgo, so it cross-compiles the same as the rest of this
// project) rather than a flat file, since callers (e.g. a KOReader plugin)
// query it on nearly every screen and re-parsing a growing JSON blob each
// time doesn't scale.
package downloads

import (
	"database/sql"
	"fmt"
	"os"
	"path/filepath"
	"time"

	_ "modernc.org/sqlite"
)

type Store struct {
	db *sql.DB
}

// Entry is one indexed download.
type Entry struct {
	SourcePath   string `json:"sourcePath"`
	MangaKey     string `json:"mangaKey"`
	ChapterKey   string `json:"chapterKey"`
	Path         string `json:"path"`
	MangaTitle   string `json:"mangaTitle"`
	ChapterTitle string `json:"chapterTitle"`
	SizeBytes    int64  `json:"sizeBytes"`
	DownloadedAt int64  `json:"downloadedAt"` // unix seconds
}

// Open opens (creating if needed) the downloads index database at
// <dir>/index.db.
func Open(dir string) (*Store, error) {
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return nil, fmt.Errorf("downloads: creating %s: %w", dir, err)
	}
	dbPath := filepath.Join(dir, "index.db")
	db, err := sql.Open("sqlite", dbPath)
	if err != nil {
		return nil, fmt.Errorf("downloads: opening %s: %w", dbPath, err)
	}
	if _, err := db.Exec(`
		CREATE TABLE IF NOT EXISTS downloads (
			source_path   TEXT NOT NULL,
			manga_key     TEXT NOT NULL,
			chapter_key   TEXT NOT NULL,
			path          TEXT NOT NULL,
			manga_title   TEXT NOT NULL,
			chapter_title TEXT NOT NULL,
			size_bytes    INTEGER NOT NULL,
			downloaded_at INTEGER NOT NULL,
			PRIMARY KEY (source_path, manga_key, chapter_key)
		)
	`); err != nil {
		db.Close()
		return nil, fmt.Errorf("downloads: creating schema: %w", err)
	}
	return &Store{db: db}, nil
}

func (s *Store) Close() error { return s.db.Close() }

// Record inserts or replaces the index entry for a downloaded chapter,
// computing its size from the file at path. Replacing lets a re-download
// (same source/manga/chapter, e.g. after MangaBrowser's "Re-download"
// action) update the existing row rather than accumulate duplicates.
func (s *Store) Record(sourcePath, mangaKey, chapterKey, path, mangaTitle, chapterTitle string) (*Entry, error) {
	info, err := os.Stat(path)
	if err != nil {
		return nil, fmt.Errorf("downloads: stat %s: %w", path, err)
	}
	e := &Entry{
		SourcePath:   sourcePath,
		MangaKey:     mangaKey,
		ChapterKey:   chapterKey,
		Path:         path,
		MangaTitle:   mangaTitle,
		ChapterTitle: chapterTitle,
		SizeBytes:    info.Size(),
		DownloadedAt: time.Now().Unix(),
	}
	_, err = s.db.Exec(`
		INSERT INTO downloads (source_path, manga_key, chapter_key, path, manga_title, chapter_title, size_bytes, downloaded_at)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?)
		ON CONFLICT (source_path, manga_key, chapter_key) DO UPDATE SET
			path=excluded.path, manga_title=excluded.manga_title, chapter_title=excluded.chapter_title,
			size_bytes=excluded.size_bytes, downloaded_at=excluded.downloaded_at
	`, e.SourcePath, e.MangaKey, e.ChapterKey, e.Path, e.MangaTitle, e.ChapterTitle, e.SizeBytes, e.DownloadedAt)
	if err != nil {
		return nil, fmt.Errorf("downloads: recording: %w", err)
	}
	return e, nil
}

// Path returns the local file path for a chapter, or "" if it hasn't been
// downloaded.
func (s *Store) Path(sourcePath, mangaKey, chapterKey string) (string, error) {
	var path string
	err := s.db.QueryRow(`SELECT path FROM downloads WHERE source_path=? AND manga_key=? AND chapter_key=?`,
		sourcePath, mangaKey, chapterKey).Scan(&path)
	if err == sql.ErrNoRows {
		return "", nil
	}
	if err != nil {
		return "", fmt.Errorf("downloads: querying: %w", err)
	}
	return path, nil
}

// ByPath is the reverse lookup: given a local file path (e.g. the document
// currently open in a reader), find which chapter it is. Returns nil if
// path isn't indexed.
func (s *Store) ByPath(path string) (*Entry, error) {
	e := &Entry{}
	err := s.db.QueryRow(`SELECT source_path, manga_key, chapter_key, path, manga_title, chapter_title, size_bytes, downloaded_at
		FROM downloads WHERE path=?`, path).
		Scan(&e.SourcePath, &e.MangaKey, &e.ChapterKey, &e.Path, &e.MangaTitle, &e.ChapterTitle, &e.SizeBytes, &e.DownloadedAt)
	if err == sql.ErrNoRows {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("downloads: querying: %w", err)
	}
	return e, nil
}

// Remove deletes both the index entry and its backing file. A no-op (not
// an error) if the entry doesn't exist.
func (s *Store) Remove(sourcePath, mangaKey, chapterKey string) error {
	path, err := s.Path(sourcePath, mangaKey, chapterKey)
	if err != nil {
		return err
	}
	if path == "" {
		return nil
	}
	if _, err := s.db.Exec(`DELETE FROM downloads WHERE source_path=? AND manga_key=? AND chapter_key=?`,
		sourcePath, mangaKey, chapterKey); err != nil {
		return fmt.Errorf("downloads: deleting index entry: %w", err)
	}
	if err := os.Remove(path); err != nil && !os.IsNotExist(err) {
		return fmt.Errorf("downloads: removing file %s: %w", path, err)
	}
	return nil
}

// List returns every downloaded chapter, most recently downloaded first.
func (s *Store) List() ([]Entry, error) {
	rows, err := s.db.Query(`SELECT source_path, manga_key, chapter_key, path, manga_title, chapter_title, size_bytes, downloaded_at
		FROM downloads ORDER BY downloaded_at DESC`)
	if err != nil {
		return nil, fmt.Errorf("downloads: listing: %w", err)
	}
	defer rows.Close()
	var entries []Entry
	for rows.Next() {
		var e Entry
		if err := rows.Scan(&e.SourcePath, &e.MangaKey, &e.ChapterKey, &e.Path, &e.MangaTitle, &e.ChapterTitle, &e.SizeBytes, &e.DownloadedAt); err != nil {
			return nil, fmt.Errorf("downloads: scanning: %w", err)
		}
		entries = append(entries, e)
	}
	return entries, rows.Err()
}

// TotalBytes returns the sum of size_bytes across every indexed download.
func (s *Store) TotalBytes() (int64, error) {
	var total sql.NullInt64
	if err := s.db.QueryRow(`SELECT SUM(size_bytes) FROM downloads`).Scan(&total); err != nil {
		return 0, fmt.Errorf("downloads: summing: %w", err)
	}
	return total.Int64, nil
}

// Prune deletes the oldest-downloaded chapters (index entry + file), one at
// a time, until the total is at or under limitBytes. Returns what was
// removed, oldest first. A limitBytes <= 0 is treated as "no limit" (a
// no-op), since 0 is store.lua's zero-value default meaning "unset".
func (s *Store) Prune(limitBytes int64) ([]Entry, error) {
	if limitBytes <= 0 {
		return nil, nil
	}
	total, err := s.TotalBytes()
	if err != nil {
		return nil, err
	}
	if total <= limitBytes {
		return nil, nil
	}
	entries, err := s.List() // most-recent-first
	if err != nil {
		return nil, err
	}
	var removed []Entry
	for i := len(entries) - 1; i >= 0 && total > limitBytes; i-- {
		e := entries[i]
		if err := s.Remove(e.SourcePath, e.MangaKey, e.ChapterKey); err != nil {
			return removed, err
		}
		total -= e.SizeBytes
		removed = append(removed, e)
	}
	return removed, nil
}
