//go:build ignore

package main

import (
	"image"
	"image/color"
	"image/png"
	"math"
	"os"
)

const size = 1024
const scale = 3

func main() {
	img := render()
	if err := os.MkdirAll("resources", 0o755); err != nil {
		panic(err)
	}
	f, err := os.Create("resources/icon.png")
	if err != nil {
		panic(err)
	}
	defer f.Close()
	if err := png.Encode(f, img); err != nil {
		panic(err)
	}
}

func render() *image.RGBA {
	w := size * scale
	big := image.NewRGBA(image.Rect(0, 0, w, w))
	bg := color.RGBA{0x17, 0x18, 0x1c, 0xff}
	accent := color.RGBA{0x7e, 0xe2, 0xa8, 0xff}
	muted := color.RGBA{0x4a, 0x4d, 0x57, 0xff}

	inset := 72.0 * scale
	radius := 200.0 * scale
	for y := 0; y < w; y++ {
		for x := 0; x < w; x++ {
			fx, fy := float64(x)+0.5, float64(y)+0.5
			if insideRoundRect(fx, fy, inset, inset, float64(w)-inset, float64(w)-inset, radius) {
				big.SetRGBA(x, y, bg)
			}
		}
	}

	thick := 74.0 * scale
	// Prompt chevron.
	segments := [][4]float64{
		{352 * scale, 336 * scale, 508 * scale, 512 * scale},
		{508 * scale, 512 * scale, 352 * scale, 688 * scale},
		{566 * scale, 688 * scale, 700 * scale, 688 * scale},
	}
	for y := 0; y < w; y++ {
		for x := 0; x < w; x++ {
			fx, fy := float64(x)+0.5, float64(y)+0.5
			for i, s := range segments {
				if distToSegment(fx, fy, s[0], s[1], s[2], s[3]) <= thick/2 {
					c := accent
					if i == 2 {
						c = muted
					}
					big.SetRGBA(x, y, c)
				}
			}
		}
	}

	img := image.NewRGBA(image.Rect(0, 0, size, size))
	for y := 0; y < size; y++ {
		for x := 0; x < size; x++ {
			var r, g, b, a float64
			for dy := 0; dy < scale; dy++ {
				for dx := 0; dx < scale; dx++ {
					c := big.RGBAAt(x*scale+dx, y*scale+dy)
					af := float64(c.A) / 255
					r += float64(c.R) * af
					g += float64(c.G) * af
					b += float64(c.B) * af
					a += af
				}
			}
			n := float64(scale * scale)
			if a == 0 {
				continue
			}
			img.SetRGBA(x, y, color.RGBA{
				R: uint8(r / a),
				G: uint8(g / a),
				B: uint8(b / a),
				A: uint8(a / n * 255),
			})
		}
	}
	return img
}

func insideRoundRect(x, y, x0, y0, x1, y1, r float64) bool {
	if x < x0 || x > x1 || y < y0 || y > y1 {
		return false
	}
	cx := math.Min(math.Max(x, x0+r), x1-r)
	cy := math.Min(math.Max(y, y0+r), y1-r)
	dx, dy := x-cx, y-cy
	return dx*dx+dy*dy <= r*r
}

func distToSegment(px, py, x0, y0, x1, y1 float64) float64 {
	dx, dy := x1-x0, y1-y0
	if dx == 0 && dy == 0 {
		return math.Hypot(px-x0, py-y0)
	}
	t := ((px-x0)*dx + (py-y0)*dy) / (dx*dx + dy*dy)
	t = math.Min(1, math.Max(0, t))
	return math.Hypot(px-(x0+t*dx), py-(y0+t*dy))
}
