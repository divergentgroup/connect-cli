// Command logoart turns a screenshot of the Connect logo (on black) into coloured braille art
// for the post-login page. Regenerate internal/auth/logo.html with:
//
//	go run ./scripts/logoart logo.png 90 > internal/auth/logo.html
package main

import (
	"fmt"
	"html"
	"image/png"
	"os"
	"strconv"
	"strings"
)

func main() {
	f, err := os.Open(os.Args[1])
	if err != nil {
		panic(err)
	}
	img, err := png.Decode(f)
	if err != nil {
		panic(err)
	}
	cols, _ := strconv.Atoi(os.Args[2])
	b := img.Bounds()
	dot := float64(b.Dx()) / float64(cols*2)
	rows := int(float64(b.Dy())/(dot*4) + 0.999)
	// sample returns whether the dot is lit and its average colour.
	sample := func(dx, dy int) (bool, [3]int) {
		x0, y0, x1, y1 := int(float64(dx)*dot), int(float64(dy)*dot), int(float64(dx+1)*dot), int(float64(dy+1)*dot)
		var s [3]int
		n := 0
		for y := y0; y < y1 && y < b.Dy(); y++ {
			for x := x0; x < x1 && x < b.Dx(); x++ {
				r, g, bl, _ := img.At(b.Min.X+x, b.Min.Y+y).RGBA()
				s[0] += int(r >> 8)
				s[1] += int(g >> 8)
				s[2] += int(bl >> 8)
				n++
			}
		}
		if n == 0 {
			return false, s
		}
		for i := range s {
			s[i] /= n
		}
		return max(s[0], s[1], s[2]) > 50, s
	}
	class := func(c [3]int) string {
		grey := func(a, b int) bool { d := a - b; return d < 30 && d > -30 }
		switch {
		case grey(c[0], c[1]) && grey(c[1], c[2]):
			return "w" // wordmark, including its anti-aliased grey edges
		case c[1] > 150 && c[0] < 110:
			return "c" // cyan blob
		case c[2] > 90 && c[1] < 110:
			return "b" // dark blue circle
		}
		return "o" // overlap
	}
	bits := [4][2]int{{1, 8}, {2, 16}, {4, 32}, {64, 128}}
	for r := 0; r < rows; r++ {
		var line strings.Builder
		cur := ""
		for c := 0; c < cols; c++ {
			v, n := 0, 0
			var acc [3]int
			for dy := 0; dy < 4; dy++ {
				for dx := 0; dx < 2; dx++ {
					if lit, col := sample(c*2+dx, r*4+dy); lit {
						v |= bits[dy][dx]
						for i := range acc {
							acc[i] += col[i]
						}
						n++
					}
				}
			}
			cls := ""
			if n > 0 {
				for i := range acc {
					acc[i] /= n
				}
				cls = class(acc)
			}
			if cls != cur {
				if cur != "" {
					line.WriteString("</b>")
				}
				if cls != "" {
					fmt.Fprintf(&line, `<b class="%s">`, cls)
				}
				cur = cls
			}
			line.WriteString(html.EscapeString(string(rune(0x2800 + v))))
		}
		if cur != "" {
			line.WriteString("</b>")
		}
		fmt.Println(line.String())
	}
}
