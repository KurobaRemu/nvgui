package render

import (
	"math"

	"github.com/egoist/mygo/ui"

	"nvgui/renderlist"
)

// Box drawing, block, sextant and Powerline generation is derived from
// github.com/egoist/mygo/plugins/terminal/box.go, MIT, commit
// 400ba3b17c3ec60ef9b0604e26aa4cdcf2a2ff2d; its box-arm and quadrant
// patterns derive from Ghostty, MIT, commit
// befcdfd2c3a1cb24d9ec886e93c95b2b5daa7028.

const (
	armNone = iota
	armLight
	armHeavy
	armDouble
)

var boxArms = [128]string{
	"0101", "0202", "1010", "2020", "", "", "", "", "", "", "", "", "0110", "0210", "0120", "0220",
	"0011", "0012", "0021", "0022", "1100", "1200", "2100", "2200", "1001", "1002", "2001", "2002", "1110", "1210", "2110", "1120",
	"2120", "2210", "1220", "2220", "1011", "1012", "2011", "1021", "2021", "2012", "1022", "2022", "0111", "0112", "0211", "0212",
	"0121", "0122", "0221", "0222", "1101", "1102", "1201", "1202", "2101", "2102", "2201", "2202", "1111", "1112", "1211", "1212",
	"2111", "1121", "2121", "2112", "2211", "1122", "1221", "2212", "1222", "2122", "2221", "2222", "", "", "", "",
	"0303", "3030", "0310", "0130", "0330", "0013", "0031", "0033", "1300", "3100", "3300", "1003", "3001", "3003", "1310", "3130",
	"3330", "1013", "3031", "3033", "0313", "0131", "0333", "1303", "3101", "3303", "1313", "3131", "3333", "", "", "",
	"", "", "", "", "0001", "1000", "0100", "0010", "0002", "2000", "0200", "0020", "0201", "1020", "0102", "2010",
}

var blockQuadrants = [...]struct {
	rune      rune
	quadrants string
}{
	{0x2596, "0010"}, {0x2597, "0001"}, {0x2598, "1000"}, {0x2599, "1011"}, {0x259a, "1001"},
	{0x259b, "1110"}, {0x259c, "1101"}, {0x259d, "0100"}, {0x259e, "0110"}, {0x259f, "0111"},
}

func quadrantPattern(character rune) string {
	for _, entry := range blockQuadrants {
		if entry.rune == character {
			return entry.quadrants
		}
	}
	return ""
}

func drawBoxRune(p *ui.Painter, cmd renderlist.Command, fg ui.Color) {
	if p == nil {
		return
	}
	character, ok := decodeRune(cmd.Rune)
	if !ok {
		return
	}
	rect := commandRect(cmd)
	if rect.W <= 0 || rect.H <= 0 {
		return
	}
	if !drawnBoxRune(character) {
		font := commandFont(cmd)
		p.Glyphs(ui.Shape(cmd.Rune, font), rect.X, rect.Y+font.Metrics().Ascent, fg)
		return
	}
	drawBox(p, character, rect, fg)
}

func drawnBoxRune(character rune) bool {
	switch {
	case 0x2500 <= character && character <= 0x257f:
		return boxArms[character-0x2500] != "" || 0x256d <= character && character <= 0x2573
	case 0x2580 <= character && character <= 0x259f:
		return true
	case 0x1fb00 <= character && character <= 0x1fb3b:
		return true
	case 0xe0b0 <= character && character <= 0xe0b7:
		return true
	}
	return false
}

func drawBox(p *ui.Painter, character rune, rect ui.Rect, fg ui.Color) {
	scale := p.Scale()
	if scale <= 0 {
		scale = 1
	}
	x0 := float32(math.Round(float64(rect.X * scale)))
	y0 := float32(math.Round(float64(rect.Y * scale)))
	x1 := float32(math.Round(float64((rect.X + rect.W) * scale)))
	y1 := float32(math.Round(float64((rect.Y + rect.H) * scale)))
	width, height := x1-x0, y1-y0
	if width <= 0 || height <= 0 {
		return
	}
	fill := func(ax, ay, bx, by float32) {
		if bx > ax && by > ay {
			p.Fill(ui.Rect{X: ax / scale, Y: ay / scale, W: (bx - ax) / scale, H: (by - ay) / scale}, fg, 0)
		}
	}
	switch {
	case 0x2580 <= character && character <= 0x259f:
		drawBlock(character, x0, y0, width, height, fill, p, fg, scale)
		return
	case 0x1fb00 <= character && character <= 0x1fb3b:
		drawSextant(character, x0, y0, width, height, fill)
		return
	case 0xe0b0 <= character && character <= 0xe0b3:
		drawPowerlineTriangle(p, character, x0, y0, x1, y1, scale, fg)
		return
	case 0xe0b4 <= character && character <= 0xe0b7:
		drawPowerlineCap(p, character, x0, y0, x1, y1, scale, fg)
		return
	}
	light := max(1, float32(math.Round(float64(scale))))
	centerX := x0 + float32(math.Floor(float64((width-light)/2))) + light/2
	centerY := y0 + float32(math.Floor(float64((height-light)/2))) + light/2
	if 0x256d <= character && character <= 0x2573 {
		drawArcOrDiagonal(p, character, x0, y0, x1, y1, centerX, centerY, width, height, light, scale, fg)
		return
	}
	arms := boxArms[character-0x2500]
	if arms == "" {
		return
	}
	drawArms(fill, [4]int{
		int(arms[0] - '0'), int(arms[1] - '0'), int(arms[2] - '0'), int(arms[3] - '0'),
	}, x0, y0, x1, y1, centerX, centerY, light)
}

