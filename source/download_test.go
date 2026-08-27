package source

import (
	"os"
	"path/filepath"
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
