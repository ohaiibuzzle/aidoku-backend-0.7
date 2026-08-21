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

// Entry is one indexed download. SourceKey (the source's stable manifest
// ID, e.g. "en.weebcentral") is the real identity -- see the package doc on
// why. SourcePath (the installed .aix's filesystem path at the time of the
// most recent Record) is kept alongside purely as a display/debugging hint;
// nothing should treat it as a stable identifier.
type Entry struct {
	SourceKey    string `json:"sourceKey"`
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
// <dir>/index.db, migrating it from the pre-source_key schema if needed --
// see migrateToSourceKey.
func Open(dir string) (*Store, error) {
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return nil, fmt.Errorf("downloads: creating %s: %w", dir, err)
	}
	dbPath := filepath.Join(dir, "index.db")
	db, err := sql.Open("sqlite", dbPath)
	if err != nil {
		return nil, fmt.Errorf("downloads: opening %s: %w", dbPath, err)
	}
	s := &Store{db: db}
	if err := s.migrateToSourceKey(); err != nil {
		db.Close()
		return nil, fmt.Errorf("downloads: migrating schema: %w", err)
	}
	if _, err := db.Exec(`
		CREATE TABLE IF NOT EXISTS downloads (
			source_key    TEXT NOT NULL,
			manga_key     TEXT NOT NULL,
			chapter_key   TEXT NOT NULL,
			source_path   TEXT NOT NULL,
			path          TEXT NOT NULL,
			manga_title   TEXT NOT NULL,
			chapter_title TEXT NOT NULL,
			size_bytes    INTEGER NOT NULL,
			downloaded_at INTEGER NOT NULL,
			PRIMARY KEY (source_key, manga_key, chapter_key)
		)
	`); err != nil {
		db.Close()
		return nil, fmt.Errorf("downloads: creating schema: %w", err)
	}
	return s, nil
}

// migrateToSourceKey upgrades a pre-existing database created before
// source_key existed (when source_path -- the installed file's path --
// was itself the primary key). SQLite can't add a column into a PRIMARY
// KEY or change one via ALTER TABLE, so this rebuilds the table: renaming
// the old one, creating the new schema, and copying every row across with
// source_key seeded from its old source_path. That doesn't retroactively
// fix any row whose source has already been renamed by an update installed
// before this migration ran (nothing on disk records what that source's
// real key was) -- but it does mean every row keeps resolving exactly as
// it did before the migration, and every row recorded from here on is
// keyed by the stable identity instead of a path that can change under it.
// A no-op if the table doesn't exist yet (fresh install) or already has a
// source_key column (already migrated).
func (s *Store) migrateToSourceKey() error {
	var exists int
	if err := s.db.QueryRow(`SELECT count(*) FROM sqlite_master WHERE type='table' AND name='downloads'`).Scan(&exists); err != nil {
		return err
	}
	if exists == 0 {
		return nil
	}
	rows, err := s.db.Query(`PRAGMA table_info(downloads)`)
	if err != nil {
		return err
	}
	hasSourceKey := false
	for rows.Next() {
		var cid int
		var name, colType string
		var notNull, pk int
		var dflt sql.NullString
		if err := rows.Scan(&cid, &name, &colType, &notNull, &dflt, &pk); err != nil {
			rows.Close()
			return err
		}
		if name == "source_key" {
			hasSourceKey = true
		}
	}
	if err := rows.Err(); err != nil {
		return err
	}
	rows.Close()
	if hasSourceKey {
		return nil
	}

	tx, err := s.db.Begin()
	if err != nil {
		return err
	}
	defer tx.Rollback()

	if _, err := tx.Exec(`ALTER TABLE downloads RENAME TO downloads_pre_source_key`); err != nil {
		return err
	}
	if _, err := tx.Exec(`
		CREATE TABLE downloads (
			source_key    TEXT NOT NULL,
			manga_key     TEXT NOT NULL,
			chapter_key   TEXT NOT NULL,
			source_path   TEXT NOT NULL,
			path          TEXT NOT NULL,
			manga_title   TEXT NOT NULL,
			chapter_title TEXT NOT NULL,
			size_bytes    INTEGER NOT NULL,
			downloaded_at INTEGER NOT NULL,
			PRIMARY KEY (source_key, manga_key, chapter_key)
		)
	`); err != nil {
		return err
	}
	if _, err := tx.Exec(`
		INSERT INTO downloads (source_key, manga_key, chapter_key, source_path, path, manga_title, chapter_title, size_bytes, downloaded_at)
		SELECT source_path, manga_key, chapter_key, source_path, path, manga_title, chapter_title, size_bytes, downloaded_at
		FROM downloads_pre_source_key
	`); err != nil {
		return err
	}
	if _, err := tx.Exec(`DROP TABLE downloads_pre_source_key`); err != nil {
		return err
	}
	return tx.Commit()
}

func (s *Store) Close() error { return s.db.Close() }

