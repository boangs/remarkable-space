package space

// 底座的 OTA 钩子 (rmkit-cn-ota.timer → ota-watch.sh) 2026-10-29 改过: 定时器原来按单调时钟计时,
// 设备休眠时不走, 升级写完后一直浅睡就一次不检查, 重启进新系统空间就没了 (Move 升 3.30 实测)。
// 新版改墙钟定时 + 加 rmkit-cn-ota-reboot.service 在原厂切分区前补跑一次。
//
// 底座只随电脑上的助手走, 已经装好的机器拿不到。这里在空间后台启动后检查一次, 旧的就换成新的:
// 只碰这三样 (定时器、重启前补跑单元、ota-watch.sh), 都不在 xochitl 的启动链上, 写坏了最多是
// 升级后空间没起来, 不会挡开机。aarch64 的 /etc 是 tmpfs overlay, 单元文件要连底层一起写
// (同 reenable.sh 的 install_both), 否则重启就没了。
//
// otahook/ 里的三份文件必须和 base/ 里的原件一字不差, otahook_test.go 管着。

import (
	"bytes"
	_ "embed"
	"log"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"
)

var (
	//go:embed otahook/rmkit-cn-ota.timer
	otaTimer []byte
	//go:embed otahook/rmkit-cn-ota-reboot.service
	otaRebootUnit []byte
	//go:embed otahook/ota-watch.sh
	otaWatchScript []byte
)

const (
	rmkitDir    = "/home/root/rmkit-cn"
	unitDir     = "/etc/systemd/system"
	otaTimerN   = "rmkit-cn-ota.timer"
	otaRebootN  = "rmkit-cn-ota-reboot.service"
	rebootWants = "reboot.target.wants"
)

// EnsureOTAHook 后台启动 30 秒后检查一次 (不拖慢开机, 也不和刚起来的 xochitl 抢 IO)
func (h *Handler) EnsureOTAHook() {
	go func() {
		time.Sleep(30 * time.Second)
		if err := ensureOTAHook("/"); err != nil {
			log.Printf("[ota-hook] 升级失败, 下次启动再试: %v", err)
		}
	}()
}

// ensureOTAHook root 只给测试换根目录用, 设备上是 "/"
func ensureOTAHook(root string) error {
	p := func(s string) string { return filepath.Join(root, s) }
	// 没装底座 (或老到没有 OTA 钩子) 的机器不碰: 那是助手的事
	if _, err := os.Stat(p(filepath.Join(unitDir, otaTimerN))); err != nil {
		return nil
	}
	if _, err := os.Stat(p(filepath.Join(rmkitDir, "bin"))); err != nil {
		return nil
	}
	script := p(filepath.Join(rmkitDir, "bin/ota-watch.sh"))
	units := otaHookUnitsCurrent(p)
	scriptOK := fileIs(script, otaWatchScript)
	if units && scriptOK {
		return nil
	}
	if !scriptOK {
		tmp := script + ".new"
		if err := os.WriteFile(tmp, otaWatchScript, 0o755); err != nil {
			return err
		}
		if err := os.Rename(tmp, script); err != nil {
			return err
		}
	}
	if !units {
		dirs := []string{p("/etc")}
		if root == "/" && etcIsOverlay() {
			lower, done, err := mountLowerRoot()
			if err != nil {
				return err
			}
			defer done()
			dirs = append(dirs, filepath.Join(lower, "etc"))
		}
		for _, etc := range dirs {
			if err := writeOTAUnits(filepath.Join(etc, "systemd/system")); err != nil {
				return err
			}
		}
		exec.Command("sync").Run()
	}
	if root == "/" {
		exec.Command("systemctl", "daemon-reload").Run()
		exec.Command("systemctl", "restart", otaTimerN).Run()
	}
	log.Printf("[ota-hook] 已升级 OTA 钩子 (单元 %v, 脚本 %v)", !units, !scriptOK)
	return nil
}

func otaHookUnitsCurrent(p func(string) string) bool {
	d := p(unitDir)
	if !fileIs(filepath.Join(d, otaTimerN), otaTimer) || !fileIs(filepath.Join(d, otaRebootN), otaRebootUnit) {
		return false
	}
	_, err := os.Lstat(filepath.Join(d, rebootWants, otaRebootN))
	return err == nil
}

func writeOTAUnits(dir string) error {
	if err := os.MkdirAll(filepath.Join(dir, rebootWants), 0o755); err != nil {
		return err
	}
	for name, b := range map[string][]byte{otaTimerN: otaTimer, otaRebootN: otaRebootUnit} {
		if err := os.WriteFile(filepath.Join(dir, name), b, 0o644); err != nil {
			return err
		}
	}
	link := filepath.Join(dir, rebootWants, otaRebootN)
	_ = os.Remove(link)
	return os.Symlink(filepath.Join(unitDir, otaRebootN), link)
}

func fileIs(path string, want []byte) bool {
	b, err := os.ReadFile(path)
	return err == nil && bytes.Equal(b, want)
}

// etcIsOverlay Paper Pro / Move 的 /etc 是 overlay (上层在 tmpfs); rm2 的 /etc 就是根分区上的目录
func etcIsOverlay() bool {
	b, err := os.ReadFile("/proc/mounts")
	if err != nil {
		return false
	}
	for _, l := range strings.Split(string(b), "\n") {
		f := strings.Fields(l)
		if len(f) >= 3 && f[1] == "/etc" && f[2] == "overlay" {
			return true
		}
	}
	return false
}

// mountLowerRoot 把根分区再绑一份到临时目录并改成可写, 写进去的就是 overlay 的底层 (重启后还在)
func mountLowerRoot() (string, func(), error) {
	d, err := os.MkdirTemp("/home/root", ".space-rootfs-")
	if err != nil {
		return "", nil, err
	}
	if out, err := exec.Command("mount", "--bind", "/", d).CombinedOutput(); err != nil {
		os.Remove(d)
		return "", nil, errText(err, out)
	}
	done := func() {
		exec.Command("umount", "-l", d).Run()
		os.Remove(d)
	}
	if out, err := exec.Command("mount", "-o", "remount,rw", d).CombinedOutput(); err != nil {
		done()
		return "", nil, errText(err, out)
	}
	return d, done, nil
}

func errText(err error, out []byte) error {
	if s := strings.TrimSpace(string(out)); s != "" {
		return &cmdErr{err, s}
	}
	return err
}

type cmdErr struct {
	err error
	out string
}

func (e *cmdErr) Error() string { return e.err.Error() + ": " + e.out }
