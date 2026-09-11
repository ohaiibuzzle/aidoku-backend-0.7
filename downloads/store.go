// Package downloads is the persistent index of locally downloaded chapters:
// which (source, manga, chapter) maps to which local CBZ file, its size, and
// when it was fetched -- backed by SQLite (modernc.org/sqlite, pure Go, no
// cgo) since callers query it on nearly every screen.
//
// Record, Path, and AcquireLock (lock.go) are the only methods any Go binary
// here still calls (cmd/aidoku-run's "download" command). The KOReader
// plugin instead opens index.db directly via lua-ljsqlite3 (see
// downloadsengine.lua) and reimplements the rest of this API's queries in
// Lua -- the Go versions stay here, fully tested, as this package's public
// surface for any other embedder, not dead code.
package downloads

import (
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"fmt"
	"os"
	"path/filepath"
	"strings"
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
	SourceKey     string   `json:"sourceKey"`
	SourcePath    string   `json:"sourcePath"`
	MangaKey      string   `json:"mangaKey"`
	ChapterKey    string   `json:"chapterKey"`
	Path          string   `json:"path"`
	MangaTitle    string   `json:"mangaTitle"`
	ChapterTitle  string   `json:"chapterTitle"`
	SizeBytes     int64    `json:"sizeBytes"`
	DownloadedAt  int64    `json:"downloadedAt"` // unix seconds
	ChapterNumber *float32 `json:"chapterNumber"` // nil if the source didn't report one, same as models.Chapter
	VolumeNumber  *float32 `json:"volumeNumber"`  // nil if the source didn't report one, same as models.Chapter
}

