package source

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/ohaiibuzzle/aidokurunner-go/models"
)

func TestResolveOutputPath_Flat(t *testing.T) {
	dir := t.TempDir()
	manga := models.Manga{Title: "Test Manga"}
	title := "Chapter 1"
	chapter := models.Chapter{Title: &title}

	got, err := resolveOutputPath(filepath.Join(dir, "Chapter 1.cbz"), "", manga, chapter)
	if err != nil {
		t.Fatalf("resolveOutputPath: %v", err)
	}
	want := filepath.Join(dir, "Chapter 1.cbz")
	if got != want {
		t.Errorf("got %q, want %q", got, want)
	}
}

func TestResolveOutputPath_MangaDirCreatedAndSanitized(t *testing.T) {
	dir := t.TempDir()
	manga := models.Manga{Title: "Test Manga"}
	title := "Chapter 1"
	chapter := models.Chapter{Title: &title}

	got, err := resolveOutputPath(filepath.Join(dir, "Chapter 1.cbz"), `Bad? Name: <Title> [src.key]`, manga, chapter)
	if err != nil {
		t.Fatalf("resolveOutputPath: %v", err)
	}
	wantDir := filepath.Join(dir, "Bad- Name- -Title- [src.key]")
	want := filepath.Join(wantDir, "Chapter 1.cbz")
	if got != want {
		t.Errorf("got %q, want %q", got, want)
	}
	if info, err := os.Stat(wantDir); err != nil || !info.IsDir() {
		t.Errorf("expected manga directory %q to have been created", wantDir)
	}
}

func TestResolveOutputPath_SanitizesChapterFilenameToo(t *testing.T) {
	dir := t.TempDir()
	manga := models.Manga{Title: "Test Manga"}
	title := "Chapter 1"
	chapter := models.Chapter{Title: &title}

	got, err := resolveOutputPath(filepath.Join(dir, `Chapter: 1?.cbz`), "Manga Folder", manga, chapter)
	if err != nil {
		t.Fatalf("resolveOutputPath: %v", err)
	}
	want := filepath.Join(dir, "Manga Folder", "Chapter- 1-.cbz")
	if got != want {
		t.Errorf("got %q, want %q", got, want)
	}
}

// TestResolveOutputPath_MangaDirNameTraversalRejected guards against a
// manga title of "." or ".." resolving outside the downloads directory:
// unlike the chapter filename (which always gets a ".cbz" suffix
// neutralizing that), mangaDirName is used as a bare path component with
// filepath.Join, so an unsanitized ".." there would escape the intended
// directory entirely.
func TestResolveOutputPath_MangaDirNameTraversalRejected(t *testing.T) {
	dir := t.TempDir()
	manga := models.Manga{Title: "Test Manga"}
	title := "Chapter 1"
	chapter := models.Chapter{Title: &title}

	got, err := resolveOutputPath(filepath.Join(dir, "downloads", "Chapter 1.cbz"), "..", manga, chapter)
	if err != nil {
		t.Fatalf("resolveOutputPath: %v", err)
	}
	if !strings.HasPrefix(got, filepath.Join(dir, "downloads")) {
		t.Errorf("resolved path %q escaped the downloads directory %q", got, filepath.Join(dir, "downloads"))
	}
}

func TestSanitizeFilenameRejectsDotAndDotDot(t *testing.T) {
	for _, in := range []string{".", ".."} {
		if got := sanitizeFilename(in); got == "." || got == ".." {
			t.Errorf("sanitizeFilename(%q) = %q, want something other than a bare . or ..", in, got)
		}
	}
}

func TestResolveOutputPath_Empty(t *testing.T) {
	manga := models.Manga{Title: "Test Manga"}
	title := "Chapter 1"
	chapter := models.Chapter{Title: &title}

	got, err := resolveOutputPath("", "ignored", manga, chapter)
	if err != nil {
		t.Fatalf("resolveOutputPath: %v", err)
	}
	if want := "Test Manga - Chapter 1.cbz"; got != want {
		t.Errorf("got %q, want %q", got, want)
	}
}
