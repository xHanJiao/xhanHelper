// Package imgshrink — 把图片压到「适合屏幕看」的尺寸，供 HTML 产物内联使用。
//
// 为什么需要：产物页按 760px 宽设计、右栏最宽 1200、Retina 到顶也就 2400 物理像素，
// 而用户上传的手机照片动辄 4000×3000 / 3–5MB。原样内联要再付 1.33 倍 base64 膨胀，
// 几张就顶满整页 8MB 上限（见 docs/design/html-artifact §4.3）。
//
// 这里只做「够看」的压缩，不追求无损：按最长边缩放到 `maxEdge`，有透明通道编码成 PNG，
// 否则 JPEG（`quality`）。已经够小的图**原样返回**——重编码只会白白掉画质，
// 且能避开 EXIF 方向的额外处理。
package imgshrink

import (
	"bytes"
	"encoding/binary"
	"errors"
	"fmt"
	"image"
	"image/jpeg"
	"image/png"

	"golang.org/x/image/draw"
	_ "golang.org/x/image/webp" // 注册 webp 解码器

	_ "image/gif" // 注册 gif 解码器（取首帧）
)

const (
	// MaxPixels — 解码后的像素上限。超过直接拒绝：再大就不是"照片"，解出来会吃光内存。
	MaxPixels = 50_000_000
	// passthroughBytes — 够小就原样返回的字节阈值（配合"尺寸也在上限内"一起判断）。
	passthroughBytes = 600 << 10
)

// ErrTooLarge — 像素数超限。
var ErrTooLarge = errors.New("图片像素过多")

// Shrink 把图片压到最长边不超过 maxEdge。
// 返回压缩后的字节与 MIME；原图已足够小则原样返回（此时 MIME 按原始格式给出）。
func Shrink(data []byte, maxEdge, quality int) ([]byte, string, error) {
	if maxEdge <= 0 || quality <= 0 {
		return nil, "", errors.New("缩放参数非法")
	}
	cfg, format, err := image.DecodeConfig(bytes.NewReader(data))
	if err != nil {
		return nil, "", fmt.Errorf("无法识别的图片：%w", err)
	}
	if cfg.Width*cfg.Height > MaxPixels {
		return nil, "", fmt.Errorf("%w（%d×%d）", ErrTooLarge, cfg.Width, cfg.Height)
	}
	if cfg.Width <= maxEdge && cfg.Height <= maxEdge && len(data) <= passthroughBytes {
		return data, mimeOf(format), nil
	}

	img, _, err := image.Decode(bytes.NewReader(data))
	if err != nil {
		return nil, "", fmt.Errorf("解码图片失败：%w", err)
	}
	// EXIF 方向必须先烘焙进像素：Go 标准库解 JPEG/PNG 不会读方向，直接重编码会让竖拍照片躺倒。
	img = applyOrientation(img, exifOrientation(data))
	img = scaleDown(img, maxEdge)

	var buf bytes.Buffer
	if hasAlpha(img) {
		if err := png.Encode(&buf, img); err != nil {
			return nil, "", fmt.Errorf("编码 PNG 失败：%w", err)
		}
		return buf.Bytes(), "image/png", nil
	}
	if err := jpeg.Encode(&buf, img, &jpeg.Options{Quality: quality}); err != nil {
		return nil, "", fmt.Errorf("编码 JPEG 失败：%w", err)
	}
	return buf.Bytes(), "image/jpeg", nil
}

func mimeOf(format string) string {
	switch format {
	case "png":
		return "image/png"
	case "gif":
		return "image/gif"
	case "webp":
		return "image/webp"
	default:
		return "image/jpeg"
	}
}

// scaleDown — 最长边超过 maxEdge 才缩，等比。用 CatmullRom：标准库只有最近邻，缩照片会糊。
func scaleDown(src image.Image, maxEdge int) image.Image {
	b := src.Bounds()
	w, h := b.Dx(), b.Dy()
	if w <= maxEdge && h <= maxEdge {
		return src
	}
	nw, nh := w, h
	if w >= h {
		nw = maxEdge
		nh = h * maxEdge / w
	} else {
		nh = maxEdge
		nw = w * maxEdge / h
	}
	if nw < 1 {
		nw = 1
	}
	if nh < 1 {
		nh = 1
	}
	dst := image.NewRGBA(image.Rect(0, 0, nw, nh))
	draw.CatmullRom.Scale(dst, dst.Bounds(), src, b, draw.Over, nil)
	return dst
}

