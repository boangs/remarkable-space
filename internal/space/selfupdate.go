package space

// 空间在设备上自更新 (v1.1.0 起): 设置 → 关于空间 → 点击更新。
//
// 只换空间自己的部分: space-server、外壳 shell/、组件 kit/、内置应用 apps/、挂载组件 hooks/、静态页 static/。
// 底座 (注入库、汉化、输入法、防砖链) 和 systemd 单元不在这里 —— 那些只走电脑上的助手。
//
// 包是商店索引里 id 为 "space" 的条目 (min_shell 3, 旧外壳的商店看不到它), 商店按设备签 .integrity。
// 流程: 下载 → sha256 → 解到暂存目录 → 验商店签名 → 新 space-server -selftest 自检 (能在本机跑、
// 外壳和内置应用清单都完整) → 各部分换成新的, 旧的改名 .prev → 写版本号 → 交给看门狗重启服务。
// 看门狗 (update-guard.sh, systemd-run 独立运行, 不随本进程退出): 60 秒内新服务回不来, 就把 .prev
// 换回去再重启, 并留下 .update-result 让下次打开设置页时说明。
// 外壳的 shell.prev 一直留着: 新外壳 QML 写坏了打不开时, 注入库退回它 (见 qml_inject_impl.cpp)。

import (
	"archive/zip"
	"encoding/json"
	"errors"
	"fmt"
	"log"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"sync"
	"time"
)

// UpdateID 商店索引里空间自己那一项的 id
const UpdateID = "space"

// 自更新换掉的部分。shell/kit 按版本放 rel/<版本>/ 下、用链接切换 (见 swapVersioned);
// 其余整目录改名换, 旧的留 .prev。
var (
	updateLinked     = []string{"shell", "kit"}
	updateSpaceParts = []string{"apps", "hooks", "themes"}
	versionDirSafeRe = regexp.MustCompile(`[^A-Za-z0-9._-]`)
)

type updateState struct {
	Phase string `json:"phase"` // "" / downloading / verifying / testing / switching / restarting / error
	Got   int64  `json:"got"`
	Total int64  `json:"total"`
	Error string `json:"error,omitempty"`
}

var (
	updMu sync.Mutex
	upd   updateState
)

func setUpd(f func(*updateState)) {
	updMu.Lock()
	defer updMu.Unlock()
	f(&upd)
}

// 路径都从注册表推: 内置应用目录 = <空间目录>/apps, 版本号文件在空间目录的上一级 (助手也写这里)
func (h *Handler) spaceRoot() string { return filepath.Dir(h.Reg.BuiltinDir) }
func (h *Handler) versionFile() string {
	return filepath.Join(filepath.Dir(h.spaceRoot()), ".space_version")
}
func serverDir() string {
	exe, err := os.Executable()
	if err != nil {
		return "/home/root/remarkable-space"
	}
	return filepath.Dir(exe)
}

// SpaceVersion 这台设备上空间的版本 (助手或自更新写的), 读不到是空串
func (h *Handler) SpaceVersion() string {
	b, _ := os.ReadFile(h.versionFile())
	return strings.TrimSpace(string(b))
}

var verNumRe = regexp.MustCompile(`\d+`)

// VersionCmp 比较 v1.0.12 / 1.1.0 / v1.1.0-beta.2 这类版本号。
// 主体只看数字段; 带 "-" 预发布后缀的低于同号正式版 (semver), 两个都带后缀时再比后缀的数字段。
// 以前整串抽数字, v1.1.0-beta.2 被当成 [1,1,0,2] 比 v1.1.0 还高, 装过 beta 的机器看不到正式版 (2026-10-24)。
func VersionCmp(a, b string) int {
	ma, pa, _ := strings.Cut(a, "-")
	mb, pb, _ := strings.Cut(b, "-")
	if c := numCmp(ma, mb); c != 0 {
		return c
	}
	switch {
	case pa == "" && pb != "":
		return 1
	case pa != "" && pb == "":
		return -1
	}
	return numCmp(pa, pb)
}

