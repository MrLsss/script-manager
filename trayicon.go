package main

import (
	"bytes"
	"image"
	"image/color"
	"image/png"
)

// trayIconPNG draws the menu bar icon: a rounded square with a prompt.
func trayIconPNG() []byte {
	const size = 32
	img := image.NewRGBA(image.Rect(0, 0, size, size))
	black := color.RGBA{0, 0, 0, 255}
	set := func(x, y int) {
		if x >= 0 && y >= 0 && x < size && y < size {
			img.Set(x, y, black)
		}
	}
	inside := func(x, y int) bool {
		const r = 7
		if x < 3 || y < 3 || x > size-4 || y > size-4 {
			return false
		}
		cx, cy := 3+r, 3+r
		if x < cx && y < cy {
			dx, dy := cx-x, cy-y
			return dx*dx+dy*dy <= r*r
		}
		cx, cy = size-4-r, 3+r
		if x > cx && y < cy {
			dx, dy := x-cx, cy-y
			return dx*dx+dy*dy <= r*r
		}
		cx, cy = 3+r, size-4-r
		if x < cx && y > cy {
			dx, dy := cx-x, y-cy
			return dx*dx+dy*dy <= r*r
		}
		cx, cy = size-4-r, size-4-r
		if x > cx && y > cy {
			dx, dy := x-cx, y-cy
			return dx*dx+dy*dy <= r*r
		}
		return true
	}
	for y := 0; y < size; y++ {
		for x := 0; x < size; x++ {
			if !inside(x, y) {
				continue
			}
			edge := !inside(x-2, y) || !inside(x+2, y) || !inside(x, y-2) || !inside(x, y+2)
			if edge {
				set(x, y)
			}
		}
	}
	line := func(x0, y0, x1, y1 int) {
		dx, dy := x1-x0, y1-y0
		steps := dx
		if dy > steps {
			steps = dy
		}
		if steps < 0 {
			steps = -steps
		}
		for i := 0; i <= steps; i++ {
			x := x0 + dx*i/maxInt(steps, 1)
			y := y0 + dy*i/maxInt(steps, 1)
			for ox := 0; ox < 2; ox++ {
				for oy := 0; oy < 2; oy++ {
					set(x+ox, y+oy)
				}
			}
		}
	}
	line(10, 10, 16, 16)
	line(16, 16, 10, 22)
	line(18, 22, 23, 22)
	var buf bytes.Buffer
	_ = png.Encode(&buf, img)
	return buf.Bytes()
}

func maxInt(a, b int) int {
	if a > b {
		return a
	}
	return b
}
