package space

import (
	"encoding/json"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"testing"
)

// Guards against the usual translation mistakes: a typo in a key, a lost {placeholder}, a
// missing string. Dictionaries are plain JSON read by the shell at runtime, so nothing else
// would catch these before they show up on the tablet.

var placeholderRe = regexp.MustCompile(`\{[A-Za-z_]+\}`)

// requireComplete lists the languages whose dictionaries must contain every key of en.json.
// Other languages (zh-CN, inherited from upstream) are only checked for unknown keys and
// placeholders, because the shell falls back to English for missing keys anyway.
var requireComplete = map[string]bool{"fr": true, "de": true}

func readDict(t *testing.T, path string) map[string]string {
	t.Helper()
	b, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read %s: %v", path, err)
	}
	var d map[string]string
	if err := json.Unmarshal(b, &d); err != nil {
		t.Fatalf("parse %s: %v", path, err)
	}
	return d
}

func placeholders(s string) string {
	p := placeholderRe.FindAllString(s, -1)
	sort.Strings(p)
	return strings.Join(p, ",")
}

// checkDictDir compares every <lang>.json in dir with the en.json next to it.
func checkDictDir(t *testing.T, dir string) {
	t.Helper()
	en := readDict(t, filepath.Join(dir, "en.json"))
	files, _ := filepath.Glob(filepath.Join(dir, "*.json"))
	for _, f := range files {
		lang := strings.TrimSuffix(filepath.Base(f), ".json")
		if lang == "en" {
			continue
		}
		d := readDict(t, f)
		for k, v := range d {
			base := strings.TrimSuffix(k, "_one") // "<key>_one" is the singular form
			enVal, ok := en[base]
			if !ok {
				t.Errorf("%s: key %q does not exist in en.json", f, k)
				continue
			}
			if k == base && placeholders(v) != placeholders(enVal) {
				t.Errorf("%s: key %q has placeholders [%s], en.json has [%s]", f, k, placeholders(v), placeholders(enVal))
			}
		}
		if requireComplete[lang] {
			for k := range en {
				if _, ok := d[k]; !ok {
					t.Errorf("%s: missing key %q", f, k)
				}
			}
		}
	}
}

func TestShellDictionaries(t *testing.T) {
	dir := filepath.Join("..", "..", "shell", "i18n")
	checkDictDir(t, dir)
	for _, l := range SupportedLangs {
		if _, err := os.Stat(filepath.Join(dir, l+".json")); err != nil {
			t.Errorf("SupportedLangs has %q but %s is missing", l, filepath.Join(dir, l+".json"))
		}
	}
}

func TestAppDictionaries(t *testing.T) {
	dirs, _ := filepath.Glob(filepath.Join("..", "..", "apps", "*", "i18n"))
	if len(dirs) == 0 {
		t.Skip("no app dictionaries found (running outside the repo?)")
	}
	for _, d := range dirs {
		checkDictDir(t, d)
	}
}
