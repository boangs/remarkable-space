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

var placeholderRe = regexp.MustCompile(`\{[A-Za-z_]+\}`)

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

func checkDictDir(t *testing.T, dir string) {
	t.Helper()
	en := readDict(t, filepath.Join(dir, "en.json"))
	for _, lang := range SupportedLangs {
		if lang == "en" {
			continue
		}
		path := filepath.Join(dir, lang+".json")
		d := readDict(t, path)
		for k, enVal := range en {
			v, ok := d[k]
			if !ok {
				t.Errorf("%s: missing key %q", path, k)
				continue
			}
			if placeholders(v) != placeholders(enVal) {
				t.Errorf("%s: key %q placeholders [%s], en.json has [%s]", path, k, placeholders(v), placeholders(enVal))
			}
		}
		for k := range d {
			base := strings.TrimSuffix(k, "_one")
			if _, ok := en[base]; !ok {
				t.Errorf("%s: key %q does not exist in en.json", path, k)
			}
		}
	}
}

func TestShellDictionaries(t *testing.T) {
	dir := filepath.Join("..", "..", "shell", "i18n")
	checkDictDir(t, dir)
}

func TestAppDictionaries(t *testing.T) {
	dirs, _ := filepath.Glob(filepath.Join("..", "..", "apps", "*", "i18n"))
	for _, d := range dirs {
		checkDictDir(t, d)
	}
}