func numCmp(a, b string) int {
	x, y := verNumRe.FindAllString(a, -1), verNumRe.FindAllString(b, -1)
	for i := 0; i < len(x) || i < len(y); i++ {
		var p, q int
		if i < len(x) {
			p, _ = strconv.Atoi(x[i])
		}
		if i < len(y) {
			q, _ = strconv.Atoi(y[i])
		}
		if p != q {
			if p < q {
				return -1
			}
			return 1
		}
	}
	return 0
}

// GET /space/update → 当前版本、商店里的新版本、进行中的阶段、上次没成功的原因
func (h *Handler) updateStatus(w http.ResponseWriter, r *http.Request) {
	out := map[string]any{"current": h.SpaceVersion()}
	if e, err := h.updateEntry(r.URL.Query().Get("refresh") == "1"); err == nil && e != nil {
		latest, _ := e["version"].(string)
		out["latest"] = latest
		out["available"] = VersionCmp(latest, h.SpaceVersion()) > 0
		for _, k := range []string{"size", "notes", "notes_en"} {
			if v, ok := e[k]; ok {
				out[k] = v
			}
		}
	} else if err != nil {
		out["checkError"] = err.Error()
	}
	updMu.Lock()
	out["state"] = upd
	updMu.Unlock()
	// 看门狗留下的结果: 回退过就告诉用户一次
	if b, err := os.ReadFile(filepath.Join(serverDir(), ".update-result")); err == nil {
		if strings.TrimSpace(string(b)) == "rollback" {
			out["rolledBack"] = true
		}
		_ = os.Remove(filepath.Join(serverDir(), ".update-result"))
	}
	writeJSON(w, http.StatusOK, out)
}

// updateEntry 从商店索引里找空间自己那一项 (索引走和商店页同一份缓存; refresh 先作废缓存)
func (h *Handler) updateEntry(refresh bool) (map[string]any, error) {
	if h.StoreURL == "" && h.StoreAPI == "" {
		return nil, errors.New("没有配置应用商店")
	}
	if refresh {
		h.storeMu.Lock()
		h.storeBody = nil
		h.storeMu.Unlock()
	}
	return h.appEntry(UpdateID)
}

// POST /space/update → 后台开始更新, 进度看 GET /space/update 的 state
func (h *Handler) updateStart(w http.ResponseWriter, r *http.Request) {
	e, err := h.updateEntry(true)
	if err != nil {
		fail(w, http.StatusBadGateway, err.Error())
		return
	}
	if e == nil {
		fail(w, http.StatusNotFound, "商店里没有空间的新版本")
		return
	}
	latest, _ := e["version"].(string)
	if VersionCmp(latest, h.SpaceVersion()) <= 0 {
		fail(w, http.StatusConflict, "已经是最新版本")
		return
	}
	updMu.Lock()
	busy := upd.Phase != "" && upd.Phase != "error"
	if !busy {
		upd = updateState{Phase: "downloading"}
	}
	updMu.Unlock()
	if busy {
		fail(w, http.StatusConflict, "正在更新")
		return
	}
	url, _ := e["zip"].(string)
	sha, _ := e["sha256"].(string)
	go func() {
		if err := h.runUpdate(url, sha, latest); err != nil {
			log.Printf("空间自更新失败: %v", err)
			setUpd(func(s *updateState) { s.Phase = "error"; s.Error = err.Error() })
		}
	}()
	writeJSON(w, http.StatusOK, map[string]any{"started": true, "version": latest})
}

