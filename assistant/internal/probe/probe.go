// Package probe 只读地探测 reMarkable 的型号/固件/槽位/空间/已装组件。
// 它刻意只读系统信息文件, 不进入 /home/root/.local/share/remarkable (用户文档)。
package probe

import (
	"context"
	"strconv"
	"strings"

	"github.com/boangs/remarkable-space/assistant/internal/i18n"
	"github.com/boangs/remarkable-space/assistant/internal/sshx"
)

// Info 是一次探测的结果, 直接给前端展示与预检使用。
type Info struct {
	Arch       string `json:"arch"`       // aarch64 / armv7l
	FWVersion  string `json:"fwVersion"`  // /etc/version, 如 20260806095513
	IMGVersion string `json:"imgVersion"` // /etc/os-release 的 IMG_VERSION, 如 3.29.0.153
	Resolution string `json:"resolution"` // fb0 virtual_size
	Model      string `json:"model"`      // 人类可读
	ModelKey   string `json:"modelKey"`   // rm2 / rmpp / rmppm / unknown

	ActiveSlot string `json:"activeSlot"` // /dev/mmcblk0p2 等 (rm2 没有 → 空)
	NextBoot   string `json:"nextBoot"`
	ErrcntA    int    `json:"errcntA"`
	ErrcntB    int    `json:"errcntB"`
	Secboot    string `json:"secboot"` // unlocked / locked / 空 (非 i.MX93)
	// HasLSE: CPU 支持 ARMv8.1 的 LSE 原子指令 (cpuinfo Features 里的 atomics)。
	// Paper Pro (ferrari, Cortex-A53) 没有; Paper Pro Move (chiappa, A55) 有。
	// 用 ARMv8.1+ 编出来的二进制在没有它的机器上执行即 SIGILL。
	HasLSE bool `json:"hasLSE"`

	RootFreeMB int `json:"rootFreeMB"`
	HomeFreeMB int `json:"homeFreeMB"`

	HaveXovi       bool   `json:"haveXovi"`
	RmkitInstalled bool   `json:"rmkitInstalled"` // 底座 (注入链/汉化/输入法后端)
	SpaceInstalled bool   `json:"spaceInstalled"` // 空间外壳就位且侧栏入口已切过去
	SpaceActive    bool   `json:"spaceActive"`    // 空间后端 space-server 在跑
	RmkitFW        string `json:"rmkitFW"`        // rmkit-cn 上次安装时记录的固件版本
	SpaceVersion   string `json:"spaceVersion"`   // 装进去的那个安装包的版本 (助手安装时写的)
	XochitlActive  bool   `json:"xochitlActive"`

	InAndroidMode    bool   `json:"inAndroidMode"`    // 当前是 Android 模式 (dropbear 应答)
	AndroidInstalled bool   `json:"androidInstalled"` // 本槽已有单槽 Android 组件
	AndroidVersion   string `json:"androidVersion"`   // 装进去的 Android 安装包版本 (助手安装时写的; 老安装没有)
	InitIsWrapper    bool   `json:"initIsWrapper"`

	// ForeignLauncher: 别的工具 (如 rmtool) 在 xochitl.service.d 里换掉了 ExecStart 的 drop-in 文件名,
	// 空格分隔。它们用自己的启动器拉 xochitl, 空间的注入进不去, 两套 xovi 并存还可能崩溃循环 → 拒装。
	ForeignLauncher string `json:"foreignLauncher"`
}

// 单次往返拿齐所有字段, 每行 key=value。
const probeScript = `
echo arch=$(uname -m)
echo fw=$(cat /etc/version 2>/dev/null | head -n 1 | tr -d '[:space:]')
echo img=$(sed -n 's/^IMG_VERSION=//p' /etc/os-release 2>/dev/null | tr -d '"[:space:]')
echo dtmodel=$(tr -d '\0' < /proc/device-tree/model 2>/dev/null)
echo res=$(cat /sys/class/graphics/fb0/virtual_size 2>/dev/null)
echo slot=$(rootdev 2>/dev/null)
echo next=$(rootdev --next-boot 2>/dev/null)
echo erra=$(cat /sys/devices/platform/lpgpr/roota_errcnt 2>/dev/null)
echo errb=$(cat /sys/devices/platform/lpgpr/rootb_errcnt 2>/dev/null)
echo secboot=$(cat /sys/devices/platform/lpgpr/secboot 2>/dev/null)
grep -q " atomics" /proc/cpuinfo 2>/dev/null && echo lse=1
echo rootfree=$(df -kP / 2>/dev/null | awk 'END{print $4}')
echo homefree=$(df -kP /home 2>/dev/null | awk 'END{print $4}')
# 只有 xovi.so 不够: 生成 hashtab 靠 qt-resource-rebuilder 扩展。别的工具 (rmtool 等) 装的 xovi
# 可能没启用它, 当成"有 xovi"跳过部署 → hashtab 永远生不出来, 安装回退 (2026-10-30 客户 Move)
[ -f /home/root/xovi/xovi.so ] && [ -f /home/root/xovi/extensions.d/qt-resource-rebuilder.so ] && echo xovi=1
[ -d /home/root/rmkit-cn/bin ] && echo rmkit=1
echo rmkitfw=$(cat /home/root/rmkit-cn/.last_fw_version 2>/dev/null)
echo spacever=$(cat /home/root/rmkit-cn/.space_version 2>/dev/null)
echo androidver=$(cat /home/root/rmkit-cn/.android_version 2>/dev/null)
[ -f /home/root/rmkit-cn/space/shell/Space.qml ] && [ -f /home/root/rmkit-cn/space/.primary ] && echo space=1
[ "$(cat /proc/1/comm 2>/dev/null)" = "systemd" ] && systemctl is-active remarkable-space >/dev/null 2>&1 && echo spaceactive=1
[ "$(cat /proc/1/comm 2>/dev/null)" = "systemd" ] && systemctl is-active xochitl >/dev/null 2>&1 && echo xochitl=1
[ -d /android ] && [ "$(cat /proc/1/comm 2>/dev/null)" != "systemd" ] && echo androidmode=1
grep -q boot-android-mode /sbin/init 2>/dev/null && echo initwrap=1
fl=""
for f in /etc/systemd/system/xochitl.service.d/*.conf; do
  [ -f "$f" ] || continue
  n=${f##*/}
  case "$n" in rmkit-cn.conf|zz-rmkit-cn.conf|zz-space.conf) continue ;; esac
  grep -qE '^[[:space:]]*ExecStart=[^[:space:]]' "$f" 2>/dev/null && fl="$fl $n"
done
echo foreign=$fl
[ -f /boot/fitImage.ahab-android ] && [ -x /usr/bin/rm-android-init-ss ] && echo android=1
`

