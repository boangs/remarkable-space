package space

import "testing"

func TestDisplayName(t *testing.T) {
	// A built-in app of this fork: English default name plus per-language overrides.
	m := &Manifest{
		Name:  "Weather",
		Names: map[string]string{"fr": "Météo", "de": "Wetter", "zh-CN": "天气"},
	}
	cases := map[string]string{
		"fr":    "Météo",
		"de":    "Wetter",
		"zh-CN": "天气",
		"en":    "Weather", // no Names["en"], no NameEn -> Name
		"pt":    "Weather", // unknown language -> Name
	}
	for lang, want := range cases {
		if got := m.DisplayName(lang); got != want {
			t.Errorf("DisplayName(%q) = %q, want %q", lang, got, want)
		}
	}
}

func TestDisplayNameLegacyNameEn(t *testing.T) {
	// Apps from the upstream store: Chinese Name plus NameEn, no Names map.
	m := &Manifest{Name: "天气", NameEn: "Weather"}
	cases := map[string]string{"zh-CN": "天气", "en": "Weather", "fr": "Weather", "de": "Weather"}
	for lang, want := range cases {
		if got := m.DisplayName(lang); got != want {
			t.Errorf("DisplayName(%q) = %q, want %q", lang, got, want)
		}
	}
}

func TestDisplayNameWithoutTranslations(t *testing.T) {
	// Better to show the original name than an id or an empty string.
	m := &Manifest{Name: "天气"}
	for _, lang := range []string{"en", "fr", "de", "zh-CN"} {
		if got := m.DisplayName(lang); got != "天气" {
			t.Errorf("DisplayName(%q) = %q, want the original name", lang, got)
		}
	}
}