// hasAlpha — 任一像素半透明即视为需要 PNG。只扫已解码的目标图（最大 1600 边，代价可忽略）。
func hasAlpha(img image.Image) bool {
	switch img.(type) {
	case *image.YCbCr, *image.Gray, *image.Gray16:
		return false // JPEG / 灰度解出来天然不透明，省一遍全图扫描
	}
	b := img.Bounds()
	for y := b.Min.Y; y < b.Max.Y; y++ {
		for x := b.Min.X; x < b.Max.X; x++ {
			if _, _, _, a := img.At(x, y).RGBA(); a < 0xFFFF {
				return true
			}
		}
	}
	return false
}

// applyOrientation — 按 EXIF Orientation（1–8）烘焙旋转/翻转。
func applyOrientation(src image.Image, o int) image.Image {
	if o <= 1 || o > 8 {
		return src
	}
	b := src.Bounds()
	w, h := b.Dx(), b.Dy()
	swap := o >= 5
	dw, dh := w, h
	if swap {
		dw, dh = h, w
	}
	dst := image.NewRGBA(image.Rect(0, 0, dw, dh))
	for y := 0; y < h; y++ {
		for x := 0; x < w; x++ {
			var dx, dy int
			switch o {
			case 2: // 水平翻转
				dx, dy = w-1-x, y
			case 3: // 旋转 180
				dx, dy = w-1-x, h-1-y
			case 4: // 垂直翻转
				dx, dy = x, h-1-y
			case 5: // 转置
				dx, dy = y, x
			case 6: // 顺时针 90（手机竖拍最常见）
				dx, dy = h-1-y, x
			case 7: // 反转置
				dx, dy = h-1-y, w-1-x
			case 8: // 逆时针 90
				dx, dy = y, w-1-x
			}
			dst.Set(dx, dy, src.At(b.Min.X+x, b.Min.Y+y))
		}
	}
	return dst
}

// exifOrientation — 从 JPEG 的 APP1/Exif 里读 IFD0 的 0x0112。读不到按 1（正常）处理。
func exifOrientation(data []byte) int {
	if len(data) < 4 || data[0] != 0xFF || data[1] != 0xD8 {
		return 1
	}
	i := 2
	for i+4 <= len(data) {
		if data[i] != 0xFF {
			return 1
		}
		marker := data[i+1]
		// 独立标记（无长度字段）
		if marker == 0x01 || (marker >= 0xD0 && marker <= 0xD8) {
			i += 2
			continue
		}
		if marker == 0xDA { // SOS：后面是压缩数据，没有更多元数据了
			return 1
		}
		segLen := int(binary.BigEndian.Uint16(data[i+2 : i+4]))
		if segLen < 2 || i+2+segLen > len(data) {
			return 1
		}
		seg := data[i+4 : i+2+segLen]
		if marker == 0xE1 && len(seg) > 6 && string(seg[:4]) == "Exif" && seg[4] == 0 && seg[5] == 0 {
			return parseTIFFOrientation(seg[6:])
		}
		i += 2 + segLen
	}
	return 1
}

func parseTIFFOrientation(t []byte) int {
	if len(t) < 8 {
		return 1
	}
	var bo binary.ByteOrder
	switch {
	case t[0] == 'I' && t[1] == 'I':
		bo = binary.LittleEndian
	case t[0] == 'M' && t[1] == 'M':
		bo = binary.BigEndian
	default:
		return 1
	}
	if bo.Uint16(t[2:4]) != 42 {
		return 1
	}
	off := int(bo.Uint32(t[4:8]))
	if off < 0 || off+2 > len(t) {
		return 1
	}
	n := int(bo.Uint16(t[off : off+2]))
	const entryLen = 12
	for k := 0; k < n; k++ {
		p := off + 2 + k*entryLen
		if p+entryLen > len(t) {
			return 1
		}
		if bo.Uint16(t[p:p+2]) != 0x0112 {
			continue
		}
		v := int(bo.Uint16(t[p+8 : p+10]))
		if v >= 1 && v <= 8 {
			return v
		}
		return 1
	}
	return 1
}