// Run 探测设备。
func Run(ctx context.Context, c *sshx.Client) (Info, error) {
	res, err := c.Run(ctx, probeScript)
	if err != nil {
		return Info{}, err
	}
	kv := map[string]string{}
	for _, line := range strings.Split(res.Stdout, "\n") {
		k, v, ok := strings.Cut(strings.TrimSpace(line), "=")
		if ok {
			kv[k] = strings.TrimSpace(v)
		}
	}
	info := Info{
		Arch:             kv["arch"],
		FWVersion:        kv["fw"],
		IMGVersion:       kv["img"],
		Resolution:       kv["res"],
		ActiveSlot:       kv["slot"],
		NextBoot:         kv["next"],
		ErrcntA:          atoi(kv["erra"]),
		ErrcntB:          atoi(kv["errb"]),
		Secboot:          kv["secboot"],
		HasLSE:           kv["lse"] == "1",
		RootFreeMB:       atoi(kv["rootfree"]) / 1024,
		HomeFreeMB:       atoi(kv["homefree"]) / 1024,
		HaveXovi:         kv["xovi"] == "1",
		RmkitInstalled:   kv["rmkit"] == "1",
		SpaceInstalled:   kv["space"] == "1",
		SpaceActive:      kv["spaceactive"] == "1",
		RmkitFW:          kv["rmkitfw"],
		SpaceVersion:     kv["spacever"],
		XochitlActive:    kv["xochitl"] == "1",
		InAndroidMode:    kv["androidmode"] == "1",
		AndroidInstalled: kv["android"] == "1",
		AndroidVersion:   kv["androidver"],
		InitIsWrapper:    kv["initwrap"] == "1",
		ForeignLauncher:  kv["foreign"],
	}
	info.ModelKey, info.Model = classify(kv["dtmodel"], info.Resolution, info.Arch)
	if info.Resolution == "" {
		info.Resolution = kv["dtmodel"]
	}
	return info, nil
}

// classify 优先按设备树型号 (3.28 起 /sys/class/graphics/fb0 不再存在), 再按分辨率
// (与 installer/install.sh 一致), 最后按架构兜底。
func classify(dtModel, res, arch string) (string, string) {
	switch {
	case strings.Contains(dtModel, "Chiappa"):
		return "rmppm", "reMarkable Paper Pro Move"
	case strings.Contains(dtModel, "Ferrari"):
		return "rmpp", "reMarkable Paper Pro"
	case strings.Contains(dtModel, "reMarkable 2"):
		return "rm2", "reMarkable 2"
	}
	switch res {
	case "1404,1872":
		return "rm2", "reMarkable 2"
	case "2160,2880":
		return "rmpp", "reMarkable Paper Pro"
	case "1696,954":
		return "rmppm", "reMarkable Paper Pro Move"
	}
	if arch == "armv7l" {
		return "unknown", i18n.T("未知型号", "Unknown model") + " (armv7l, " + res + ")"
	}
	return "unknown", i18n.T("未知型号", "Unknown model") + " (" + res + ")"
}

func atoi(s string) int {
	n, _ := strconv.Atoi(strings.TrimSpace(s))
	return n
}

// FWID 是底座认的固件身份 "构建时间-版本号" (如 20261009073202-3.29.0.153),
// 与设备上 precheck.sh / fw-upgrade.sh 的 fw_id 必须一致: .last_fw_version、
// hashtab.fw_version、compiled-qmd/<身份>/ 都按它记。只用构建时间会漏掉
// 3.29.0.152 → .153 这种构建时间不变的升级 (2026-10-15 实测因此熔断)。
func (i Info) FWID() string {
	if i.FWVersion == "" || i.IMGVersion == "" {
		return i.FWVersion
	}
	return i.FWVersion + "-" + i.IMGVersion
}
