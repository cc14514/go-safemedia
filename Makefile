SHELL := /bin/bash
BIN   := bin/safemedia
PKG   := ./...

# 版本号唯一来源：internal/version/version.go 的 var Version。
VERSION ?= $(shell sed -n 's/^var Version = "\(.*\)"$$/\1/p' internal/version/version.go)
LDFLAGS := -X github.com/cc14514/go-safemedia/internal/version.Version=$(VERSION)

# 桌面外壳（Wails v2）。本机 npm 不可用，故用 -s 跳过前端构建（不需要 Node）。
WAILS   ?= $(shell go env GOPATH)/bin/wails
DESKTOP := desktop/build/bin/safemedia.app
ICONSET := desktop/build/icon.iconset
# 图标源 SVG → PNG 用无头 Chrome（L3 已依赖它）；macOS 无内置 SVG 光栅化器（sips 不支持 SVG）。
CHROME  ?= /Applications/Google Chrome.app/Contents/MacOS/Google Chrome

# 首版分发规格：仅 macOS / arm64，见 specs/002/spec.md 的 Scope。
DIST    := dist
ARCH    := darwin-arm64
PKGNAME := safemedia_$(VERSION)_$(ARCH)
# 发布标签：始终是 v 开头，VERSION 带不带 v 都能用。
TAG     := v$(patsubst v%,%,$(VERSION))

.PHONY: all build test test-l1 test-l2 test-l3 lint vet fmt clean smoke \
        desktop desktop-icon desktop-icon-svg desktop-test desktop-run dist release

all: build

build:
	go build -ldflags "$(LDFLAGS)" -o $(BIN) ./cmd/safemedia

## L1 单元测试（开发者自检门禁）
test-l1:
	go test ./internal/... ./cmd/... ./desktop/...

## L2 集成测试（Feature 完成门禁）
test-l2:
	go test ./tests/integration/...

## L3 E2E（QA 交付门禁，需真实浏览器与样本视频）
##   SAFEMEDIA_E2E_SAMPLES=<dir>  指定真实样本目录（见 tests/e2e/fetch-samples.sh）
##   SAFEMEDIA_E2E_NO_BROWSER=1   跳过无头浏览器解码校验
test-l3:
	go test -count=1 ./tests/e2e/... -v
	@echo "▶ L3 报告已生成：tests/e2e/REPORT.md"

test: test-l1 test-l2

vet:
	go vet $(PKG) ./desktop/...

fmt:
	gofmt -l -w .

lint: vet

smoke: build
	./$(BIN) --help

## 构建桌面外壳 .app（仅 macOS；需要 wails CLI 与 Xcode Command Line Tools）
desktop:
	@command -v $(WAILS) >/dev/null 2>&1 || { \
	  echo "缺少 wails CLI。安装：go install github.com/wailsapp/wails/v2/cmd/wails@v2.16.0"; exit 1; }
	cd desktop && $(WAILS) build -s -ldflags "$(LDFLAGS) -w -s"
	@$(MAKE) --no-print-directory desktop-icon

## 用 sips + iconutil 重做 .icns 并重新自签（Wails 内置的 icns 编码器只写 5 个 @2x 尺寸、
## 且用最近邻算法缩放，小尺寸会糊；iconutil 会写全 10 个尺寸并做高质量缩放）
desktop-icon:
	@command -v iconutil >/dev/null 2>&1 || { echo "缺少 iconutil（Xcode Command Line Tools）"; exit 1; }
	@test -f desktop/build/appicon.png || { echo "缺少 desktop/build/appicon.png（见 make desktop-icon-svg）"; exit 1; }
	@rm -rf $(ICONSET) && mkdir -p $(ICONSET)
	@for s in 16 32 64 128 256 512 1024; do \
	  sips -z $$s $$s desktop/build/appicon.png --out $(ICONSET)/r$$s.png >/dev/null; done
	@cp $(ICONSET)/r16.png   $(ICONSET)/icon_16x16.png
	@cp $(ICONSET)/r32.png   $(ICONSET)/icon_16x16@2x.png
	@cp $(ICONSET)/r32.png   $(ICONSET)/icon_32x32.png
	@cp $(ICONSET)/r64.png   $(ICONSET)/icon_32x32@2x.png
	@cp $(ICONSET)/r128.png  $(ICONSET)/icon_128x128.png
	@cp $(ICONSET)/r256.png  $(ICONSET)/icon_128x128@2x.png
	@cp $(ICONSET)/r256.png  $(ICONSET)/icon_256x256.png
	@cp $(ICONSET)/r512.png  $(ICONSET)/icon_256x256@2x.png
	@cp $(ICONSET)/r512.png  $(ICONSET)/icon_512x512.png
	@cp $(ICONSET)/r1024.png $(ICONSET)/icon_512x512@2x.png
	iconutil -c icns $(ICONSET) -o $(DESKTOP)/Contents/Resources/iconfile.icns
	codesign --force --deep --sign - $(DESKTOP)