func drawPowerlineTriangle(p *ui.Painter, character rune, x0, y0, x1, y1, scale float32, fg ui.Color) {
	var path ui.Path
	if character == 0xe0b0 || character == 0xe0b1 {
		path.MoveTo(x0/scale, y0/scale).LineTo(x1/scale, (y0+(y1-y0)/2)/scale).LineTo(x0/scale, y1/scale)
	} else {
		path.MoveTo(x1/scale, y0/scale).LineTo(x0/scale, (y0+(y1-y0)/2)/scale).LineTo(x1/scale, y1/scale)
	}
	if character == 0xe0b0 || character == 0xe0b2 {
		path.Close()
		p.FillPath(&path, fg)
	} else {
		p.StrokePath(&path, max(1, float32(math.Round(float64(scale))))/scale, fg)
	}
}

func drawPowerlineCap(p *ui.Painter, character rune, x0, y0, x1, y1, scale float32, fg ui.Color) {
	const control = 0.5523
	middleY := y0 + (y1-y0)/2
	tipX, flatX := x1, x0
	if character >= 0xe0b6 {
		tipX, flatX = x0, x1
	}
	controlX := flatX + (tipX-flatX)*control
	controlY := (y1 - y0) / 2 * control
	var path ui.Path
	path.MoveTo(flatX/scale, y0/scale).
		CubeTo(controlX/scale, y0/scale, tipX/scale, (middleY-controlY)/scale, tipX/scale, middleY/scale).
		CubeTo(tipX/scale, (middleY+controlY)/scale, controlX/scale, y1/scale, flatX/scale, y1/scale)
	if character == 0xe0b4 || character == 0xe0b6 {
		path.Close()
		p.FillPath(&path, fg)
	} else {
		p.StrokePath(&path, max(1, float32(math.Round(float64(scale))))/scale, fg)
	}
}

func drawArcOrDiagonal(p *ui.Painter, character rune, x0, y0, x1, y1, centerX, centerY, width, height, light, scale float32, fg ui.Color) {
	middleX, middleY := centerX/scale, centerY/scale
	radius := min(width, height) / 2 / scale
	var path ui.Path
	switch character {
	case 0x256d:
		path.MoveTo(middleX, y1/scale).LineTo(middleX, middleY+radius).QuadTo(middleX, middleY, middleX+radius, middleY).LineTo(x1/scale, middleY)
	case 0x256e:
		path.MoveTo(middleX, y1/scale).LineTo(middleX, middleY+radius).QuadTo(middleX, middleY, middleX-radius, middleY).LineTo(x0/scale, middleY)
	case 0x256f:
		path.MoveTo(middleX, y0/scale).LineTo(middleX, middleY-radius).QuadTo(middleX, middleY, middleX-radius, middleY).LineTo(x0/scale, middleY)
	case 0x2570:
		path.MoveTo(middleX, y0/scale).LineTo(middleX, middleY-radius).QuadTo(middleX, middleY, middleX+radius, middleY).LineTo(x1/scale, middleY)
	case 0x2571:
		path.MoveTo(x1/scale, y0/scale).LineTo(x0/scale, y1/scale)
	case 0x2572:
		path.MoveTo(x0/scale, y0/scale).LineTo(x1/scale, y1/scale)
	case 0x2573:
		path.MoveTo(x1/scale, y0/scale).LineTo(x0/scale, y1/scale)
		p.StrokePath(&path, light/scale, fg)
		path = ui.Path{}
		path.MoveTo(x0/scale, y0/scale).LineTo(x1/scale, y1/scale)
	}
	p.StrokePath(&path, light/scale, fg)
}

