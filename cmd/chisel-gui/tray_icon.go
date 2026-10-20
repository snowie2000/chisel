package main

import (
	"bytes"
	"encoding/binary"
	"image"
	"image/color"
	"image/png"
)

// Match the interface's green mark. Generate the small code-native icon without
// external image assets; Windows expects ICO while other desktops accept PNG.
func trayIcon(ico bool) []byte {
	img := image.NewNRGBA(image.Rect(0, 0, 32, 32))
	green, dark := color.NRGBA{198, 238, 151, 255}, color.NRGBA{33, 49, 23, 255}
	for y := 2; y < 30; y++ {
		for x := 2; x < 30; x++ {
			if (x < 5 || x > 26) && (y < 5 || y > 26) {
				continue
			}
			img.SetNRGBA(x, y, green)
		}
	}
	for y := 8; y < 15; y++ {
		for x := 7; x < 26; x++ {
			if (y >= 10 && y <= 12 && x <= 22) || (x >= 19 && x <= 25 && abs(y-11) <= 25-x) {
				img.SetNRGBA(x, y, dark)
			}
		}
	}
	for y := 17; y < 24; y++ {
		for x := 6; x < 25; x++ {
			if (y >= 19 && y <= 21 && x >= 9) || (x <= 12 && abs(y-20) <= x-6) {
				img.SetNRGBA(x, y, dark)
			}
		}
	}
	var data bytes.Buffer
	_ = png.Encode(&data, img)
	if !ico {
		return data.Bytes()
	}
	var out bytes.Buffer
	for _, value := range []uint16{0, 1, 1} {
		_ = binary.Write(&out, binary.LittleEndian, value)
	}
	out.Write([]byte{32, 32, 0, 0})
	for _, value := range []uint16{1, 32} {
		_ = binary.Write(&out, binary.LittleEndian, value)
	}
	_ = binary.Write(&out, binary.LittleEndian, uint32(data.Len()))
	_ = binary.Write(&out, binary.LittleEndian, uint32(22))
	out.Write(data.Bytes())
	return out.Bytes()
}

func abs(n int) int {
	if n < 0 {
		return -n
	}
	return n
}