func (h *Handler) runUpdate(url, sha, version string) error {
	work := filepath.Join(serverDir(), ".update")
	_ = os.RemoveAll(work)
	if err := os.MkdirAll(work, 0o755); err != nil {
		return err
	}
	// 1. 下载 (放 /home 下: /tmp 是内存盘) 并校验摘要
	zf, err := os.Create(filepath.Join(work, "space.zip"))
	if err != nil {
		return err
	}
	err = downloadPkg(url, sha, h.downloadHeader(), zf, maxAppBytes, func(got, total int64) {
		setUpd(func(s *updateState) { s.Got, s.Total = got, total })
	})
	zf.Close()
	if err != nil {
		return err
	}
	// 2. 解包, 验商店签名 (这台设备的专属包)
	setUpd(func(s *updateState) { s.Phase = "verifying" })
	zr, err := zip.OpenReader(filepath.Join(work, "space.zip"))
	if err != nil {
		return fmt.Errorf("更新包打不开: %w", err)
	}
	stage := filepath.Join(work, "stage")
	err = extract(&zr.Reader, "", stage)
	zr.Close()
	if err != nil {
		return fmt.Errorf("更新包解不开: %w", err)
	}
	if err := VerifyIntegrity(stage, UpdateID); err != nil {
		return fmt.Errorf("更新包校验不通过: %w", err)
	}
	bin := filepath.Join(stage, "bin", "space-server-"+h.Reg.Arch)
	if _, err := os.Stat(bin); err != nil {
		return fmt.Errorf("更新包里没有本机架构 (%s) 的后台", h.Reg.Arch)
	}
	_ = os.Chmod(bin, 0o755)
	// 3. 新后台自检: 能在本机跑, 外壳与内置应用清单完整
	setUpd(func(s *updateState) { s.Phase = "testing" })
	out, err := exec.Command(bin, "-selftest", stage).CombinedOutput()
	if err != nil {
		return fmt.Errorf("新版本自检没通过: %v %s", err, strings.TrimSpace(string(out)))
	}
	// 4. 换: 外壳与组件切链接到 rel/<新版本>/; 其余旧的改名 .prev, 新的放进去
	setUpd(func(s *updateState) { s.Phase = "switching" })
	if err := h.swapVersioned(stage, version); err != nil {
		return err
	}
	type part struct{ cur, next string }
	var parts []part
	for _, p := range updateSpaceParts {
		if _, err := os.Stat(filepath.Join(stage, p)); err == nil {
			parts = append(parts, part{filepath.Join(h.spaceRoot(), p), filepath.Join(stage, p)})
		}
	}
	if _, err := os.Stat(filepath.Join(stage, "static")); err == nil {
		parts = append(parts, part{filepath.Join(serverDir(), "static"), filepath.Join(stage, "static")})
	}
	parts = append(parts, part{filepath.Join(serverDir(), "space-server"), bin})
	for _, p := range parts {
		_ = os.RemoveAll(p.cur + ".prev")
		if err := os.Rename(p.cur, p.cur+".prev"); err != nil && !os.IsNotExist(err) {
			return fmt.Errorf("换 %s 失败: %w", p.cur, err)
		}
		if err := os.Rename(p.next, p.cur); err != nil {
			_ = os.Rename(p.cur+".prev", p.cur) // 这一项换不上就把旧的放回去
			return fmt.Errorf("换 %s 失败: %w", p.cur, err)
		}
	}
	vf := h.versionFile()
	_ = os.Rename(vf, vf+".prev")
	_ = os.WriteFile(vf, []byte(version+"\n"), 0o644)
	_ = os.RemoveAll(work)
	// 5. 看门狗接手: 重启服务, 60 秒内起不来就回退
	setUpd(func(s *updateState) { s.Phase = "restarting" })
	return h.startGuard()
}

