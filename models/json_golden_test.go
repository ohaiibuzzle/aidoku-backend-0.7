package models

import (
	"bytes"
	"encoding/json"
	"flag"
	"os"
	"path/filepath"
	"testing"
)

var updateGolden = flag.Bool("update", false, "rewrite models/testdata/*.golden.json")

// The KOReader plugin reads `aidoku-run settings`/`filters` output with
// default encoding/json field names (sourcesettingsbrowser.lua,
// filterbrowser.lua). Renaming a field or adding a MarshalJSON silently
// breaks it, so pin the shape. Regenerate with: go test ./models -update
func checkGolden(t *testing.T, name string, v any) {
	t.Helper()
	got, err := json.MarshalIndent(v, "", "  ")
	if err != nil {
		t.Fatal(err)
	}
	got = append(got, '\n')
	path := filepath.Join("testdata", name)
	if *updateGolden {
		if err := os.WriteFile(path, got, 0o644); err != nil {
			t.Fatal(err)
		}
		return
	}
	want, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("%v (run with -update to create)", err)
	}
	if !bytes.Equal(got, want) {
		t.Errorf("%s changed; the Lua plugin depends on this shape. If intended, update the Lua readers and rerun with -update.\ngot:\n%s", name, got)
	}
}

func TestSettingJSONGolden(t *testing.T) {
	sel := Setting{Key: "lang", Title: "Language", Type: SettingTypeSelect,
		Select: &SelectSetting{Values: []string{"en", "ja"}, Titles: []string{"English", "Japanese"}, DefaultValue: strp("en")}}
	multi := Setting{Key: "genres", Title: "Genres", Type: SettingTypeMultiselect,
		Multiselect: &MultiSelectSetting{Values: []string{"a", "b"}, Titles: []string{"A", "B"}, DefaultValue: []string{"a"}}}
	toggle := Setting{Key: "nsfw", Title: "Show NSFW", Type: SettingTypeToggle,
		Toggle: &ToggleSetting{Subtitle: strp("Adult content"), DefaultValue: true}}
	text := Setting{Key: "url", Title: "Base URL", Type: SettingTypeText,
		Text: &TextSetting{Placeholder: strp("https://"), DefaultValue: strp("https://example.com")}}
	settings := []Setting{
		{Key: "general", Title: "General", Type: SettingTypeGroup,
			Group: &GroupSetting{Footer: strp("Footer"), Items: []Setting{sel, multi}}},
		{Key: "advanced", Title: "Advanced", Type: SettingTypePage,
			Page: &PageSetting{Items: []Setting{toggle, text}}},
	}
	checkGolden(t, "settings.golden.json", settings)
}

func TestFilterJSONGolden(t *testing.T) {
	filters := []Filter{
		{ID: "sort", Title: strp("Sort"), Kind: FilterKindSort, CanAscend: true,
			SortOptions: []string{"Latest", "Popular"}, SortDefault: &SortDefault{Index: 1, Ascending: true}},
		{ID: "status", Title: strp("Status"), Kind: FilterKindSelect,
			Select: SelectFilter{Options: []string{"Any", "Ongoing"}, IDs: []string{"", "ongoing"}, DefaultValue: strp("")}},
		{ID: "genre", Title: strp("Genre"), Kind: FilterKindMultiselect,
			MultiSelect: MultiSelectFilter{IsGenre: true, CanExclude: true, Options: []string{"Action", "Comedy"},
				IDs: []string{"action", "comedy"}, DefaultIncluded: []string{"action"}}},
		{ID: "completed", Kind: FilterKindCheck, CheckName: strp("Completed only"), CheckCanExclude: true,
			CheckDefaultValue: boolp(false)},
	}
	checkGolden(t, "filters.golden.json", filters)
}
