// space-server — 「空间」的后端, 一个进程把前台要用的服务全带齐:
// 文件上传、蓝牙音频、字体/锁屏图应用、AI、截图、二维码, 以及空间自己的
// 应用注册表/绿色安装/应用托管。
//
// 它顶替 rmkit-cn 的 upload-server (同样监听 8080, 路由逐条兼容), 安装时会停掉
// rmkit-cn-upload.service。rmkit-cn 只剩注入链 (qmldiff/.so/汉化/输入法后端) 当底座。
//
// 设备上的数据路径一律沿用 rmkit-cn 时期的位置 —— 字体、锁屏图、空间应用与应用数据
// 都在 ~/.local/share/rmkit-cn/ 下, 改名就等于丢用户数据。
package main

import (
	"flag"
	"fmt"
	"log"
	"net"
	"net/http"
	"os"
	"path/filepath"
	"runtime"
	"strconv"

	"github.com/boangs/remarkable-space/internal/server"
	"github.com/boangs/remarkable-space/internal/space"
)

func main() {
	var (
		listen     = flag.String("listen", ":8080", "监听地址")
		staticDir  = flag.String("static", "", "静态资源目录 (默认: <二进制所在目录>/static)")
		fontsDir   = flag.String("fonts", "", "字体存放目录 (默认: $HOME/.local/share/rmkit-cn/fonts)")
		screensDir = flag.String("screens", "", "锁屏图存放目录 (默认: $HOME/.local/share/rmkit-cn/screens)")
		musicDir   = flag.String("music", "", "音乐存放目录 (默认: $HOME/.local/share/rmkit-cn/music)")
		heroDir    = flag.String("hero", "", "「空间」首页插图目录 (默认: $HOME/.local/share/rmkit-cn/space/hero)")
		// 已不用: 文档改由 xolib 直接写进 xochitl 数据目录。保留这个参数是因为装好的 systemd 单元还在传它,
		// 删掉的话旧单元会因"未知参数"起不来。
		_              = flag.String("staging", "", "(已废弃, 忽略)")
		fontsActiveDir = flag.String("fonts-active", "", "字体激活符号链接目录 (默认: $HOME/.local/share/fonts)")
		xochitlConf    = flag.String("xochitl-conf", "", "xochitl 配置路径 (默认: $HOME/.config/remarkable/xochitl.conf)")
		spaceDir       = flag.String("space", "/home/root/rmkit-cn/space", "「空间」内置应用目录 (含 shell/ kit/ apps/)")
		spaceUserDir   = flag.String("space-apps", "", "「空间」用户应用目录 (默认: $HOME/.local/share/rmkit-cn/space/apps)")
		spaceDataDir   = flag.String("space-data", "", "「空间」应用数据目录 (默认: $HOME/.local/share/rmkit-cn/space/data)")
		spaceModDir    = flag.String("space-modules", "", "「空间」编译应用的 QML 模块目录 (默认: $HOME/.local/share/rmkit-cn/space/qmlmods; 要与 xochitl 的 QML_IMPORT_PATH 一致)")
		selftest       = flag.String("selftest", "", "自更新用: 对着这个暂存目录自检 (外壳、内置应用清单) 后退出, 不起服务")
		spaceStore     = flag.String("space-store", "https://boangs.com/rmkit-space/index.json", "「空间」应用商店索引 URL (空 = 关闭商店)")
		// 默认空: 服务端的握手接口上线之前打开它, 所有设备都会连不上商店。
		// 等 store-server 部署好再开, 见 docs-store-key.md。
		spaceStoreAPI = flag.String("space-store-api", "", "「空间」应用商店握手接口 (空 = 不握手, 直接取静态索引)")
	)
	flag.Parse()

	if *selftest != "" {
		arch := map[string]string{"arm64": "aarch64", "arm": "armv7"}[runtime.GOARCH]
		if err := space.SelfTest(*selftest, arch); err != nil {
			fmt.Fprintln(os.Stderr, err)
			os.Exit(1)
		}
		fmt.Println("selftest ok")
		return
	}

	// 环境变量兜底: service 文件由 install.sh 生成, 重装会覆盖, 手改的参数会丢。
	// 放 drop-in 里一行 Environment= 就不会被覆盖, 也不必把整条 ExecStart 抄一遍。
	if *spaceStoreAPI == "" {
		*spaceStoreAPI = os.Getenv("SPACE_STORE_API")
	}

	if *staticDir == "" {
		exe, err := os.Executable()
		if err != nil {
			log.Fatalf("os.Executable: %v", err)
		}
		*staticDir = filepath.Join(filepath.Dir(exe), "static")
	}

	home, err := os.UserHomeDir()
	if err != nil {
		log.Fatalf("home dir: %v", err)
	}
	if *fontsDir == "" {
		*fontsDir = filepath.Join(home, ".local/share/rmkit-cn/fonts")
	}
	if *screensDir == "" {
		*screensDir = filepath.Join(home, ".local/share/rmkit-cn/screens")
	}
	if *musicDir == "" {
		*musicDir = filepath.Join(home, ".local/share/rmkit-cn/music")
	}
	if *heroDir == "" {
		*heroDir = filepath.Join(home, ".local/share/rmkit-cn/space/hero")
	}
	if *fontsActiveDir == "" {
		*fontsActiveDir = filepath.Join(home, ".local/share/fonts")
	}
	if *xochitlConf == "" {
		*xochitlConf = filepath.Join(home, ".config/remarkable/xochitl.conf")
	}
	if *spaceUserDir == "" {
		*spaceUserDir = filepath.Join(home, ".local/share/rmkit-cn/space/apps")
	}
	if *spaceDataDir == "" {
		*spaceDataDir = filepath.Join(home, ".local/share/rmkit-cn/space/data")
	}
	if *spaceModDir == "" {
		*spaceModDir = filepath.Join(home, ".local/share/rmkit-cn/space/qmlmods")
	}

	srv, err := server.New(server.Config{
		StaticDir:      *staticDir,
		FontsDir:       *fontsDir,
		ScreensDir:     *screensDir,
		MusicDir:       *musicDir,
		HeroDir:        *heroDir,
		FontsActiveDir: *fontsActiveDir,
		XochitlConf:    *xochitlConf,
		SpaceDir:       *spaceDir,
		SpaceUserDir:   *spaceUserDir,
		SpaceDataDir:   *spaceDataDir,
		SpaceModDir:    *spaceModDir,
		SpaceBaseURL:   "http://127.0.0.1:8080",
		SpaceStore:     *spaceStore,
		SpaceStoreAPI:  *spaceStoreAPI,
	})
	if err != nil {
		log.Fatalf("server.New: %v", err)
	}
	server.StartAudioDaemon()         // 蓝牙音频 (装了 rmkit-audio 才生效)
	srv.Space().AutoStart()           // 「空间」里声明了 autostart 的应用后台
	srv.Space().AutoInstallDefaults() // 原来空间自带、后来拆成商店应用的功能 (AI): 更新上来补装一次
	srv.Space().EnsureOTAHook()       // 底座的 OTA 钩子 2026-10-29 改过, 已装好的机器在这里换成新的

	fmt.Printf("space-server listening on %s\n", *listen)
	fmt.Printf("  static = %s\n  fonts  = %s\n  screens = %s\n  fonts-active = %s\n  xochitl-conf = %s\n",
		*staticDir, *fontsDir, *screensDir, *fontsActiveDir, *xochitlConf)

	// 本机端口一直开着 (AI 按钮、空间外壳、应用后台都在设备上); 对外的两类端口另外管:
	// USB 网段常开, 局域网 (扫码上传) 只在空间开着时开 —— 见 internal/server/lan.go。
	// 装好的 systemd 单元传的是 -listen 0.0.0.0:8080: 这里把"所有地址"收窄成本机,
	// 对外那部分交给 ServeExternal, 免得改单元文件 (改 /etc 有变砖风险)。
	host, portStr, err := net.SplitHostPort(*listen)
	if err != nil {
		log.Fatalf("listen 参数不对: %v", err)
	}
	port, _ := strconv.Atoi(portStr)
	h := srv.Routes()
	if host == "" || host == "0.0.0.0" || host == "::" {
		go srv.ServeExternal(port, h)
		*listen = net.JoinHostPort("127.0.0.1", portStr)
	}
	if err := http.ListenAndServe(*listen, h); err != nil {
		log.Fatalf("listen: %v", err)
	}
}
