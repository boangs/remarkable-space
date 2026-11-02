package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestAdditionalSystemLanguages(t *testing.T) {
	root := repoRoot(t)
	body, err := os.ReadFile(filepath.Join(root, "qmd-src", "language_zh_cn.qmd"))
	if err != nil {
		t.Fatal(err)
	}
	for _, code := range []string{"zh_CN", "zh_TW", "ja", "ko"} {
		if !strings.Contains(string(body), `"`+code+`"`) {
			t.Errorf("system language selector is missing %s", code)
		}
	}
}
