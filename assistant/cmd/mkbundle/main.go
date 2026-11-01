// mkbundle 从仓库产物生成桌面助手的安装包 (zip + manifest.json, 每文件 sha256)。
//
//	go run ./cmd/mkbundle -component space -repo ../base -space-repo .. -out space-bundle.zip
//	go run ./cmd/mkbundle -component android-rmppm -android-dir <目录> -out android-rmppm-bundle.zip
//
// rmkit-cn 包装的是"底座 + 空间"一整套: 底座 (xovi/注入链/汉化/输入法后端) 按
// installer/install.sh 需要的仓库路径从 -repo 收集, 空间 (外壳/内置应用/后端二进制)
// 从 -space-repo 收集, 两边在包里分别以 space/ 和 space-dist/ 前缀存放。
// android 包收集 -android-dir 下的固定文件名 (见 internal/android.required)。
package main

import (
	"archive/zip"
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"flag"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"github.com/boangs/remarkable-space/assistant/internal/bundle"
)

// rmkit-cn: 目录整体收集 (递归) 与单文件
var rmkitDirs = []string{"dist", "vendor/xovi", "vendor/extensions", "vendor/audio", "qmd-src", "assets/chess", "systemd"}
var rmkitFiles = []string{
	"qmd/pinyin_interceptor.qmd", "qmd/zh_CN.rcc", "qmd/zh_CN/keyboard_layout.json",
	"scripts/version-switcher.sh",
	"installer/reenable.sh", "installer/fw-upgrade.sh", "installer/precheck.sh", "installer/qml-inject-lib.sh", "installer/ota-watch.sh",
	"intercept/qml-inject/adv_panel.qml", "intercept/qml-inject/glyph_ai_button.qml", "intercept/qml-inject/text_ai_button.qml",
	"intercept/qml-inject/pinyin_ime.qml", "intercept/qml-inject/icon_ai.svg",
}

// dist/ 里只有这些需要进包 (主机侧 qmd-tool / 备份文件不带)
func wantDist(name string) bool {
	base := filepath.Base(name)
	if strings.HasPrefix(base, "qmd-tool-") && base != "qmd-tool-aarch64" && base != "qmd-tool-armv7" {
		return false
	}
	return !strings.Contains(base, ".bak")
}

var androidFiles = []string{"fitImage.ahab-android", "modules.tar.gz", "rm-android-init-ss", "rm-touch-relay",
	"rm-epd-bridge", "rm-native-controls", "propset", "boot-android.sh", "init-wrapper.tmpl.sh",
	"android-kernel-revert.service.tmpl", "udhcpd-usb.conf", "udhcpd-usb1.conf", "rm-wifi-config"}

