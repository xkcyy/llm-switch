package tray

import (
	"encoding/binary"
	"image/color"
	"math"
)

// Icon 生成一个 32x32 的 ICO 图标（青蓝渐变圆角方块 + 白色开关标记）。
func Icon() []byte {
	const size = 32
	pixels := make([]byte, size*size*4)
	c1 := color.RGBA{R: 0x22, G: 0xd3, B: 0xee, A: 0xff} // 青
	c2 := color.RGBA{R: 0x25, G: 0x63, B: 0xeb, A: 0xff} // 蓝
	inSwitch := func(x, y int) bool {
		// 横向开关：左右两个圆点 + 中间横条
		cy := 16
		if math.Hypot(float64(x-10), float64(y-cy)) <= 3.5 {
			return true
		}
		if math.Hypot(float64(x-22), float64(y-cy)) <= 3.5 {
			return true
		}
		return y >= cy-1 && y <= cy+1 && x >= 10 && x <= 22
	}
	rounded := func(x, y int) bool {
		const r = 6.0
		fx, fy := float64(x), float64(y)
		cx := math.Min(math.Max(fx, r), size-1-r)
		cy := math.Min(math.Max(fy, r), size-1-r)
		return math.Hypot(fx-cx, fy-cy) <= r+0.5
	}
	for y := 0; y < size; y++ {
		for x := 0; x < size; x++ {
			i := (y*size + x) * 4
			if !rounded(x, y) {
				continue
			}
			t := float64(x+y) / float64(2*(size-1))
			base := color.RGBA{
				R: uint8(float64(c1.R)*(1-t) + float64(c2.R)*t),
				G: uint8(float64(c1.G)*(1-t) + float64(c2.G)*t),
				B: uint8(float64(c1.B)*(1-t) + float64(c2.B)*t),
				A: 0xff,
			}
			if inSwitch(x, y) {
				base = color.RGBA{R: 0xf8, G: 0xfa, B: 0xfc, A: 0xff}
			}
			pixels[i] = base.B
			pixels[i+1] = base.G
			pixels[i+2] = base.R
			pixels[i+3] = 0xff
		}
	}
	// 行序自下而上
	flipped := make([]byte, len(pixels))
	for y := 0; y < size; y++ {
		copy(flipped[y*size*4:(y+1)*size*4], pixels[(size-1-y)*size*4:(size-y)*size*4])
	}
	// AND mask：32 行 × 4 字节，全部 0（不透明区域由 alpha 控制）
	mask := make([]byte, size*4)
	header := make([]byte, 40)
	binary.LittleEndian.PutUint32(header[0:], 40)
	binary.LittleEndian.PutUint32(header[4:], size)
	binary.LittleEndian.PutUint32(header[8:], size*2)
	binary.LittleEndian.PutUint16(header[12:], 1)
	binary.LittleEndian.PutUint16(header[14:], 32)
	image := append(header, flipped...)
	image = append(image, mask...)
	out := make([]byte, 22)
	binary.LittleEndian.PutUint16(out[2:], 1) // type icon
	binary.LittleEndian.PutUint16(out[4:], 1) // count
	out[6] = size
	out[7] = size
	binary.LittleEndian.PutUint16(out[10:], 1)  // planes
	binary.LittleEndian.PutUint16(out[12:], 32) // bpp
	binary.LittleEndian.PutUint32(out[14:], uint32(len(image)))
	binary.LittleEndian.PutUint32(out[18:], 22)
	return append(out, image...)
}