// swapVersioned 外壳 shell/ 与组件 kit/ 放进 rel/<版本>/, 再把 shell、kit 两个链接原子地指过去,
// shell.prev / kit.prev 指着上一版。为什么不像别的目录那样直接改名换: xochitl 一次运行里按地址缓存
// 编译好的 QML, 同一路径换了内容它看不见 —— 新外壳配旧 kit 组件会直接加载失败。注入库按真实路径
// 加载外壳 (rel/<版本>/shell/Space.qml), 外壳相对引入 ../kit, 每个版本地址都不一样。
func (h *Handler) swapVersioned(stage, version string) error {
	root := h.spaceRoot()
	ver := versionDirSafeRe.ReplaceAllString(strings.TrimPrefix(version, "v"), "_")
	dst := filepath.Join(root, "rel", ver)
	_ = os.RemoveAll(dst)
	if err := os.MkdirAll(dst, 0o755); err != nil {
		return err
	}
	for _, p := range updateLinked {
		if err := os.Rename(filepath.Join(stage, p), filepath.Join(dst, p)); err != nil {
			return fmt.Errorf("放 %s 失败: %w", p, err)
		}
	}
	keep := map[string]bool{ver: true}
	for _, p := range updateLinked {
		cur := filepath.Join(root, p)
		// 助手装的是真目录: 先挪进 rel/<当前版本>/, 当作上一版
		if st, err := os.Lstat(cur); err == nil && st.Mode()&os.ModeSymlink == 0 {
			old := versionDirSafeRe.ReplaceAllString(strings.TrimPrefix(h.SpaceVersion(), "v"), "_")
			if old == "" || old == ver {
				old = "base"
			}
			od := filepath.Join(root, "rel", old)
			_ = os.MkdirAll(od, 0o755)
			_ = os.RemoveAll(filepath.Join(od, p))
			if err := os.Rename(cur, filepath.Join(od, p)); err != nil {
				return fmt.Errorf("挪旧的 %s 失败: %w", p, err)
			}
			_ = os.Symlink(filepath.Join(od, p), cur)
		}
		prevTarget, _ := os.Readlink(cur)
		if prevTarget != "" {
			keep[filepath.Base(filepath.Dir(prevTarget))] = true
			_ = os.Remove(cur + ".prev")
			_ = os.Symlink(prevTarget, cur+".prev")
		}
		tmp := cur + ".new"
		_ = os.Remove(tmp)
		if err := os.Symlink(filepath.Join(dst, p), tmp); err != nil {
			return err
		}
		if err := os.Rename(tmp, cur); err != nil { // rename 覆盖链接是原子的
			return fmt.Errorf("切 %s 失败: %w", p, err)
		}
	}
	// 只留当前和上一版
	ents, _ := os.ReadDir(filepath.Join(root, "rel"))
	for _, e := range ents {
		if !keep[e.Name()] {
			_ = os.RemoveAll(filepath.Join(root, "rel", e.Name()))
		}
	}
	return nil
}

func (h *Handler) startGuard() error {
	sd, sr, vf := serverDir(), h.spaceRoot(), h.versionFile()
	var paths []string
	for _, p := range updateSpaceParts {
		paths = append(paths, filepath.Join(sr, p))
	}
	paths = append(paths, filepath.Join(sd, "static"), filepath.Join(sd, "space-server"))
	var linked []string
	for _, p := range updateLinked {
		linked = append(linked, filepath.Join(sr, p))
	}
	script := `#!/bin/sh
# 空间自更新的看门狗 (space-server 写的, 每次更新覆盖)。不随 space-server 退出: systemd-run 起的独立单元。
systemctl restart remarkable-space.service
i=0
while [ $i -lt 60 ]; do
  sleep 3; i=$((i+3))
  if wget -q -O /dev/null http://127.0.0.1:8080/space/apps; then echo ok > ` + sd + `/.update-result; exit 0; fi
done
for p in ` + strings.Join(paths, " ") + `; do
  if [ -e "$p.prev" ]; then rm -rf "$p.failed"; mv "$p" "$p.failed"; mv "$p.prev" "$p"; fi
done
for p in ` + strings.Join(linked, " ") + `; do
  if [ -L "$p.prev" ]; then ln -sfn "$(readlink "$p.prev")" "$p"; fi
done
[ -e ` + vf + `.prev ] && mv ` + vf + `.prev ` + vf + `
echo rollback > ` + sd + `/.update-result
systemctl restart remarkable-space.service
`
	gp := filepath.Join(sd, "update-guard.sh")
	if err := os.WriteFile(gp, []byte(script), 0o755); err != nil {
		return err
	}
	return exec.Command("systemd-run", "--unit=space-update-guard-"+strconv.FormatInt(time.Now().Unix(), 10),
		"--no-block", "/bin/sh", gp).Run()
}

