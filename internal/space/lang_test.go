package space

import (
	"os"
	"path/filepath"
	"testing"
)

func TestNormalizeLang(t *testing.T) {
	cases := map[string]string{
		"zh_CN":       "zh-CN",
		"zh-CN":       "zh-CN",
		"zh":          "zh-CN",
		"zh_CN.UTF-8": "zh-CN",
		"zh-Hans":     "zh-CN",
		"zh_TW":       "zh-CN", // 繁体暂无词典, 给简体比掉到英文贴近
		"en":          "en",
		"en_US.UTF-8": "en",
		"en-GB":       "en",
		"":            "en",
		"de_DE":       "de",
		"de_CH.UTF-8": "de",
		"de-AT":       "de",
		"fr_FR":       "fr",
		"fr-CA":       "fr",
		"fr":          "fr",
		"pt_BR":       "en", // no Portuguese dictionary yet: falls back to English
		"gibberish":   "en",
	}
	for in, want := range cases {
		if got := NormalizeLang(in); got != want {
			t.Errorf("NormalizeLang(%q) = %q, 应为 %q", in, got, want)
		}
	}
}

// 写一个 xochitl.conf 样本。注意真机上 [General] 是头一段, 后面还有 [Dialogs] 等,
// 同名键不该被读串。
func writeConf(t *testing.T, body string) string {
	t.Helper()
	p := filepath.Join(t.TempDir(), "xochitl.conf")
	if err := os.WriteFile(p, []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
	return p
}

// clearLangEnv makes a test independent of the developer's own locale.
//
// ResolveLang falls back to $LC_ALL, $LC_MESSAGES and $LANG when neither Space's settings nor
// xochitl.conf name a language. Before French was supported, a machine set to fr_FR passed
// these tests by accident (fr normalised to "en"). The environment is a hidden input of the
// function, so every test that depends on "no language configured" must empty it first.
func clearLangEnv(t *testing.T) {
	t.Helper()
	for _, k := range []string{"LC_ALL", "LC_MESSAGES", "LANG"} {
		t.Setenv(k, "") // restored automatically when the test ends
	}
}

func TestResolveLang(t *testing.T) {
	clearLangEnv(t)
	// 用户在系统设置里选过中文
	withLang := writeConf(t, "[General]\nKeyboard=en_US\nLanguage=zh_CN\n\n[Dialogs]\nLanguage=en\n")
	if got := ResolveLang("", withLang); got != "zh-CN" {
		t.Errorf("有 Language 键时 = %q, 应为 zh-CN", got)
	}

	// 真机上的情况: 从没切过语言, 只有 Keyboard
	onlyKbd := writeConf(t, "[General]\nKeyboard=zh_CN\nAutoUpdate=true\n")
	if got := ResolveLang("", onlyKbd); got != "zh-CN" {
		t.Errorf("只有 Keyboard 时 = %q, 应为 zh-CN (装了中文输入法多半要中文界面)", got)
	}

	// 空间自己的设置压过系统
	if got := ResolveLang("en", onlyKbd); got != "en" {
		t.Errorf("空间设置应压过系统, 得到 %q", got)
	}
	// "auto" 等同于没设置
	if got := ResolveLang("auto", onlyKbd); got != "zh-CN" {
		t.Errorf("auto 应表示跟随系统, 得到 %q", got)
	}

	// 配置文件不存在也不能炸
	if got := ResolveLang("", filepath.Join(t.TempDir(), "nope.conf")); got != "en" {
		t.Errorf("无配置文件时 = %q, 应为 en", got)
	}
}

// [General] 之后的段里有同名键时, 不能读串。
func TestResolveLangStopsAtNextSection(t *testing.T) {
	clearLangEnv(t)
	p := writeConf(t, "[General]\nAutoUpdate=true\n\n[Other]\nLanguage=zh_CN\n")
	if got := ResolveLang("", p); got != "en" {
		t.Errorf("[Other] 段里的 Language 不该被读到, 得到 %q", got)
	}
}

// The environment fallback itself: LC_ALL beats LC_MESSAGES beats LANG (POSIX precedence).
func TestResolveLangFromEnvironment(t *testing.T) {
	clearLangEnv(t)
	noConf := filepath.Join(t.TempDir(), "nope.conf")

	t.Setenv("LANG", "de_DE.UTF-8")
	if got := ResolveLang("", noConf); got != "de" {
		t.Errorf("LANG=de_DE.UTF-8 gave %q, want de", got)
	}
	t.Setenv("LC_MESSAGES", "fr_FR.UTF-8")
	if got := ResolveLang("", noConf); got != "fr" {
		t.Errorf("LC_MESSAGES should beat LANG, got %q, want fr", got)
	}
	t.Setenv("LC_ALL", "C") // "C" means "no language": skipped, falls through to the next variable
	if got := ResolveLang("", noConf); got != "fr" {
		t.Errorf("LC_ALL=C should be ignored, got %q, want fr", got)
	}
}

func TestLangFromMaps(t *testing.T) {
	dir := t.TempDir()
	cases := map[string]string{
		"ffff96406000-ffff96420000 r--p 00000000 b3:03 9679   /usr/share/remarkable/xochitl/translations/reMarkable_zh_CN.qm\n":           "zh-CN",
		"ffff9c592000-ffff9c5a1000 r--p 00000000 b3:03 7430   /usr/share/remarkable/xochitl/translations/reMarkable_en.qm\n":              "en",
		"ffffa588b000-ffffa588c000 r--p 00000000 fe:02 803903 /home/root/xovi/exthome/qt-resource-rebuilder/zh_CN.rcc\n":                  "",
		"ffff96406000-ffff96420000 r--p 00000000 b3:03 9679   /usr/share/remarkable/xochitl/translations/reMarkable_zh_CN.qm (deleted)\n": "zh-CN", // 文件被换过, 旧映射还在
	}
	for maps, want := range cases {
		p := filepath.Join(dir, "maps")
		if err := os.WriteFile(p, []byte("00400000-00401000 r-xp 00000000 b3:03 1 /usr/bin/xochitl\n"+maps), 0o644); err != nil {
			t.Fatal(err)
		}
		if got := langFromMaps(p); got != want {
			t.Errorf("langFromMaps(%q) = %q, want %q", maps, got, want)
		}
	}
	if got := langFromMaps(filepath.Join(dir, "nope")); got != "" {
		t.Errorf("missing file = %q", got)
	}
}