// Open opens (creating if needed) the downloads index database at
// <dir>/index.db, migrating it from the pre-source_key schema if needed --
// see migrateToSourceKey.
func Open(dir string) (*Store, error) {
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return nil, fmt.Errorf("downloads: creating %s: %w", dir, err)
	}
	dbPath := filepath.Join(dir, "index.db")
	// _busy_timeout: the KOReader plugin now opens this same file directly
	// (via lua-ljsqlite3) for reads/deletes while this process holds it open
	// for a write -- without a busy timeout a concurrent writer/reader would
	// get SQLITE_BUSY immediately instead of retrying. No _journal_mode=WAL
	// here: some real target hardware (older Kindle kernels) can't safely
	// mmap for WAL, and this package has no way to detect that -- see
	// CLAUDE.md.
	db, err := sql.Open("sqlite", dbPath+"?_busy_timeout=5000")
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
			source_key     TEXT NOT NULL,
			manga_key      TEXT NOT NULL,
			chapter_key    TEXT NOT NULL,
			source_path    TEXT NOT NULL,
			path           TEXT NOT NULL,
			manga_title    TEXT NOT NULL,
			chapter_title  TEXT NOT NULL,
			size_bytes     INTEGER NOT NULL,
			downloaded_at  INTEGER NOT NULL,
			chapter_number REAL,
			volume_number  REAL,
			PRIMARY KEY (source_key, manga_key, chapter_key)
		)
	`); err != nil {
		db.Close()
		return nil, fmt.Errorf("downloads: creating schema: %w", err)
	}
	if err := s.migrateAddChapterOrdering(); err != nil {
		db.Close()
		return nil, fmt.Errorf("downloads: migrating schema: %w", err)
	}
	return s, nil
}

// migrateToSourceKey upgrades a database from before source_key existed
// (when source_path was itself the primary key). SQLite can't add/change a
// PRIMARY KEY column via ALTER TABLE, so this rebuilds the table, seeding
// source_key from the old source_path -- which can't retroactively fix a
// row whose source was renamed before this ran, but keeps every row
// resolving correctly and keys future rows by the stable identity. No-op if
// the table doesn't exist yet or already has a source_key column.
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

// migrateAddChapterOrdering adds the chapter_number/volume_number columns
// (needed to reproduce reading order from the index alone, offline) to a
// database created before they existed. Unlike migrateToSourceKey, these
// aren't part of the primary key, so a plain ALTER TABLE ADD COLUMN suffices
// -- no table rebuild. A no-op if the table doesn't exist yet (the preceding
// CREATE TABLE IF NOT EXISTS already included both columns) or already has
// them.
func (s *Store) migrateAddChapterOrdering() error {
	rows, err := s.db.Query(`PRAGMA table_info(downloads)`)
	if err != nil {
		return err
	}
	hasChapterNumber := false
	for rows.Next() {
		var cid int
		var name, colType string
		var notNull, pk int
		var dflt sql.NullString
		if err := rows.Scan(&cid, &name, &colType, &notNull, &dflt, &pk); err != nil {
			rows.Close()
			return err
		}
		if name == "chapter_number" {
			hasChapterNumber = true
		}
	}
	if err := rows.Err(); err != nil {
		return err
	}
	rows.Close()
	if hasChapterNumber {
		return nil
	}

	if _, err := s.db.Exec(`ALTER TABLE downloads ADD COLUMN chapter_number REAL`); err != nil {
		return err
	}
	if _, err := s.db.Exec(`ALTER TABLE downloads ADD COLUMN volume_number REAL`); err != nil {
		return err
	}
	return nil
}

func (s *Store) Close() error { return s.db.Close() }

// Record inserts or replaces the index entry for a downloaded chapter,
// computing its size from the file at path. Replacing lets a re-download
// (same source/manga/chapter, e.g. after MangaBrowser's "Re-download"
// action) update the existing row rather than accumulate duplicates.
func (s *Store) Record(sourceKey, sourcePath, mangaKey, chapterKey, path, mangaTitle, chapterTitle string, chapterNumber, volumeNumber *float32) (*Entry, error) {
	info, err := os.Stat(path)
	if err != nil {
		return nil, fmt.Errorf("downloads: stat %s: %w", path, err)
	}
	path, err = s.disambiguatePath(sourceKey, mangaKey, chapterKey, path)
	if err != nil {
		return nil, err
	}
	e := &Entry{
		SourceKey:     sourceKey,
		SourcePath:    sourcePath,
		MangaKey:      mangaKey,
		ChapterKey:    chapterKey,
		Path:          path,
		MangaTitle:    mangaTitle,
		ChapterTitle:  chapterTitle,
		SizeBytes:     info.Size(),
		DownloadedAt:  time.Now().Unix(),
		ChapterNumber: chapterNumber,
		VolumeNumber:  volumeNumber,
	}
	_, err = s.db.Exec(`
		INSERT INTO downloads (source_key, manga_key, chapter_key, source_path, path, manga_title, chapter_title, size_bytes, downloaded_at, chapter_number, volume_number)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)
		ON CONFLICT (source_key, manga_key, chapter_key) DO UPDATE SET
			source_path=excluded.source_path, path=excluded.path, manga_title=excluded.manga_title, chapter_title=excluded.chapter_title,
			size_bytes=excluded.size_bytes, downloaded_at=excluded.downloaded_at,
			chapter_number=excluded.chapter_number, volume_number=excluded.volume_number
	`, e.SourceKey, e.MangaKey, e.ChapterKey, e.SourcePath, e.Path, e.MangaTitle, e.ChapterTitle, e.SizeBytes, e.DownloadedAt, e.ChapterNumber, e.VolumeNumber)
	if err != nil {
		return nil, fmt.Errorf("downloads: recording: %w", err)
	}
	return e, nil
}

// disambiguatePath renames the just-downloaded file at path if some other
// (manga_key, chapter_key) under sourceKey already has an index row
// pointing at that exact path, and returns the (possibly new) path to
// record. Two different chapters whose titles happen to sanitize to the
// same filename (source.sanitizeFilename has no way to know about the
// other one -- it has no access to this index, by design, see
// source/download.go) would otherwise both end up recorded against one
// path.
//
// This can't recover the *first* chapter's bytes -- the second download
// already overwrote them on disk before Record was ever called, so by the
// time this runs there's only one real file left to rename. What it does
// prevent is the index staying wrong afterward: without this, both rows
// would permanently point at the one surviving file (deleting "chapter 1"
// would delete chapter 2's download too, and opening chapter 1 would
// silently show chapter 2's pages). With it, the second chapter gets its
// own distinct file and the first's row is left pointing at a path with
// nothing there anymore -- which existingDownload/Path already treat the
// same as "not downloaded yet" (see its own doc comment), so it just needs
// re-downloading rather than silently showing the wrong content. Caught
// here, the single place every download gets recorded (see CLAUDE.md's
// "Recording stays inside the writer"), rather than left to the source
// package or every caller of it to somehow avoid on their own.
func (s *Store) disambiguatePath(sourceKey, mangaKey, chapterKey, path string) (string, error) {
	var ownerManga, ownerChapter string
	err := s.db.QueryRow(`SELECT manga_key, chapter_key FROM downloads WHERE source_key = ? AND path = ?`, sourceKey, path).Scan(&ownerManga, &ownerChapter)
	if err == sql.ErrNoRows {
		return path, nil
	}
	if err != nil {
		return "", fmt.Errorf("downloads: checking for a path collision: %w", err)
	}
	if ownerManga == mangaKey && ownerChapter == chapterKey {
		return path, nil // re-recording the same chapter -- an expected overwrite, not a collision
	}

	// mangaKey/chapterKey can be arbitrary source-provided strings unsafe
	// to use in a filename directly (same reasoning as lockPath in
	// lock.go), so a short hash of them disambiguates without risking a
	// FAT32/exFAT-illegal character sneaking back in.
	ext := filepath.Ext(path)
	sum := sha256.Sum256([]byte(mangaKey + "\x00" + chapterKey))
	newPath := fmt.Sprintf("%s [%s]%s", strings.TrimSuffix(path, ext), hex.EncodeToString(sum[:])[:8], ext)
	if err := os.Rename(path, newPath); err != nil {
		return "", fmt.Errorf("downloads: disambiguating colliding filename: %w", err)
	}
	return newPath, nil
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

// nullFloat32 converts a nullable REAL column scan into the *float32
// pointer-means-absent representation Entry uses (matching models.Chapter).
func nullFloat32(n sql.NullFloat64) *float32 {
	if !n.Valid {
		return nil
	}
	v := float32(n.Float64)
	return &v
}

// ByPath is the reverse lookup: given a local file path (e.g. the document
// currently open in a reader), find which chapter it is. Returns nil if
// path isn't indexed.
func (s *Store) ByPath(path string) (*Entry, error) {
	e := &Entry{}
	var chapterNumber, volumeNumber sql.NullFloat64
	err := s.db.QueryRow(`SELECT source_key, source_path, manga_key, chapter_key, path, manga_title, chapter_title, size_bytes, downloaded_at, chapter_number, volume_number
		FROM downloads WHERE path=?`, path).
		Scan(&e.SourceKey, &e.SourcePath, &e.MangaKey, &e.ChapterKey, &e.Path, &e.MangaTitle, &e.ChapterTitle, &e.SizeBytes, &e.DownloadedAt, &chapterNumber, &volumeNumber)
	if err == sql.ErrNoRows {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("downloads: querying: %w", err)
	}
	e.ChapterNumber = nullFloat32(chapterNumber)
	e.VolumeNumber = nullFloat32(volumeNumber)
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
	rows, err := s.db.Query(`SELECT source_key, source_path, manga_key, chapter_key, path, manga_title, chapter_title, size_bytes, downloaded_at, chapter_number, volume_number
		FROM downloads ORDER BY downloaded_at DESC`)
	if err != nil {
		return nil, fmt.Errorf("downloads: listing: %w", err)
	}
	defer rows.Close()
	var entries []Entry
	for rows.Next() {
		var e Entry
		var chapterNumber, volumeNumber sql.NullFloat64
		if err := rows.Scan(&e.SourceKey, &e.SourcePath, &e.MangaKey, &e.ChapterKey, &e.Path, &e.MangaTitle, &e.ChapterTitle, &e.SizeBytes, &e.DownloadedAt, &chapterNumber, &volumeNumber); err != nil {
			return nil, fmt.Errorf("downloads: scanning: %w", err)
		}
		e.ChapterNumber = nullFloat32(chapterNumber)
		e.VolumeNumber = nullFloat32(volumeNumber)
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
	tx, err := s.db.Begin()
	if err != nil {
		return 0, fmt.Errorf("downloads: beginning reassociate transaction: %w", err)
	}
	defer tx.Rollback() //nolint:errcheck // no-op once committed

	if _, err := tx.Exec(`
		DELETE FROM downloads
		WHERE source_key = ?
		AND EXISTS (
			SELECT 1 FROM downloads AS d2
			WHERE d2.source_key = ? AND d2.manga_key = downloads.manga_key AND d2.chapter_key = downloads.chapter_key
		)
	`, oldSourceKey, newSourceKey); err != nil {
		return 0, fmt.Errorf("downloads: dropping conflicting rows: %w", err)
	}
	res, err := tx.Exec(`UPDATE downloads SET source_key = ? WHERE source_key = ?`, newSourceKey, oldSourceKey)
	if err != nil {
		return 0, fmt.Errorf("downloads: reassociating: %w", err)
	}
	n, err := res.RowsAffected()
	if err != nil {
		return 0, fmt.Errorf("downloads: reading rows affected: %w", err)
	}
	if err := tx.Commit(); err != nil {
		return 0, fmt.Errorf("downloads: committing reassociate transaction: %w", err)
	}
	return n, nil
}