// Record inserts or replaces the index entry for a downloaded chapter,
// computing its size from the file at path. Replacing lets a re-download
// (same source/manga/chapter, e.g. after MangaBrowser's "Re-download"
// action) update the existing row rather than accumulate duplicates.
func (s *Store) Record(sourceKey, sourcePath, mangaKey, chapterKey, path, mangaTitle, chapterTitle string) (*Entry, error) {
	info, err := os.Stat(path)
	if err != nil {
		return nil, fmt.Errorf("downloads: stat %s: %w", path, err)
	}
	e := &Entry{
		SourceKey:    sourceKey,
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
		INSERT INTO downloads (source_key, manga_key, chapter_key, source_path, path, manga_title, chapter_title, size_bytes, downloaded_at)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?)
		ON CONFLICT (source_key, manga_key, chapter_key) DO UPDATE SET
			source_path=excluded.source_path, path=excluded.path, manga_title=excluded.manga_title, chapter_title=excluded.chapter_title,
			size_bytes=excluded.size_bytes, downloaded_at=excluded.downloaded_at
	`, e.SourceKey, e.MangaKey, e.ChapterKey, e.SourcePath, e.Path, e.MangaTitle, e.ChapterTitle, e.SizeBytes, e.DownloadedAt)
	if err != nil {
		return nil, fmt.Errorf("downloads: recording: %w", err)
	}
	return e, nil
}

// Path returns the local file path for a chapter, or "" if it hasn't been
// downloaded.
func (s *Store) Path(sourceKey, mangaKey, chapterKey string) (string, error) {
	var path string
	err := s.db.QueryRow(`SELECT path FROM downloads WHERE source_key=? AND manga_key=? AND chapter_key=?`,
		sourceKey, mangaKey, chapterKey).Scan(&path)
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
	err := s.db.QueryRow(`SELECT source_key, source_path, manga_key, chapter_key, path, manga_title, chapter_title, size_bytes, downloaded_at
		FROM downloads WHERE path=?`, path).
		Scan(&e.SourceKey, &e.SourcePath, &e.MangaKey, &e.ChapterKey, &e.Path, &e.MangaTitle, &e.ChapterTitle, &e.SizeBytes, &e.DownloadedAt)
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
func (s *Store) Remove(sourceKey, mangaKey, chapterKey string) error {
	path, err := s.Path(sourceKey, mangaKey, chapterKey)
	if err != nil {
		return err
	}
	if path == "" {
		return nil
	}
	if _, err := s.db.Exec(`DELETE FROM downloads WHERE source_key=? AND manga_key=? AND chapter_key=?`,
		sourceKey, mangaKey, chapterKey); err != nil {
		return fmt.Errorf("downloads: deleting index entry: %w", err)
	}
	if err := os.Remove(path); err != nil && !os.IsNotExist(err) {
		return fmt.Errorf("downloads: removing file %s: %w", path, err)
	}
	return nil
}

// List returns every downloaded chapter, most recently downloaded first.
func (s *Store) List() ([]Entry, error) {
	rows, err := s.db.Query(`SELECT source_key, source_path, manga_key, chapter_key, path, manga_title, chapter_title, size_bytes, downloaded_at
		FROM downloads ORDER BY downloaded_at DESC`)
	if err != nil {
		return nil, fmt.Errorf("downloads: listing: %w", err)
	}
	defer rows.Close()
	var entries []Entry
	for rows.Next() {
		var e Entry
		if err := rows.Scan(&e.SourceKey, &e.SourcePath, &e.MangaKey, &e.ChapterKey, &e.Path, &e.MangaTitle, &e.ChapterTitle, &e.SizeBytes, &e.DownloadedAt); err != nil {
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
		if err := s.Remove(e.SourceKey, e.MangaKey, e.ChapterKey); err != nil {
			return removed, err
		}
		total -= e.SizeBytes
		removed = append(removed, e)
	}
	return removed, nil
}

// Reassociate repoints every entry recorded under oldSourceKey to
// newSourceKey -- a manual repair primitive for entries orphaned by a
// source rename/update that this package couldn't resolve automatically
// (see migrateToSourceKey and the package doc). Returns how many rows were
// updated. If newSourceKey already has an entry for some (manga, chapter)
// also present under oldSourceKey, the oldSourceKey row is dropped instead
// of overwriting the existing one, since the row already under
// newSourceKey is presumably the more recently verified-working one.
func (s *Store) Reassociate(oldSourceKey, newSourceKey string) (int64, error) {
	if _, err := s.db.Exec(`
		DELETE FROM downloads
		WHERE source_key = ?
		AND EXISTS (
			SELECT 1 FROM downloads AS d2
			WHERE d2.source_key = ? AND d2.manga_key = downloads.manga_key AND d2.chapter_key = downloads.chapter_key
		)
	`, oldSourceKey, newSourceKey); err != nil {
		return 0, fmt.Errorf("downloads: dropping conflicting rows: %w", err)
	}
	res, err := s.db.Exec(`UPDATE downloads SET source_key = ? WHERE source_key = ?`, newSourceKey, oldSourceKey)
	if err != nil {
		return 0, fmt.Errorf("downloads: reassociating: %w", err)
	}
	return res.RowsAffected()
}
