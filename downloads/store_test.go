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
	if _, err := s.Record("src.key", "src.aix", "manga1", "ch1", file, "Manga", "Chapter 1", nil, nil); err != nil {
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

// TestRecordDisambiguatesCollidingFilename guards against two different
// chapters (different chapter keys) whose titles happen to sanitize to the
// identical filename leaving the index with two rows permanently pointing
// at the same one surviving file. It can't recover the first chapter's
// bytes -- the second download already overwrote them on disk before
// Record was ever called -- but it must stop compounding that: the second
// chapter needs its own distinct file, and the first's row must end up
// pointing somewhere with nothing there (degrading to "needs
// re-downloading", which Path already handles gracefully) rather than
// silently sharing the winner's content.
func TestRecordDisambiguatesCollidingFilename(t *testing.T) {
	dir := t.TempDir()
	s, err := Open(dir)
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	defer s.Close()

	// Both chapters' downloader picked the exact same output filename --
	// e.g. two chapters of the same manga both titled "Omake".
	collidingPath := filepath.Join(dir, "Omake.cbz")
	if err := os.WriteFile(collidingPath, make([]byte, 100), 0o644); err != nil {
		t.Fatalf("seeding first file: %v", err)
	}
	first, err := s.Record("src.key", "src.aix", "manga1", "ch1", collidingPath, "Manga", "Omake", nil, nil)
	if err != nil {
		t.Fatalf("Record ch1: %v", err)
	}
	if first.Path != collidingPath {
		t.Fatalf("first Record should not have been disambiguated: got path %q", first.Path)
	}

	// The second download physically overwrites the first's bytes at
	// collidingPath before Record is even called -- this is the real
	// sequence (the source package writes the CBZ, then aidoku-run's
	// download command calls Record), not something this test invents.
	if err := os.WriteFile(collidingPath, make([]byte, 200), 0o644); err != nil {
		t.Fatalf("seeding second file (overwriting the first on disk, as the real download would): %v", err)
	}
	second, err := s.Record("src.key", "src.aix", "manga1", "ch2", collidingPath, "Manga", "Omake", nil, nil)
	if err != nil {
		t.Fatalf("Record ch2: %v", err)
	}
	if second.Path == collidingPath {
		t.Fatalf("second Record should have been disambiguated to a different path, still got %q", second.Path)
	}
	if _, err := os.Stat(second.Path); err != nil {
		t.Fatalf("disambiguated file missing on disk: %v", err)
	}

	path1, err := s.Path("src.key", "manga1", "ch1")
	if err != nil {
		t.Fatalf("Path(ch1): %v", err)
	}
	path2, err := s.Path("src.key", "manga1", "ch2")
	if err != nil {
		t.Fatalf("Path(ch2): %v", err)
	}
	if path1 == path2 {
		t.Fatalf("ch1 and ch2 still point at the same path %q", path1)
	}
	// ch1's file is gone -- clobbered by ch2's download before Record ever
	// ran -- so its row now correctly looks like "not downloaded" rather
	// than silently resolving to ch2's content.
	if _, err := os.Stat(path1); !os.IsNotExist(err) {
		t.Errorf("expected ch1's path to have no file (stale row), stat err=%v", err)
	}
	if _, err := os.Stat(path2); err != nil {
		t.Errorf("ch2's recorded file missing: %v", err)
	}
}

// TestRecordSamePathTwiceForSameChapterDoesNotDisambiguate checks that
// re-recording the identical chapter at the same path (a retry, or an
// update to a previously downloaded chapter) is left alone -- only a
// collision against a genuinely different chapter should trigger a rename.
func TestRecordSamePathTwiceForSameChapterDoesNotDisambiguate(t *testing.T) {
	dir := t.TempDir()
	s, err := Open(dir)
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	defer s.Close()

	file := writeFile(t, dir, "ch1.cbz", 100)
	if _, err := s.Record("src.key", "src.aix", "manga1", "ch1", file, "Manga", "Chapter 1", nil, nil); err != nil {
		t.Fatalf("first Record: %v", err)
	}
	entry, err := s.Record("src.key", "src.aix", "manga1", "ch1", file, "Manga", "Chapter 1", nil, nil)
	if err != nil {
		t.Fatalf("second Record: %v", err)
	}
	if entry.Path != file {
		t.Fatalf("re-recording the same chapter got disambiguated: path = %q, want %q", entry.Path, file)
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
	if _, err := s.Record("src.key", "src.aix", "manga1", "ch1", file, "Manga", "Chapter 1", nil, nil); err != nil {
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
	if _, err := s.Record("src.key", "src.aix", "manga1", "ch1", file1, "Manga", "Chapter 1", nil, nil); err != nil {
		t.Fatalf("Record: %v", err)
	}
	file2 := writeFile(t, dir, "ch1-v2.cbz", 200)
	if _, err := s.Record("src.key", "src.aix", "manga1", "ch1", file2, "Manga", "Chapter 1", nil, nil); err != nil {
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
	if _, err := s.Record("src.key", "src.aix", "manga1", "ch1", file, "Manga", "Chapter 1", nil, nil); err != nil {
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
		if _, err := s.Record("src.key", "src.aix", "manga1", key, file, "Manga", "Chapter", nil, nil); err != nil {
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
	if _, err := s.Record("en.weebcentral", "en.weebcentral-v8.aix", "manga1", "ch2", file2, "Manga", "Chapter 2", nil, nil); err != nil {
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

// TestChapterOrdering covers ChapterNumber/VolumeNumber round-tripping
// through Record/List/ByPath, including the nil ("source didn't report
// one") case -- these are needed offline to reproduce reading order from
// the index alone, without a network chapter-list fetch.
func TestChapterOrdering(t *testing.T) {
	dir := t.TempDir()
	s, err := Open(dir)
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	defer s.Close()

	chNum := float32(4.5)
	volNum := float32(2)
	file1 := writeFile(t, dir, "ch1.cbz", 100)
	if _, err := s.Record("src.key", "src.aix", "manga1", "ch1", file1, "Manga", "Chapter 4.5", &chNum, &volNum); err != nil {
		t.Fatalf("Record: %v", err)
	}
	file2 := writeFile(t, dir, "ch2.cbz", 100)
	if _, err := s.Record("src.key", "src.aix", "manga1", "ch2", file2, "Manga", "Chapter ?", nil, nil); err != nil {
		t.Fatalf("Record (no chapter number): %v", err)
	}

	entry, err := s.ByPath(file1)
	if err != nil {
		t.Fatalf("ByPath: %v", err)
	}
	if entry == nil || entry.ChapterNumber == nil || *entry.ChapterNumber != chNum {
		t.Errorf("ByPath(file1).ChapterNumber = %+v, want %v", entry, chNum)
	}
	if entry.VolumeNumber == nil || *entry.VolumeNumber != volNum {
		t.Errorf("ByPath(file1).VolumeNumber = %+v, want %v", entry, volNum)
	}

	entries, err := s.List()
	if err != nil {
		t.Fatalf("List: %v", err)
	}
	var got1, got2 *Entry
	for i := range entries {
		switch entries[i].ChapterKey {
		case "ch1":
			got1 = &entries[i]
		case "ch2":
			got2 = &entries[i]
		}
	}
	if got1 == nil || got1.ChapterNumber == nil || *got1.ChapterNumber != chNum {
		t.Errorf("List ch1.ChapterNumber = %+v, want %v", got1, chNum)
	}
	if got2 == nil || got2.ChapterNumber != nil {
		t.Errorf("List ch2.ChapterNumber = %+v, want nil", got2)
	}
}

// TestMigrateAddChapterOrdering simulates opening a database created before
// chapter_number/volume_number existed (post-source_key, pre-ordering) and
// checks Open adds the columns in place without erroring or losing existing
// rows, and that re-opening an already-migrated database is a no-op.
func TestMigrateAddChapterOrdering(t *testing.T) {
	dir := t.TempDir()
	db, err := sql.Open("sqlite", filepath.Join(dir, "index.db"))
	if err != nil {
		t.Fatalf("opening raw db: %v", err)
	}
	if _, err := db.Exec(`
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
		t.Fatalf("seeding pre-ordering schema: %v", err)
	}
	file := writeFile(t, dir, "ch1.cbz", 1024)
	if _, err := db.Exec(`INSERT INTO downloads VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?)`,
		"src.key", "manga1", "ch1", "src.aix", file, "Manga", "Chapter 1", 1024, 1000); err != nil {
		t.Fatalf("seeding pre-ordering row: %v", err)
	}
	if err := db.Close(); err != nil {
		t.Fatalf("closing raw db: %v", err)
	}

	s, err := Open(dir)
	if err != nil {
		t.Fatalf("Open (migrating): %v", err)
	}
	defer s.Close()

	entries, err := s.List()
	if err != nil {
		t.Fatalf("List: %v", err)
	}
	if len(entries) != 1 || entries[0].ChapterKey != "ch1" || entries[0].ChapterNumber != nil {
		t.Errorf("List after migration = %+v, want 1 entry with nil ChapterNumber", entries)
	}

	// Opening an already-migrated database again must be a no-op.
	if err := s.Close(); err != nil {
		t.Fatalf("closing: %v", err)
	}
	s2, err := Open(dir)
	if err != nil {
		t.Fatalf("re-Open after migration: %v", err)
	}
	defer s2.Close()
	if entries, err := s2.List(); err != nil || len(entries) != 1 {
		t.Errorf("List after re-Open = %+v, %v, want 1 entry", entries, err)
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
	if _, err := s.Record("old.key", "old.aix", "manga1", "ch1", file1, "Manga", "Chapter 1", nil, nil); err != nil {
		t.Fatalf("Record ch1: %v", err)
	}
	// ch2 exists under both the old and new key -- the new key's version
	// should win, and the old one should just be dropped rather than
	// erroring out on the primary-key conflict.
	fileOld2 := writeFile(t, dir, "ch2-old.cbz", 50)
	if _, err := s.Record("old.key", "old.aix", "manga1", "ch2", fileOld2, "Manga", "Chapter 2", nil, nil); err != nil {
		t.Fatalf("Record ch2 (old): %v", err)
	}
	fileNew2 := writeFile(t, dir, "ch2-new.cbz", 60)
	if _, err := s.Record("new.key", "new.aix", "manga1", "ch2", fileNew2, "Manga", "Chapter 2", nil, nil); err != nil {
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
