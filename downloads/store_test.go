package downloads

import (
	"database/sql"
	"os"
	"path/filepath"
	"testing"
)

func writeFile(t *testing.T, dir, name string, size int) string {
	t.Helper()
	path := filepath.Join(dir, name)
	if err := os.WriteFile(path, make([]byte, size), 0o644); err != nil {
		t.Fatalf("writing %s: %v", path, err)
	}
	return path
}

func TestRecordAndPath(t *testing.T) {
	dir := t.TempDir()
	s, err := Open(dir)
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	defer s.Close()

	file := writeFile(t, dir, "ch1.cbz", 1024)
	if _, err := s.Record("src.key", "src.aix", "manga1", "ch1", file, "Manga", "Chapter 1"); err != nil {
		t.Fatalf("Record: %v", err)
	}

	got, err := s.Path("src.key", "manga1", "ch1")
	if err != nil {
		t.Fatalf("Path: %v", err)
	}
	if got != file {
		t.Errorf("Path = %q, want %q", got, file)
	}

	if got, err := s.Path("src.key", "manga1", "missing"); err != nil || got != "" {
		t.Errorf("Path(missing) = %q, %v, want \"\", nil", got, err)
	}
}

func TestByPath(t *testing.T) {
	dir := t.TempDir()
	s, err := Open(dir)
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	defer s.Close()

	file := writeFile(t, dir, "ch1.cbz", 512)
	if _, err := s.Record("src.key", "src.aix", "manga1", "ch1", file, "Manga", "Chapter 1"); err != nil {
		t.Fatalf("Record: %v", err)
	}

	entry, err := s.ByPath(file)
	if err != nil {
		t.Fatalf("ByPath: %v", err)
	}
	if entry == nil || entry.ChapterKey != "ch1" || entry.SizeBytes != 512 {
		t.Errorf("ByPath = %+v", entry)
	}

	if entry, err := s.ByPath(filepath.Join(dir, "nope.cbz")); err != nil || entry != nil {
		t.Errorf("ByPath(nope) = %+v, %v, want nil, nil", entry, err)
	}
}

func TestRecordReplacesExisting(t *testing.T) {
	dir := t.TempDir()
	s, err := Open(dir)
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	defer s.Close()

	file1 := writeFile(t, dir, "ch1-v1.cbz", 100)
	if _, err := s.Record("src.key", "src.aix", "manga1", "ch1", file1, "Manga", "Chapter 1"); err != nil {
		t.Fatalf("Record: %v", err)
	}
	file2 := writeFile(t, dir, "ch1-v2.cbz", 200)
	if _, err := s.Record("src.key", "src.aix", "manga1", "ch1", file2, "Manga", "Chapter 1"); err != nil {
		t.Fatalf("Record (replace): %v", err)
	}

	entries, err := s.List()
	if err != nil {
		t.Fatalf("List: %v", err)
	}
	if len(entries) != 1 {
		t.Fatalf("List returned %d entries, want 1 (replace, not duplicate)", len(entries))
	}
	if entries[0].Path != file2 || entries[0].SizeBytes != 200 {
		t.Errorf("List[0] = %+v, want path %q size 200", entries[0], file2)
	}
}

func TestRemove(t *testing.T) {
	dir := t.TempDir()
	s, err := Open(dir)
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	defer s.Close()

	file := writeFile(t, dir, "ch1.cbz", 100)
	if _, err := s.Record("src.key", "src.aix", "manga1", "ch1", file, "Manga", "Chapter 1"); err != nil {
		t.Fatalf("Record: %v", err)
	}
	if err := s.Remove("src.key", "manga1", "ch1"); err != nil {
		t.Fatalf("Remove: %v", err)
	}
	if _, err := os.Stat(file); !os.IsNotExist(err) {
		t.Errorf("file still exists after Remove: err=%v", err)
	}
	if got, err := s.Path("src.key", "manga1", "ch1"); err != nil || got != "" {
		t.Errorf("Path after Remove = %q, %v, want \"\", nil", got, err)
	}

	// Removing an already-absent entry should be a no-op, not an error.
	if err := s.Remove("src.key", "manga1", "ch1"); err != nil {
		t.Errorf("Remove (already gone): %v", err)
	}
}

