package source

import (
	"os"
	"strings"

	"github.com/ohaiibuzzle/aidokurunner-go/models"
)

// extraSettings mirrors Source.getExtraSettings: synthesizes a language
// picker (when a source supports more than one language) and a base-URL
// picker (when the manifest opts in and multiple URLs are known).
//
// Swift's version uses iOS's Locale APIs (Locale.preferredLanguages,
// localizedString(forIdentifier:)) for the default-selected language(s)
// and human-readable titles, which have no equivalent on a headless
// Kindle/Linux target. This falls back to $LANG for a single guessed
// preferred language, and to the raw language codes as display titles.
func extraSettings(config *models.SourceConfiguration, languages, urls []string) []models.Setting {
	var extra []models.Setting

	if len(languages) > 1 {
		selectType := models.LanguageSelectTypeMultiple
		if config != nil && config.LanguageSelectType != nil {
			selectType = *config.LanguageSelectType
		}
		titles := append([]string{}, languages...)
		defaultLangs := preferredLanguages(languages)

		var inner models.Setting
		if selectType == models.LanguageSelectTypeSingle {
			key := "language"
			var def *string
			if len(defaultLangs) > 0 {
				def = &defaultLangs[0]
			}
			inner = models.Setting{
				Key: key, Title: "LANGUAGE", Notification: &key, Refreshes: []string{"content"},
				Type:   models.SettingTypeSelect,
				Select: &models.SelectSetting{Values: languages, Titles: titles, DefaultValue: def},
			}
		} else {
			key := "languages"
			inner = models.Setting{
				Key: key, Title: "LANGUAGES", Notification: &key, Refreshes: []string{"content"},
				Type:        models.SettingTypeMultiselect,
				Multiselect: &models.MultiSelectSetting{Values: languages, Titles: titles, DefaultValue: defaultLangs},
			}
		}
		extra = append(extra, models.Setting{
			Title: inner.Title,
			Type:  models.SettingTypeGroup,
			Group: &models.GroupSetting{Items: []models.Setting{inner}},
		})
	}

	if config != nil && config.AllowsBaseURLSelect != nil && *config.AllowsBaseURLSelect && len(urls) > 1 {
		var def *string
		if len(urls) > 0 {
			def = &urls[0]
		}
		inner := models.Setting{
			Key: "url", Title: "BASE_URL", Refreshes: []string{"content"},
			Type:   models.SettingTypeSelect,
			Select: &models.SelectSetting{Values: urls, DefaultValue: def},
		}
		extra = append(extra, models.Setting{
			Title: inner.Title,
			Type:  models.SettingTypeGroup,
			Group: &models.GroupSetting{Items: []models.Setting{inner}},
		})
	}

	return extra
}

// preferredLanguages mirrors Swift's
// `Set(languages).intersection(Set(preferredLanguages))`: always a non-nil
// slice (possibly empty), never nil. That distinction matters downstream —
// loadSettingsDefaults only registers a default when DefaultValue is
// non-nil, and Swift's Setting.Value.multiselect always carries a
// defaultValue (even Array(), i.e. `[]`) for the synthesized language
// picker. A nil slice here would make that registration silently skip,
// leaving `defaults.get("languages")` with nothing to return — which is
// exactly what multi-language sources like MangaDex hit for get_home in a
// headless/no-locale-match environment ("Unable to fetch languages").
func preferredLanguages(supported []string) []string {
	langs := []string{}
	lang := os.Getenv("LANG")
	if lang == "" {
		return langs
	}
	code := lang
	if idx := strings.IndexAny(lang, "_."); idx >= 0 {
		code = lang[:idx]
	}
	for _, l := range supported {
		if l == code {
			langs = append(langs, l)
		}
	}
	return langs
}

// loadSettingsDefaults mirrors Source.loadSettingsDefaults, registering
// each setting's default value under "<sourceKey>.<settingKey>" — the same
// namespacing the `defaults` host namespace uses.
func (s *Source) loadSettingsDefaults(settings []models.Setting) {
	if s.settings == nil {
		return
	}
	for _, setting := range settings {
		key := s.Key + "." + setting.Key
		switch setting.Type {
		case models.SettingTypeSelect:
			if setting.Select == nil {
				continue
			}
			if setting.Select.DefaultValue != nil {
				s.settings.RegisterDefault(key, *setting.Select.DefaultValue)
			} else if len(setting.Select.Values) > 0 {
				s.settings.RegisterDefault(key, setting.Select.Values[0])
			}
		case models.SettingTypeMultiselect:
			if setting.Multiselect != nil && setting.Multiselect.DefaultValue != nil {
				s.settings.RegisterDefault(key, setting.Multiselect.DefaultValue)
			}
		case models.SettingTypeToggle:
			if setting.Toggle != nil {
				s.settings.RegisterDefault(key, setting.Toggle.DefaultValue)
			}
		case models.SettingTypeStepper:
			if setting.Stepper != nil && setting.Stepper.DefaultValue != nil {
				s.settings.RegisterDefault(key, *setting.Stepper.DefaultValue)
			}
		case models.SettingTypeSegment:
			if setting.Segment != nil && setting.Segment.DefaultValue != nil {
				s.settings.RegisterDefault(key, *setting.Segment.DefaultValue)
			}
		case models.SettingTypeText:
			if setting.Text != nil && setting.Text.DefaultValue != nil {
				s.settings.RegisterDefault(key, *setting.Text.DefaultValue)
			}
		case models.SettingTypeEditableList:
			if setting.EditableList != nil && setting.EditableList.DefaultValue != nil {
				s.settings.RegisterDefault(key, setting.EditableList.DefaultValue)
			}
		case models.SettingTypeGroup:
			if setting.Group != nil {
				s.loadSettingsDefaults(setting.Group.Items)
			}
		case models.SettingTypePage:
			if setting.Page != nil {
				s.loadSettingsDefaults(setting.Page.Items)
			}
		case models.SettingTypePicker:
			if setting.Picker == nil {
				continue
			}
			if setting.Picker.DefaultValue != nil {
				s.settings.RegisterDefault(key, *setting.Picker.DefaultValue)
			} else if len(setting.Picker.Values) > 0 {
				s.settings.RegisterDefault(key, setting.Picker.Values[0])
			}
		}
	}
}
