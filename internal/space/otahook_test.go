package space

import (
	"bytes"
	"os"
	"path/filepath"
	"testing"
)

// 嵌进空间后台的三份文件必须和底座原件一致 (改了 base/ 记得 cp 到 internal/space/otahook/)
func TestOTAHookFilesMatchBase(t *testing.T) {
	for emb, src := range map[string]string{
		"otahook/rmkit-cn-ota.timer":          "../../base/systemd/rmkit-cn-ota.timer",
		"otahook/rmkit-cn-ota-reboot.service": "../../base/systemd/rmkit-cn-ota-reboot.service",
		"otahook/ota-watch.sh":                "../../base/installer/ota-watch.sh",
	} {
		a, err1 := os.ReadFile(emb)
		b, err2 := os.ReadFile(src)
		if err1 != nil || err2 != nil || !bytes.Equal(a, b) {
			t.Errorf("%s 和 %s 不一致 (%v %v)", emb, src, err1, err2)
		}
	}
}

// 老机器 (旧定时器、没有重启补跑) → 换成新的; 再跑一次什么都不动; 没装底座的不碰
func TestEnsureOTAHook(t *testing.T) {
	root := t.TempDir()
	ud := filepath.Join(root, unitDir)
	os.MkdirAll(ud, 0o755)
	os.MkdirAll(filepath.Join(root, rmkitDir, "bin"), 0o755)
	os.WriteFile(filepath.Join(ud, otaTimerN), []byte("[Timer]\nOnUnitActiveSec=5min\n"), 0o644)
	os.WriteFile(filepath.Join(root, rmkitDir, "bin/ota-watch.sh"), []byte("old"), 0o755)
	if err := ensureOTAHook(root); err != nil {
		t.Fatal(err)
	}
	p := func(s string) string { return filepath.Join(root, s) }
	if !otaHookUnitsCurrent(p) || !fileIs(p(filepath.Join(rmkitDir, "bin/ota-watch.sh")), otaWatchScript) {
		t.Fatal("没换成新的")
	}
	if l, _ := os.Readlink(filepath.Join(ud, rebootWants, otaRebootN)); l != filepath.Join(unitDir, otaRebootN) {
		t.Fatalf("wants 链接不对: %s", l)
	}
	st, _ := os.Stat(filepath.Join(ud, otaTimerN))
	if err := ensureOTAHook(root); err != nil {
		t.Fatal(err)
	}
	if st2, _ := os.Stat(filepath.Join(ud, otaTimerN)); !st2.ModTime().Equal(st.ModTime()) {
		t.Fatal("已经是新的还在重写")
	}
	empty := t.TempDir()
	if err := ensureOTAHook(empty); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(filepath.Join(empty, unitDir)); err == nil {
		t.Fatal("没装底座的机器也写了")
	}
}
