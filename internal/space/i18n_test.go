package space

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
)

func TestLocalizedAppText(t *testing.T) {
	dir := t.TempDir()
	if err := os.Mkdir(filepath.Join(dir, "i18n"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "i18n", "zh-TW.json"), []byte(`{"_app_name":"黃曆","_launch_confirm":"確定重啟？"}`), 0o644); err != nil {
		t.Fatal(err)
	}
	a := App{Dir: dir, Manifest: Manifest{
		Name: "黄历", NameEn: "Almanac", Description: "一天一页", DescriptionEn: "One page a day",
		Launch: &Launch{Confirm: "确定重启？"},
	}}
	if got := localizedAppText(a, "zh-TW", "_app_name"); got != "黃曆" {
		t.Fatalf("traditional name = %q", got)
	}
	if got := localizedAppText(a, "de", "_app_name"); got != "Almanac" {
		t.Fatalf("fallback name = %q", got)
	}
	if got := localizedAppText(a, "de", "_app_description"); got != "One page a day" {
		t.Fatalf("fallback description = %q", got)
	}
	if got := localizedAppText(a, "zh-TW", "_launch_confirm"); got != "確定重啟？" {
		t.Fatalf("launch confirmation = %q", got)
	}
}

func TestLocalizeIndexUsesLanguageMaps(t *testing.T) {
	body := []byte(`{"apps":[{"name":"黄历","name_en":"Almanac","description":"一天一页","description_en":"One page a day","names":{"zh-TW":"黃曆","de":"Almanach"},"descriptions":{"de":"Ein Tag pro Seite"}}]}`)
	var idx map[string]any
	if err := json.Unmarshal(localizeIndex(body, "de"), &idx); err != nil {
		t.Fatal(err)
	}
	a := idx["apps"].([]any)[0].(map[string]any)
	if a["name"] != "Almanach" || a["description"] != "Ein Tag pro Seite" {
		t.Fatalf("unexpected German entry: %#v", a)
	}
	if err := json.Unmarshal(localizeIndex(body, "fr"), &idx); err != nil {
		t.Fatal(err)
	}
	a = idx["apps"].([]any)[0].(map[string]any)
	if a["name"] != "Almanac" || a["description"] != "One page a day" {
		t.Fatalf("unexpected English fallback: %#v", a)
	}
}
