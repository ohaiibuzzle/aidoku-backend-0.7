package downloads

import (
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
	if _, err := s.Record("src.aix", "manga1", "ch1", file, "Manga", "Chapter 1"); err != nil {
		t.Fatalf("Record: %v", err)
	}

	got, err := s.Path("src.aix", "manga1", "ch1")
	if err != nil {
		t.Fatalf("Path: %v", err)
	}
	if got != file {
		t.Errorf("Path = %q, want %q", got, file)
	}

	if got, err := s.Path("src.aix", "manga1", "missing"); err != nil || got != "" {
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
	if _, err := s.Record("src.aix", "manga1", "ch1", file, "Manga", "Chapter 1"); err != nil {
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
	if _, err := s.Record("src.aix", "manga1", "ch1", file1, "Manga", "Chapter 1"); err != nil {
		t.Fatalf("Record: %v", err)
	}
	file2 := writeFile(t, dir, "ch1-v2.cbz", 200)
	if _, err := s.Record("src.aix", "manga1", "ch1", file2, "Manga", "Chapter 1"); err != nil {
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
	if _, err := s.Record("src.aix", "manga1", "ch1", file, "Manga", "Chapter 1"); err != nil {
		t.Fatalf("Record: %v", err)
	}
	if err := s.Remove("src.aix", "manga1", "ch1"); err != nil {
		t.Fatalf("Remove: %v", err)
	}
	if _, err := os.Stat(file); !os.IsNotExist(err) {
		t.Errorf("file still exists after Remove: err=%v", err)
	}
	if got, err := s.Path("src.aix", "manga1", "ch1"); err != nil || got != "" {
		t.Errorf("Path after Remove = %q, %v, want \"\", nil", got, err)
	}

	// Removing an already-absent entry should be a no-op, not an error.
	if err := s.Remove("src.aix", "manga1", "ch1"); err != nil {
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
		if _, err := s.Record("src.aix", "manga1", key, file, "Manga", "Chapter"); err != nil {
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