func TestTotalBytesAndPrune(t *testing.T) {
	dir := t.TempDir()
	s, err := Open(dir)
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	defer s.Close()

	if total, err := s.TotalBytes(); err != nil || total != 0 {
		t.Fatalf("TotalBytes (empty) = %d, %v, want 0, nil", total, err)
	}

	// Record three chapters of increasing size, each with an explicit,
	// distinct downloaded_at so Prune's oldest-first order is deterministic
	// rather than depending on real elapsed time between writes (Record's
	// own timestamp has 1-second resolution).
	sizes := []int{100, 200, 300}
	for i, size := range sizes {
		key := "ch" + string(rune('1'+i))
		file := writeFile(t, dir, key+".cbz", size)
		if _, err := s.Record("src.key", "src.aix", "manga1", key, file, "Manga", "Chapter"); err != nil {
			t.Fatalf("Record: %v", err)
		}
		if _, err := s.db.Exec(`UPDATE downloads SET downloaded_at = ? WHERE chapter_key = ?`, i, key); err != nil {
			t.Fatalf("setting downloaded_at for %s: %v", key, err)
		}
	}

	total, err := s.TotalBytes()
	if err != nil {
		t.Fatalf("TotalBytes: %v", err)
	}
	if total != 600 {
		t.Fatalf("TotalBytes = %d, want 600", total)
	}

	// limit=400 against sizes 100/200/300 (total 600): removing just the
	// oldest (ch1, 100) only gets to 500, still over limit, so pruning must
	// continue to ch2 (200) too, landing at 300.
	removed, err := s.Prune(400)
	if err != nil {
		t.Fatalf("Prune: %v", err)
	}
	if len(removed) != 2 || removed[0].ChapterKey != "ch1" || removed[1].ChapterKey != "ch2" {
		t.Fatalf("Prune removed %+v, want ch1 then ch2", removed)
	}
	total, err = s.TotalBytes()
	if err != nil {
		t.Fatalf("TotalBytes after prune: %v", err)
	}
	if total != 300 {
		t.Errorf("TotalBytes after prune = %d, want 300", total)
	}

	// limitBytes <= 0 means "no limit" (store.lua's unset default), so
	// nothing should be pruned even though we're well over any nonzero
	// value tested above.
	if removed, err := s.Prune(0); err != nil || removed != nil {
		t.Errorf("Prune(0) = %+v, %v, want nil, nil", removed, err)
	}
}

