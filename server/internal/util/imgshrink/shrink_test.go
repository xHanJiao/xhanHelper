package imgshrink

import (
	"bytes"
	"encoding/binary"
	"hash/crc32"
	"image"
	"image/color"
	"image/jpeg"
	"image/png"
	"testing"
)

func encodeJPEG(t *testing.T, w, h int) []byte {
	t.Helper()
	img := image.NewRGBA(image.Rect(0, 0, w, h))
	for y := 0; y < h; y++ {
		for x := 0; x < w; x++ {
			img.Set(x, y, color.RGBA{R: uint8(x % 256), G: uint8(y % 256), B: 128, A: 255})
		}
	}
	var buf bytes.Buffer
	if err := jpeg.Encode(&buf, img, &jpeg.Options{Quality: 90}); err != nil {
		t.Fatalf("encode jpeg: %v", err)
	}
	return buf.Bytes()
}

// withExifOrientation — 在 JPEG 的 SOI 之后插入一个只含 Orientation 的 APP1/Exif 段。
// 这样不必依赖真实相机样片，也能钉住「读方向 + 烘焙」这条链。
func withExifOrientation(jpg []byte, orientation uint16) []byte {
	// TIFF: II + 42 + IFD0 offset(8) + 1 entry + next IFD(0)
	tiff := make([]byte, 8+2+12+4)
	tiff[0], tiff[1] = 'I', 'I'
	binary.LittleEndian.PutUint16(tiff[2:4], 42)
	binary.LittleEndian.PutUint32(tiff[4:8], 8)
	binary.LittleEndian.PutUint16(tiff[8:10], 1) // 1 个 entry
	p := 10
	binary.LittleEndian.PutUint16(tiff[p:p+2], 0x0112) // tag: Orientation
	binary.LittleEndian.PutUint16(tiff[p+2:p+4], 3)    // type: SHORT
	binary.LittleEndian.PutUint32(tiff[p+4:p+8], 1)    // count
	binary.LittleEndian.PutUint16(tiff[p+8:p+10], orientation)

	payload := append([]byte("Exif\x00\x00"), tiff...)
	seg := make([]byte, 4+len(payload))
	seg[0], seg[1] = 0xFF, 0xE1
	binary.BigEndian.PutUint16(seg[2:4], uint16(2+len(payload)))
	copy(seg[4:], payload)

	out := make([]byte, 0, len(jpg)+len(seg))
	out = append(out, jpg[:2]...) // SOI
	out = append(out, seg...)
	out = append(out, jpg[2:]...)
	return out
}

func decodeDims(t *testing.T, data []byte) (int, int, string) {
	t.Helper()
	img, format, err := image.Decode(bytes.NewReader(data))
	if err != nil {
		t.Fatalf("decode result: %v", err)
	}
	b := img.Bounds()
	return b.Dx(), b.Dy(), format
}

func TestSmallImagePassthrough(t *testing.T) {
	img := image.NewRGBA(image.Rect(0, 0, 80, 60))
	var buf bytes.Buffer
	if err := png.Encode(&buf, img); err != nil {
		t.Fatalf("encode: %v", err)
	}
	out, mime, err := Shrink(buf.Bytes(), 1600, 85)
	if err != nil {
		t.Fatalf("Shrink: %v", err)
	}
	if !bytes.Equal(out, buf.Bytes()) {
		t.Fatal("够小的图应原样返回（重编码只会掉画质）")
	}
	if mime != "image/png" {
		t.Fatalf("mime 应为 image/png，实际 %s", mime)
	}
}

func TestLargeJPEGDownscaled(t *testing.T) {
	jpg := encodeJPEG(t, 3000, 2000)
	out, mime, err := Shrink(jpg, 1600, 85)
	if err != nil {
		t.Fatalf("Shrink: %v", err)
	}
	if mime != "image/jpeg" {
		t.Fatalf("不透明图应编码成 JPEG，实际 %s", mime)
	}
	w, h, _ := decodeDims(t, out)
	if w != 1600 || h != 1066 {
		t.Fatalf("应等比缩到 1600×1066，实际 %d×%d", w, h)
	}
	if len(out) >= len(jpg) {
		t.Fatalf("压缩后应更小：%d -> %d", len(jpg), len(out))
	}
}

func TestAlphaKeepsPNG(t *testing.T) {
	img := image.NewRGBA(image.Rect(0, 0, 2000, 1000))
	for y := 0; y < 1000; y++ {
		for x := 0; x < 2000; x++ {
			img.Set(x, y, color.RGBA{R: 255, A: uint8(x % 200)}) // 半透明
		}
	}
	var buf bytes.Buffer
	if err := png.Encode(&buf, img); err != nil {
		t.Fatalf("encode: %v", err)
	}
	out, mime, err := Shrink(buf.Bytes(), 1600, 85)
	if err != nil {
		t.Fatalf("Shrink: %v", err)
	}
	if mime != "image/png" {
		t.Fatalf("带透明的图必须留 PNG，实际 %s", mime)
	}
	decoded, err := png.Decode(bytes.NewReader(out))
	if err != nil {
		t.Fatalf("decode png: %v", err)
	}
	if _, _, _, a := decoded.At(10, 10).RGBA(); a >= 0xFFFF {
		t.Fatal("透明度应保留")
	}
}

// 手机竖拍（Orientation=6）必须把像素转正：否则重编码后照片会躺倒。
func TestExifOrientationBakedIn(t *testing.T) {
	jpg := withExifOrientation(encodeJPEG(t, 3000, 2000), 6)
	if got := exifOrientation(jpg); got != 6 {
		t.Fatalf("应读出 orientation=6，实际 %d", got)
	}
	out, _, err := Shrink(jpg, 1600, 85)
	if err != nil {
		t.Fatalf("Shrink: %v", err)
	}
	w, h, _ := decodeDims(t, out)
	// 原图 3000×2000 → 转正成 2000×3000 → 等比缩到 1066×1600
	if w != 1066 || h != 1600 {
		t.Fatalf("转正后应为 1066×1600，实际 %d×%d（宽高没互换＝躺倒了）", w, h)
	}
}

func TestPixelCap(t *testing.T) {
	// 手搓一张「超像素」的 PNG 头：只跑 DecodeConfig，不会真的分配 60MP 内存。
	img := image.NewRGBA(image.Rect(0, 0, 2, 2))
	var buf bytes.Buffer
	if err := png.Encode(&buf, img); err != nil {
		t.Fatalf("encode: %v", err)
	}
	data := buf.Bytes()
	// IHDR 紧跟在 8 字节签名之后：4 字节长度 + "IHDR" + width(4) + height(4) + ...
	ihdr := 8 + 4 + 4
	binary.BigEndian.PutUint32(data[ihdr:ihdr+4], 10000)
	binary.BigEndian.PutUint32(data[ihdr+4:ihdr+8], 6000)
	crc := crc32.ChecksumIEEE(data[ihdr-4 : ihdr+13])
	binary.BigEndian.PutUint32(data[ihdr+13:ihdr+17], crc)

	if _, _, err := Shrink(data, 1600, 85); err == nil {
		t.Fatal("超过像素上限应报错")
	}
}

func TestGarbageRejected(t *testing.T) {
	if _, _, err := Shrink([]byte("not an image"), 1600, 85); err == nil {
		t.Fatal("非图片应报错")
	}
}