## 由 SVG 源渲染出 1024×1024 的 appicon.png（仅在改图标时需要；用无头 Chrome，不引入 Node 依赖）
desktop-icon-svg:
	@test -f desktop/build/appicon.svg || { echo "缺少 desktop/build/appicon.svg"; exit 1; }
	@command -v "$(CHROME)" >/dev/null 2>&1 || { echo "缺少 Chrome：$(CHROME)"; exit 1; }
	rm -f desktop/build/appicon.png
	"$(CHROME)" --headless=new --disable-gpu --hide-scrollbars --force-device-scale-factor=1 \
	  --window-size=1024,1024 --screenshot=desktop/build/appicon.png "file://$(PWD)/desktop/build/appicon.svg"
	@sips -g pixelWidth -g pixelHeight -g hasAlpha desktop/build/appicon.png | tail -3

## 仅跑外壳的单元测试（不需要 wails CLI）
desktop-test:
	go test ./desktop/... ./internal/desktopcfg/...

## 本地直接运行已构建的 .app（调试用）
desktop-run: desktop
	./$(DESKTOP)/Contents/MacOS/safemedia $(ARGS)

## 一条命令产出可分发产物：CLI + 桌面 .app，打成 zip
##   默认版本取自 internal/version/version.go；发布时用 make dist VERSION=0.2.0
dist: desktop
	rm -rf $(DIST)/$(PKGNAME)
	mkdir -p $(DIST)/$(PKGNAME)
	go build -trimpath -ldflags "$(LDFLAGS) -w -s" -o $(DIST)/$(PKGNAME)/safemedia ./cmd/safemedia
	cp -R $(DESKTOP) $(DIST)/$(PKGNAME)/safemedia.app
	printf '%s\n' \
	  'go-safemedia $(VERSION) — $(ARCH)' \
	  '' \
	  '包含：' \
	  '  safemedia.app   桌面外壳（双击运行；打开后在索引页点「选择密档库」即可换扫描目录，选择会被记住）' \
	  '                  （默认库 ~/.safemedia/library，可用 ~/.safemedia/desktop.json 的 library 预设）' \
	  '  safemedia       命令行（封存/建账户/播放）' \
	  '' \
	  '首次使用：' \
	  '  1) 双击 safemedia.app，点「账户管理 → 创建账户」（当场显示助记词，请离线抄写）' \
	  '     —— 也可以终端执行：./safemedia account create' \
	  '  2) 封存视频：点索引页工具栏「封存」→「选择视频文件…／选择文件夹…」（或把视频拖进窗口）' \
	  '     —— 也可以终端执行：./safemedia seal ~/Movies/a.mp4 -o ~/.safemedia/library/a.svid' \
	  '  3) 回到登录页，选对账户并输入口令，解锁后点选播放' \
	  '     （多个账户放在 ~/.safemedia/accounts/ 下会被自动列出；换扫描目录用索引页的「选择密档库」）' \
	  '' \
	  '若双击提示「无法打开，因为它来自身份不明的开发者」：' \
	  '  这是未签名分发的正常现象（本版本不做签名公证）。在终端执行一次：' \
	  '    xattr -dr com.apple.quarantine /Applications/safemedia.app' \
	  '' \
	  '明文只在内存与本机 127.0.0.1 环回中，不落盘。' \
	  > $(DIST)/$(PKGNAME)/说明.txt
	cd $(DIST) && ditto -c -k --norsrc --keepParent $(PKGNAME) $(PKGNAME).zip
	@echo "▶ 产物：$(DIST)/$(PKGNAME).zip"
	@shasum -a 256 $(DIST)/$(PKGNAME).zip
	@du -h $(DIST)/$(PKGNAME).zip

## 发布到 GitHub Release（需 gh 已登录；自动创建/复用 $(TAG) 标签）
release: dist
	@command -v gh >/dev/null 2>&1 || { echo "缺少 gh CLI"; exit 1; }
	gh release view $(TAG) >/dev/null 2>&1 \
	  && gh release upload $(TAG) $(DIST)/$(PKGNAME).zip --clobber \
	  || gh release create $(TAG) $(DIST)/$(PKGNAME).zip \
	       --title "go-safemedia $(TAG)" --generate-notes
	@echo "▶ 已发布 $(TAG)"

clean:
	rm -rf bin $(DIST) desktop/build/bin desktop/build/darwin desktop/build/icon.iconset desktop/frontend/wailsjs