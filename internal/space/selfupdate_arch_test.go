package space

import (
	"os"
	"path/filepath"
	"testing"
)

// 自检: 只给别的架构的内置应用 (Android 只有 aarch64) 不能让 armv7 (rm2) 自更新失败; 真坏的清单仍要拦
func TestSelfTestSkipsOtherArch(t *testing.T) {
	dir := t.TempDir()
	write := func(p, s string) {
		if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(p, []byte(s), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	write(filepath.Join(dir, "shell", "Space.qml"), "import QtQuick\nItem {}\n")
	write(filepath.Join(dir, "apps", "android", ManifestName),
		`{"id":"android","name":"Android","version":"1.0.0","arch":["aarch64"],"launch":{"type":"post","path":"/apps/android/launch"}}`)
	if err := SelfTest(dir, "armv7"); err != nil {
		t.Fatalf("armv7 不该因为 aarch64 专属内置应用失败: %v", err)
	}
	if err := SelfTest(dir, "aarch64"); err != nil {
		t.Fatalf("aarch64: %v", err)
	}
	write(filepath.Join(dir, "apps", "broken", ManifestName), `{"id":"other"}`)
	if err := SelfTest(dir, "armv7"); err == nil {
		t.Fatal("坏清单应当自检失败")
	}
}