// SelfTest 新版后台在替换前对着暂存目录自检 (space-server -selftest <目录>): 外壳在、内置应用清单都能过校验。
func SelfTest(dir, arch string) error {
	if _, err := os.Stat(filepath.Join(dir, "shell", "Space.qml")); err != nil {
		return errors.New("缺 shell/Space.qml")
	}
	r := &Registry{BuiltinDir: filepath.Join(dir, "apps"), Arch: arch}
	for _, a := range r.scanDir(r.BuiltinDir, true) {
		// 只给别的架构的内置应用 (如 Android 只有 aarch64) 在本机本来就不显示, 不算坏 ——
		// 之前把「不支持本机架构」也当自检失败, rm2 (armv7) 永远自更新不了
		if !a.SupportsArch(arch) {
			continue
		}
		if a.Error != "" {
			return fmt.Errorf("内置应用 %s: %s", a.ID, a.Error)
		}
	}
	return nil
}

// defaultApps 空间更新 (助手安装或自更新) 后要自动装上的商店应用: 它们原来是空间自带的功能,
// 拆成应用后老用户不该因为更新就丢了。每个只装一次 —— 用户之后自己删了就不再装回来。
var defaultApps = []string{"ai"}

// AutoInstallDefaults 开机 30 秒后在后台补装 defaultApps (商店连不上就等下次开机再试)。
func (h *Handler) AutoInstallDefaults() {
	go func() {
		time.Sleep(30 * time.Second)
		stateFile := filepath.Join(filepath.Dir(h.Reg.UserDir), "default-apps.json")
		done := map[string]bool{}
		if b, err := os.ReadFile(stateFile); err == nil {
			_ = json.Unmarshal(b, &done)
		}
		save := func() {
			b, _ := json.Marshal(done)
			_ = os.WriteFile(stateFile, b, 0o644)
		}
		for _, id := range defaultApps {
			if done[id] {
				continue
			}
			if _, ok := h.Reg.Get(id); ok {
				done[id] = true
				save()
				continue
			}
			e, err := h.appEntry(id)
			if err != nil || e == nil {
				log.Printf("space: 补装 %s: 商店里找不到 (%v), 下次开机再试", id, err)
				continue
			}
			url, _ := e["zip"].(string)
			sha, _ := e["sha256"].(string)
			if locked, _ := e["locked"].(bool); locked || url == "" {
				continue
			}
			// 跟商店页的安装共用一份进度: 先占住 (商店页这时点获取会看到在装什么),
			// 装完一定要放开 —— 停在 installing 的话之后商店页每次点获取都被当成"正在安装"拒掉
			if p := InstallProgress(); p.Phase == "downloading" || p.Phase == "installing" {
				continue // 用户正在商店里装别的, 下次开机再补
			}
			setProgress(func(p *Progress) { *p = Progress{ID: id, Name: id, Phase: "downloading"} })
			app, err := h.Reg.InstallFromURL(url, sha, h.downloadHeader(), h.Sup)
			setProgress(func(p *Progress) {
				if err != nil {
					p.Phase, p.Error = "error", err.Error()
				} else {
					p.Phase, p.ID, p.Name = "done", app.ID, app.Name
				}
			})
			if err != nil {
				log.Printf("space: 补装 %s 失败: %v", id, err)
				continue
			}
			h.afterInstall(app)
			done[id] = true
			save()
			log.Printf("space: 已补装 %s %s", app.ID, app.Version)
		}
	}()
}

// appEntry 商店索引里某个应用的条目
func (h *Handler) appEntry(id string) (map[string]any, error) {
	h.storeMu.Lock()
	if h.storeBody == nil || time.Since(h.storeAt) > storeCacheTTL {
		body, err := h.fetchIndex()
		if err != nil {
			h.storeMu.Unlock()
			return nil, err
		}
		h.storeBody, h.storeAt = body, time.Now()
	}
	body := h.storeBody
	h.storeMu.Unlock()
	var idx struct {
		Apps []map[string]any `json:"apps"`
	}
	if err := json.Unmarshal(body, &idx); err != nil {
		return nil, err
	}
	for _, a := range idx.Apps {
		if v, _ := a["id"].(string); v == id {
			return a, nil
		}
	}
	return nil, nil
}
