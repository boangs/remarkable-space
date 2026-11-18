# 产物放 dist/, 两个架构都出 (rm2 是 armv7, Paper Pro / Move 是 aarch64)

# 官方商店的客户端标识。带上它取索引才有东西, 浏览器直接打开索引地址是 404。
# 不进仓库 —— 发版时 make STORE_KEY=xxx, 或写进仓库根的 .store-key (已 gitignore, 一行即可)。
# 留空照样能编, 自建商店与本地开发不受影响。
STORE_KEY ?= $(strip $(shell cat .store-key 2>/dev/null))
KEY_PKG   := github.com/boangs/remarkable-space/internal/space

# 助手打包直接拿 dist/ 里的产物, 漏了密钥装出来的设备看不到商店且没有任何提示,
# 所以这里吭一声。自己编着玩不用管。
ifeq ($(strip $(STORE_KEY)),)
$(info === 没有 STORE_KEY: 这版取不到官方商店索引 (服务端会 404)。发版请 make STORE_KEY=... ===)
endif
GO_FLAGS  := -trimpath -ldflags "-s -w -X '$(KEY_PKG).storeKey=$(STORE_KEY)'"

.PHONY: all aarch64 armv7 vet clean update-zip
all: aarch64 armv7

aarch64:
	GOOS=linux GOARCH=arm64 go build $(GO_FLAGS) -o dist/space-server-aarch64 ./cmd/space-server

armv7:
	GOOS=linux GOARCH=arm GOARM=7 go build $(GO_FLAGS) -o dist/space-server-armv7 ./cmd/space-server

vet:
	go vet ./...

clean:
	rm -f dist/space-server-*

# 设备上自更新用的包 (设置 → 关于空间): 两个架构的后台 + 外壳 + 组件 + 内置应用 + 挂载组件 + 静态页。
# 不含底座和 systemd 单元 —— 那些只走助手。商店按设备给它签 .integrity (索引里 sealed)。
#   make update-zip VERSION=v1.1.0 NOTES="..." NOTES_EN="..."
# testers 只在首次进商店索引时生效 (先给内测名单), 全量开放 = 删掉线上索引里这一项的 testers。
# 产物 dist/space-<版本>.zip, 拷进应用仓库 dist/ 后 ./pack.sh --index upload 并入商店索引 (id = space)。
# 上传同时会同步到阿里云 CDN 商店 (heartblue.cn, 国内设备下载走它), 见 pack.sh 里的 cdn 段。
VERSION ?= $(shell git describe --tags --always 2>/dev/null)
update-zip: aarch64 armv7
	rm -rf dist/update && mkdir -p dist/update/bin
	cp dist/space-server-aarch64 dist/space-server-armv7 dist/update/bin/
	cp -R shell kit apps hooks themes static dist/update/
	python3 -c 'import json,os;json.dump({"id":"space","name":"空间","name_en":"Space","version":"$(VERSION)","kind":"space","min_shell":3,"category":"system","notes":os.environ.get("NOTES",""),"notes_en":os.environ.get("NOTES_EN","")},open("dist/update/manifest.json","w"),ensure_ascii=False,indent=2)'
	rm -f dist/space-$(VERSION).zip && cd dist/update && zip -q -X -r ../space-$(VERSION).zip . -x '*.DS_Store'
	@ls -l dist/space-$(VERSION).zip