func main() {
	component := flag.String("component", "space", "space | android-rmppm")
	repo := flag.String("repo", "../base", "底座目录 (本仓库 base/, 原 rmkit-cn)")
	spaceRepo := flag.String("space-repo", "..", "remarkable-space 仓库根目录")
	androidDir := flag.String("android-dir", "", "android 文件目录")
	out := flag.String("out", "", "输出 zip")
	version := flag.String("version", time.Now().Format("20060102-1504"), "版本串")
	notes := flag.String("notes", "", "说明")
	flag.Parse()
	if *out == "" {
		*out = *component + "-bundle-" + *version + ".zip"
	}
	files := map[string]string{} // zip 内路径 → 本地路径
	switch bundle.Component(*component) {
	case bundle.ComponentSpace:
		for _, d := range rmkitDirs {
			root := filepath.Join(*repo, d)
			err := filepath.WalkDir(root, func(p string, e os.DirEntry, err error) error {
				if err != nil || e.IsDir() {
					return err
				}
				rel, _ := filepath.Rel(*repo, p)
				rel = filepath.ToSlash(rel)
				if strings.HasPrefix(rel, "dist/") && !wantDist(rel) {
					return nil
				}
				if strings.Contains(rel, ".bak") || strings.HasSuffix(rel, ".DS_Store") {
					return nil
				}
				// upload-server 已退役, 它的 unit 不进包 (活全归 remarkable-space.service)
				if rel == "systemd/rmkit-cn-upload.service" {
					return nil
				}
				files[rel] = p
				return nil
			})
			if err != nil {
				die("收集 %s: %v", d, err)
			}
		}
		for _, f := range rmkitFiles {
			p := filepath.Join(*repo, f)
			if _, err := os.Stat(p); err == nil {
				files[f] = p
			} else {
				fmt.Fprintf(os.Stderr, "  · 跳过不存在的 %s\n", f)
			}
		}
		// install.sh 165-178: rime 二进制必须带 /rime/schema 才算可用 (否则回落纯 Go);
		// install.sh 391: 最终生效的 ime-server 必须带 /rime/input 路由 (前端 404 死循环事故)。
		// 按架构算出"最终生效"的那个来检查; 只是回落用的纯 Go 旧版不致命, 但提示。
		_, hasPrebuilt := files["dist/rime-prebuilt.tar.gz"]
		_, hasRuntime := files["dist/rime-runtime-data.tar.gz"]
		hasRime := hasPrebuilt && hasRuntime
		for _, ar := range []struct{ rime, pure string }{{"ime-server-rime-aarch64", "ime-server"}, {"ime-server-rime-armv7", "ime-server-armv7"}} {
			rp, rok := files["dist/"+ar.rime]
			if rok && !fileContains(rp, "/rime/schema") {
				fmt.Fprintf(os.Stderr, "  · dist/%s 无 /rime/schema (旧构建), 不入包 → 该架构回落纯 Go 引擎\n", ar.rime)
				delete(files, "dist/"+ar.rime)
				rok = false
			}
			effective := "dist/" + ar.pure
			if rok && hasRime {
				effective = "dist/" + ar.rime
			}
			pp, pok := files[effective]
			if !pok {
				die("缺 %s (该架构没有可部署的输入法后端)", effective)
			}
			if !fileContains(pp, "/rime/input") {
				die("%s 是旧版 (无 /rime 路由), 请先重新构建 ime-go", effective)
			}
			if p, ok := files["dist/"+ar.pure]; ok && !fileContains(p, "/rime/input") {
				fmt.Fprintf(os.Stderr, "  · dist/%s 是旧版纯 Go 后端 (无 /rime 路由), 仅作回落; 建议 make -C ime-go 重建\n", ar.pure)
			}
		}
		// 空间: 外壳/共享组件/内置应用整树 + 后端二进制 + 静态页 + 矢量底图。
		// 装在设备上的落点见 internal/rmkit.NewPlan。
		for _, d := range []string{"shell", "kit", "apps", "hooks", "themes"} {
			root := filepath.Join(*spaceRepo, d)
			err := filepath.WalkDir(root, func(p string, e os.DirEntry, err error) error {
				if err != nil || e.IsDir() {
					return err
				}
				rel, _ := filepath.Rel(*spaceRepo, p)
				rel = filepath.ToSlash(rel)
				if strings.HasSuffix(rel, ".DS_Store") {
					return nil
				}
				files["space/"+rel] = p
				return nil
			})
			if err != nil {
				die("收集空间 %s: %v", d, err)
			}
		}
		for zipName, local := range map[string]string{
			"space/static/qr.html": "static/qr.html",
			// 空间后端的 unit 混进底座的 systemd/ 一起走设备端阶段 1 的双写
			"systemd/remarkable-space.service": "systemd/remarkable-space.service",
			// 空间自己的 xochitl drop-in 单独放: 它要落到 xochitl.service.d/ 而不是
			// unit 目录, 底座阶段 1 的循环只认 .service/.path/.timer, 混在一起会被漏掉。
			"space-systemd/zz-space.conf": "systemd/zz-space.conf",
			// 后端的 drop-in, 同理落到 remarkable-space.service.d/
			"space-systemd/10-store-api.conf": "systemd/10-store-api.conf",
			// rm-appload: KOReader 这类 framebuffer 程序要靠它拿画布。
			// 由 vendor/fetch-appload.sh 从官方拉下来 (不进仓库, 见那个脚本的说明)。
			"third_party/appload/aarch64/appload.so":         "third_party/appload/aarch64/appload.so",
			"third_party/appload/aarch64/qtfb-shim.so":       "third_party/appload/aarch64/shims/qtfb-shim.so",
			"third_party/appload/aarch64/qtfb-shim-32bit.so": "third_party/appload/aarch64/shims/qtfb-shim-32bit.so",
			"third_party/appload/armv7/appload.so":           "third_party/appload/armv7/appload.so",
			"third_party/appload/armv7/qtfb-shim.so":         "third_party/appload/armv7/shims/qtfb-shim.so",
			"third_party/appload/armv7/qtfb-shim-32bit.so":   "third_party/appload/armv7/shims/qtfb-shim-32bit.so",
		} {
			p := filepath.Join(*spaceRepo, local)
			if _, err := os.Stat(p); err != nil {
				if strings.HasPrefix(local, "third_party/appload/") {
					die("缺 %s —— 先跑 vendor/fetch-appload.sh 把官方包拉下来", p)
				}
				die("缺空间文件 %s", p)
			}
			files[zipName] = p
		}
		for _, bin := range []string{"space-server-aarch64", "space-server-armv7"} {
			p := filepath.Join(*spaceRepo, "dist", bin)
			if _, err := os.Stat(p); err != nil {
				die("缺 %s (先在 remarkable-space 跑 make all)", p)
			}
			files["space-dist/"+bin] = p
		}
	case bundle.ComponentAndroid:
		if *androidDir == "" {
			die("android-rmppm 需要 -android-dir")
		}
		for _, f := range append(append([]string{}, androidFiles...), "android-system.tar.gz") {
			p := filepath.Join(*androidDir, f)
			if _, err := os.Stat(p); err != nil {
				if f == "android-system.tar.gz" {
					fmt.Fprintln(os.Stderr, "  · 无 android-system.tar.gz, 包不含 Android 系统 (设备须已有)")
					continue
				}
				die("缺 %s", p)
			}
			files["android/"+f] = p
		}
	default:
		die("未知组件 %s", *component)
	}

	zf, err := os.Create(*out)
	if err != nil {
		die("%v", err)
	}
	zw := zip.NewWriter(zf)
	mf := bundle.Manifest{Component: bundle.Component(*component), Version: *version, Created: time.Now().UTC().Format(time.RFC3339), Notes: *notes, Files: map[string]bundle.Entry{}}
	names := make([]string, 0, len(files))
	for n := range files {
		names = append(names, n)
	}
	sort.Strings(names)
	var total int64
	for _, n := range names {
		src, err := os.Open(files[n])
		if err != nil {
			die("%v", err)
		}
		method := zip.Deflate
		if strings.HasSuffix(n, ".gz") || strings.HasSuffix(n, ".zip") {
			method = zip.Store // 已压缩的不再压
		}
		w, err := zw.CreateHeader(&zip.FileHeader{Name: n, Method: method, Modified: time.Now()})
		if err != nil {
			die("%v", err)
		}
		h := sha256.New()
		size, err := io.Copy(io.MultiWriter(w, h), src)
		_ = src.Close()
		if err != nil {
			die("写 %s: %v", n, err)
		}
		mf.Files[n] = bundle.Entry{Size: size, SHA256: hex.EncodeToString(h.Sum(nil))}
		total += size
	}
	var mb bytes.Buffer
	enc := json.NewEncoder(&mb)
	enc.SetIndent("", "  ")
	_ = enc.Encode(mf)
	w, _ := zw.Create(bundle.ManifestName)
	_, _ = w.Write(mb.Bytes())
	if err := zw.Close(); err != nil {
		die("%v", err)
	}
	_ = zf.Close()
	st, _ := os.Stat(*out)
	fmt.Printf("✓ %s: %d 个文件, 原始 %.1f MB, 包 %.1f MB, 版本 %s\n", *out, len(names), float64(total)/1e6, float64(st.Size())/1e6, *version)
}

func fileContains(p, needle string) bool {
	b, err := os.ReadFile(p)
	return err == nil && bytes.Contains(b, []byte(needle))
}

func die(format string, a ...any) {
	fmt.Fprintf(os.Stderr, "✗ "+format+"\n", a...)
	os.Exit(1)
}