func drawArms(fill func(float32, float32, float32, float32), arms [4]int, x0, y0, x1, y1, centerX, centerY, light float32) {
	up, right, down, left := arms[0], arms[1], arms[2], arms[3]
	offset := light
	verticalDouble := up == armDouble || down == armDouble
	horizontalDouble := left == armDouble || right == armDouble
	thick := func(weight int) float32 {
		switch weight {
		case armHeavy:
			return 2 * light
		case armDouble:
			return 2*offset + light
		}
		return light
	}
	horizontalWeight := max(thick(left), thick(right))
	verticalWeight := max(thick(up), thick(down))
	single := func(weight int) bool { return weight == armLight || weight == armHeavy }
	if single(up) {
		weight := thick(up)
		end := centerY + horizontalWeight/2
		if horizontalDouble && down == armNone {
			end = centerY - offset + light/2
		}
		fill(centerX-weight/2, y0, centerX+weight/2, end)
	}
	if single(down) {
		weight := thick(down)
		start := centerY - horizontalWeight/2
		if horizontalDouble && up == armNone {
			start = centerY + offset - light/2
		}
		fill(centerX-weight/2, start, centerX+weight/2, y1)
	}
	if single(left) {
		weight := thick(left)
		end := centerX + verticalWeight/2
		if verticalDouble && right == armNone {
			end = centerX - offset + light/2
		}
		fill(x0, centerY-weight/2, end, centerY+weight/2)
	}
	if single(right) {
		weight := thick(right)
		start := centerX - verticalWeight/2
		if verticalDouble && left == armNone {
			start = centerX + offset - light/2
		}
		fill(start, centerY-weight/2, x1, centerY+weight/2)
	}
	for _, side := range []float32{-1, 1} {
		if verticalDouble {
			lineX := centerX + side*offset - light/2
			across := left
			if side > 0 {
				across = right
			}
			if up == armDouble && down == armDouble && across != armDouble {
				fill(lineX, y0, lineX+light, y1)
			} else {
				inside, outside := centerY-offset+light/2, centerY+offset+light/2
				if across == armDouble {
					outside = inside
				} else if !horizontalDouble {
					outside = centerY + light/2
				}
				if up == armDouble {
					fill(lineX, y0, lineX+light, outside)
				}
				inside, outside = centerY+offset-light/2, centerY-offset-light/2
				if across == armDouble {
					outside = inside
				} else if !horizontalDouble {
					outside = centerY - light/2
				}
				if down == armDouble {
					fill(lineX, outside, lineX+light, y1)
				}
			}
		}
		if horizontalDouble {
			lineY := centerY + side*offset - light/2
			across := up
			if side > 0 {
				across = down
			}
			if left == armDouble && right == armDouble && across != armDouble {
				fill(x0, lineY, x1, lineY+light)
			} else {
				inside, outside := centerX-offset+light/2, centerX+offset+light/2
				if across == armDouble {
					outside = inside
				} else if !verticalDouble {
					outside = centerX + light/2
				}
				if left == armDouble {
					fill(x0, lineY, outside, lineY+light)
				}
				inside, outside = centerX+offset-light/2, centerX-offset-light/2
				if across == armDouble {
					outside = inside
				} else if !verticalDouble {
					outside = centerX - light/2
				}
				if right == armDouble {
					fill(outside, lineY, x1, lineY+light)
				}
			}
		}
	}
}

func drawSextant(character rune, x0, y0, width, height float32, fill func(float32, float32, float32, float32)) {
	index := int(character - 0x1fb00)
	pattern := index + index/20 + 1
	rx := func(fraction float32) float32 { return x0 + float32(math.Round(float64(width*fraction))) }
	ry := func(fraction float32) float32 { return y0 + float32(math.Round(float64(height*fraction))) }
	xm, ya, yb := rx(0.5), ry(1.0/3), ry(2.0/3)
	xs := [...]float32{x0, xm, x0 + width}
	ys := [...]float32{y0, ya, yb, y0 + height}
	for cell := range 6 {
		if pattern&(1<<cell) == 0 {
			continue
		}
		column, row := cell%2, cell/2
		fill(xs[column], ys[row], xs[column+1], ys[row+1])
	}
}

func drawBlock(character rune, x0, y0, width, height float32, fill func(float32, float32, float32, float32), p *ui.Painter, fg ui.Color, scale float32) {
	x1, y1 := x0+width, y0+height
	rx := func(fraction float32) float32 { return x0 + float32(math.Round(float64(width*fraction))) }
	ry := func(fraction float32) float32 { return y0 + float32(math.Round(float64(height*fraction))) }
	switch {
	case character == 0x2580:
		fill(x0, y0, x1, ry(0.5))
	case 0x2581 <= character && character <= 0x2588:
		fill(x0, ry(1-float32(character-0x2580)/8), x1, y1)
	case 0x2589 <= character && character <= 0x258f:
		fill(x0, y0, rx(float32(0x2590-character)/8), y1)
	case character == 0x2590:
		fill(rx(0.5), y0, x1, y1)
	case 0x2591 <= character && character <= 0x2593:
		alpha := float32(character-0x2590) / 4
		p.Fill(ui.Rect{X: x0 / scale, Y: y0 / scale, W: width / scale, H: height / scale}, fg.Alpha(alpha), 0)
	case character == 0x2594:
		fill(x0, y0, x1, ry(1.0/8))
	case character == 0x2595:
		fill(rx(7.0/8), y0, x1, y1)
	default:
		quadrants := quadrantPattern(character)
		middleX, middleY := rx(0.5), ry(0.5)
		if len(quadrants) != 4 {
			return
		}
		if quadrants[0] == '1' {
			fill(x0, y0, middleX, middleY)
		}
		if quadrants[1] == '1' {
			fill(middleX, y0, x1, middleY)
		}
		if quadrants[2] == '1' {
			fill(x0, middleY, middleX, y1)
		}
		if quadrants[3] == '1' {
			fill(middleX, middleY, x1, y1)
		}
	}
}
