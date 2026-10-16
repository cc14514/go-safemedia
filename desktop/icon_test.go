package main

import (
	"os"
	"testing"

	"image"
	_ "image/png"
)

// 应用图标（US5 分发产物的一部分）：Wails 要求 desktop/build/appicon.png 存在，
// 空缺时它会静默使用自带的默认图标（深灰底 + 白 W），用户看到的就是「没做完的 app」。
//
// 这里只断言「源在、且不是默认图标」，不断言具体像素（改设计不该弄红测试）：
//   - 尺寸 1024×1024、四角透明（macOS 图标必须自带形状，不能是满幅方块）；
//   - 有彩色（默认图标是纯灰阶，最大最小通道差为 0）；
//   - 有翠绿区域（盾牌）与深靛蓝区域（底板），避免被换成任意一张灰图。
func TestAppIconIsSourceAndNotWailsDefault(t *testing.T) {
	const png = "build/appicon.png"
	f, err := os.Open(png)
	if err != nil {
		t.Fatalf("缺少图标源 %s（%v）；用 `make desktop-icon-svg` 由 build/appicon.svg 重新渲染", png, err)
	}
	defer f.Close()

	img, _, err := image.Decode(f)
	if err != nil {
		t.Fatalf("图标 %s 不是可解码的 PNG: %v", png, err)
	}
	b := img.Bounds()
	if b.Dx() != 1024 || b.Dy() != 1024 {
		t.Fatalf("图标应为 1024x1024（Wails 会按最大尺寸取用），实际 %dx%d", b.Dx(), b.Dy())
	}

	corner := img.At(b.Min.X+8, b.Min.Y+8)
	if _, _, _, a := corner.RGBA(); a>>8 != 0 {
		t.Errorf("四角必须透明（否则 macOS 会把它当满幅方块），(8,8) 的 alpha=%d", a>>8)
	}

	var colorful, green, indigo int
	// 采样步长 4px：1024×1024 全量扫太慢，采样足够判定颜色分布。
	for y := b.Min.Y; y < b.Max.Y; y += 4 {
		for x := b.Min.X; x < b.Max.X; x += 4 {
			r, g, bl, a := img.At(x, y).RGBA()
			if a>>8 < 200 {
				continue
			}
			r8, g8, b8 := int(r>>8), int(g>>8), int(bl>>8)
			max, min := r8, r8
			for _, v := range []int{g8, b8} {
				if v > max {
					max = v
				}
				if v < min {
					min = v
				}
			}
			if max-min > 24 {
				colorful++
			}
			if g8 > r8+40 && g8 > b8+24 {
				green++
			}
			if b8 > r8+24 && b8 > 60 {
				indigo++
			}
		}
	}
	if colorful == 0 {
		t.Fatal("图标没有任何彩色像素 —— 像 Wails 的默认图标（灰阶），不是本项目的盾牌标识")
	}
	// 采样总数约 65536；盾牌约占 1/3 面积、底板其余，阈值取保守下限。
	if green < 2000 {
		t.Errorf("翠绿（盾牌）像素过少：%d，图标可能被换成别的图", green)
	}
	if indigo < 2000 {
		t.Errorf("深靛蓝（底板）像素过少：%d，图标可能被换成别的图", indigo)
	}
}

// 图标 SVG 是源文件（可手改、可重渲染），必须与 PNG 一起入库 —— 否则全新 clone
// 只能得到 Wails 默认图标（同 desktop/frontend/dist/index.html 那次教训）。
func TestAppIconSVGIsTracked(t *testing.T) {
	if _, err := os.Stat("build/appicon.svg"); err != nil {
		t.Fatalf("缺少图标源 SVG build/appicon.svg（%v）；它必须入库，且不能被 .gitignore 吞掉", err)
	}
}