// TestMigrateToSourceKey simulates opening a database created by a version
// of this package before source_key existed (source_path was the primary
// key), and checks that Open transparently upgrades it in place: every
// existing row keeps resolving under its old source_path value (now
// standing in as its source_key too, since nothing on disk records what
// the real key was), and newly recorded rows use a real source_key from
// there on.
func TestMigrateToSourceKey(t *testing.T) {
	dir := t.TempDir()
	db, err := sql.Open("sqlite", filepath.Join(dir, "index.db"))
	if err != nil {
		t.Fatalf("opening raw db: %v", err)
	}
	if _, err := db.Exec(`
		CREATE TABLE downloads (
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
		t.Fatalf("seeding pre-migration schema: %v", err)
	}
	file := writeFile(t, dir, "ch1.cbz", 1024)
	if _, err := db.Exec(`INSERT INTO downloads VALUES (?, ?, ?, ?, ?, ?, ?, ?)`,
		"en.weebcentral-v7.aix", "manga1", "ch1", file, "Manga", "Chapter 1", 1024, 1000); err != nil {
		t.Fatalf("seeding pre-migration row: %v", err)
	}
	if err := db.Close(); err != nil {
		t.Fatalf("closing raw db: %v", err)
	}

	s, err := Open(dir)
	if err != nil {
		t.Fatalf("Open (migrating): %v", err)
	}
	defer s.Close()

	got, err := s.Path("en.weebcentral-v7.aix", "manga1", "ch1")
	if err != nil {
		t.Fatalf("Path: %v", err)
	}
	if got != file {
		t.Errorf("Path after migration = %q, want %q", got, file)
	}
	entries, err := s.List()
	if err != nil {
		t.Fatalf("List: %v", err)
	}
	if len(entries) != 1 || entries[0].SourceKey != "en.weebcentral-v7.aix" || entries[0].SourcePath != "en.weebcentral-v7.aix" {
		t.Errorf("List after migration = %+v", entries)
	}

	// A fresh record after migration uses the real, distinct source_key.
	file2 := writeFile(t, dir, "ch2.cbz", 512)
	if _, err := s.Record("en.weebcentral", "en.weebcentral-v8.aix", "manga1", "ch2", file2, "Manga", "Chapter 2"); err != nil {
		t.Fatalf("Record after migration: %v", err)
	}
	if got, err := s.Path("en.weebcentral", "manga1", "ch2"); err != nil || got != file2 {
		t.Errorf("Path(new key) = %q, %v, want %q, nil", got, err, file2)
	}

	// Opening an already-migrated database again must be a no-op, not
	// error out or re-run the migration.
	if err := s.Close(); err != nil {
		t.Fatalf("closing: %v", err)
	}
	s2, err := Open(dir)
	if err != nil {
		t.Fatalf("re-Open after migration: %v", err)
	}
	defer s2.Close()
	if entries, err := s2.List(); err != nil || len(entries) != 2 {
		t.Errorf("List after re-Open = %+v, %v, want 2 entries", entries, err)
	}
}

// TestReassociate covers the manual repair path: relinking every entry
// under an old source_key (e.g. a source's file got renamed by an update
// installed before this package's automatic handling existed) to the
// source's current key, including the case where the new key already has
// a conflicting entry for the same (manga, chapter).
func TestReassociate(t *testing.T) {
	dir := t.TempDir()
	s, err := Open(dir)
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	defer s.Close()

	file1 := writeFile(t, dir, "ch1.cbz", 100)
	if _, err := s.Record("old.key", "old.aix", "manga1", "ch1", file1, "Manga", "Chapter 1"); err != nil {
		t.Fatalf("Record ch1: %v", err)
	}
	// ch2 exists under both the old and new key -- the new key's version
	// should win, and the old one should just be dropped rather than
	// erroring out on the primary-key conflict.
	fileOld2 := writeFile(t, dir, "ch2-old.cbz", 50)
	if _, err := s.Record("old.key", "old.aix", "manga1", "ch2", fileOld2, "Manga", "Chapter 2"); err != nil {
		t.Fatalf("Record ch2 (old): %v", err)
	}
	fileNew2 := writeFile(t, dir, "ch2-new.cbz", 60)
	if _, err := s.Record("new.key", "new.aix", "manga1", "ch2", fileNew2, "Manga", "Chapter 2"); err != nil {
		t.Fatalf("Record ch2 (new): %v", err)
	}

	n, err := s.Reassociate("old.key", "new.key")
	if err != nil {
		t.Fatalf("Reassociate: %v", err)
	}
	if n != 1 {
		t.Errorf("Reassociate returned %d, want 1 (only ch1 should move)", n)
	}

	if got, err := s.Path("new.key", "manga1", "ch1"); err != nil || got != file1 {
		t.Errorf("Path(new.key, ch1) = %q, %v, want %q, nil", got, err, file1)
	}
	if got, err := s.Path("old.key", "manga1", "ch1"); err != nil || got != "" {
		t.Errorf("Path(old.key, ch1) = %q, %v, want \"\", nil", got, err)
	}
	if got, err := s.Path("new.key", "manga1", "ch2"); err != nil || got != fileNew2 {
		t.Errorf("Path(new.key, ch2) = %q, %v, want the new-key version %q", got, err, fileNew2)
	}
	if got, err := s.Path("old.key", "manga1", "ch2"); err != nil || got != "" {
		t.Errorf("Path(old.key, ch2) = %q, %v, want \"\" (dropped, not moved)", got, err)
	}
}
