// icongen 把品牌图标缩成 hicolor 主题要求的那一套尺寸。
//
// 为什么需要它：麒麟的包规范检查（common-standards-22）要求 Icon 字段对应的图标
// 要么是 /usr/share/icons/hicolor/scalable/apps/ 下的 svg，要么是 16/24/32/48/64/
// 96/128/256/512 **九种尺寸全给齐**的 png。只放一张 128 的会被判警告，表现是开始
// 菜单、任务栏、桌面快捷方式三处可能有一处显示不出图标。
//
// 为什么自己写而不调 ImageMagick：麒麟 V10 那条链路在 ubuntu:20.04 容器里编，容器里
// 只保证有 Go。缩放用面积平均（box filter）——源图是 1024 见方，缩到这九档都是大比例
// 缩小，面积平均比最近邻干净得多，也不必引第三方图像库。
package main

import (
	"flag"
	"fmt"
	"image"
	"image/draw"
	"image/png"
	"os"
	"path/filepath"
)

// hicolor 主题要求的九档。少一档就是一条警告。
var sizes = []int{16, 24, 32, 48, 64, 96, 128, 256, 512}

func main() {
	src := flag.String("src", "build/appicon.png", "源图标（png，建议 512 见方以上）")
	out := flag.String("out", "build/linux/icons", "输出根目录，下面按 <尺寸>x<尺寸>/apps/ 摆放")
	name := flag.String("name", "", "图标文件名（不含扩展名），须与 .desktop 的 Icon 字段一致")
	flag.Parse()

	if *name == "" {
		fatal(fmt.Errorf("-name 不能为空：它必须和 .desktop 里 Icon= 的值一模一样"))
	}
	img, err := load(*src)
	if err != nil {
		fatal(err)
	}
	for _, s := range sizes {
		dir := filepath.Join(*out, fmt.Sprintf("%dx%d", s, s), "apps")
		if err := os.MkdirAll(dir, 0o755); err != nil {
			fatal(err)
		}
		if err := save(filepath.Join(dir, *name+".png"), scale(img, s)); err != nil {
			fatal(err)
		}
	}
	fmt.Printf("▶ 已生成 %d 档图标到 %s\n", len(sizes), *out)
}

func load(path string) (*image.RGBA, error) {
	f, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	defer f.Close()
	decoded, err := png.Decode(f)
	if err != nil {
		return nil, fmt.Errorf("%s: %w", path, err)
	}
	// 统一转成预乘 alpha 的 RGBA：下面按通道求平均，只有在预乘空间里平均才不会在
	// 半透明边缘上拖出一圈黑边。
	b := decoded.Bounds()
	img := image.NewRGBA(image.Rect(0, 0, b.Dx(), b.Dy()))
	draw.Draw(img, img.Bounds(), decoded, b.Min, draw.Src)
	return img, nil
}

// scale 用面积平均把 src 缩到 size 见方。
func scale(src *image.RGBA, size int) *image.RGBA {
	sw, sh := src.Bounds().Dx(), src.Bounds().Dy()
	dst := image.NewRGBA(image.Rect(0, 0, size, size))
	for y := 0; y < size; y++ {
		y0, y1 := y*sh/size, (y+1)*sh/size
		if y1 == y0 {
			y1++
		}
		for x := 0; x < size; x++ {
			x0, x1 := x*sw/size, (x+1)*sw/size
			if x1 == x0 {
				x1++
			}
			var r, g, b, a, n uint32
			for sy := y0; sy < y1 && sy < sh; sy++ {
				for sx := x0; sx < x1 && sx < sw; sx++ {
					i := src.PixOffset(sx, sy)
					r += uint32(src.Pix[i])
					g += uint32(src.Pix[i+1])
					b += uint32(src.Pix[i+2])
					a += uint32(src.Pix[i+3])
					n++
				}
			}
			i := dst.PixOffset(x, y)
			dst.Pix[i] = uint8(r / n)
			dst.Pix[i+1] = uint8(g / n)
			dst.Pix[i+2] = uint8(b / n)
			dst.Pix[i+3] = uint8(a / n)
		}
	}
	return dst
}

func save(path string, img image.Image) error {
	f, err := os.Create(path)
	if err != nil {
		return err
	}
	defer f.Close()
	if err := png.Encode(f, img); err != nil {
		return err
	}
	return f.Close()
}

func fatal(err error) {
	fmt.Fprintln(os.Stderr, "icongen:", err)
	os.Exit(1)
}
