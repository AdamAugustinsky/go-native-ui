package main

import (
	"fmt"
	"math"
	"strconv"
	"strings"

	"gonativeui/third_party/lucide"
)

// seg is one op of a compiled path, its points absolute in grid units.
type seg struct {
	op  byte
	pts []float64
}

// parsePath compiles SVG path data (SVG 1.1 §8.3, every command, relative
// and implicit repeats, arcs and numbers run together as "a5 5 0 017.07 0" or
// ".5.5") to moves, lines, cubics and closes. Arcs become cubics of at most a
// quarter turn each; quadratics are kept.
func parsePath(d string) ([]seg, error) {
	toks, err := tokenize(d)
	if err != nil {
		return nil, err
	}
	var out []seg
	var cx, cy, sx, sy float64 // the pen and the subpath's start
	var lc, lq [2]float64      // the last cubic's and quadratic's control point, for S and T
	prev := byte(0)
	i := 0
	for i < len(toks) {
		t := toks[i]
		if !t.cmd {
			return nil, fmt.Errorf("path %q: a number where a command belongs", d)
		}
		c := t.c
		i++
		rel := c >= 'a'
		up := c &^ 0x20
		n := map[byte]int{'M': 2, 'L': 2, 'H': 1, 'V': 1, 'C': 6, 'S': 4, 'Q': 4, 'T': 2, 'A': 7, 'Z': 0}[up]
		if up == 'Z' {
			out = append(out, seg{op: lucide.OpClose})
			cx, cy = sx, sy
			prev = 'Z'
			continue
		}
		if prev == 'Z' && up != 'M' { // a subpath after a close starts where it closed
			out = append(out, seg{lucide.OpMove, []float64{sx, sy}})
		}
		first := true
		for {
			if i+n > len(toks) {
				return nil, fmt.Errorf("path %q: %c lacks arguments", d, c)
			}
			args := make([]float64, n)
			for k := range n {
				if toks[i+k].cmd {
					return nil, fmt.Errorf("path %q: %c lacks arguments", d, c)
				}
				args[k] = toks[i+k].v
			}
			i += n
			ox, oy := 0.0, 0.0
			if rel {
				ox, oy = cx, cy
			}
			switch up {
			case 'M':
				if first {
					cx, cy = args[0]+ox, args[1]+oy
					sx, sy = cx, cy
					out = append(out, seg{lucide.OpMove, []float64{cx, cy}})
				} else { // further pairs are lines
					cx, cy = args[0]+ox, args[1]+oy
					out = append(out, seg{lucide.OpLine, []float64{cx, cy}})
				}
			case 'L':
				cx, cy = args[0]+ox, args[1]+oy
				out = append(out, seg{lucide.OpLine, []float64{cx, cy}})
			case 'H':
				if rel {
					cx += args[0]
				} else {
					cx = args[0]
				}
				out = append(out, seg{lucide.OpLine, []float64{cx, cy}})
			case 'V':
				if rel {
					cy += args[0]
				} else {
					cy = args[0]
				}
				out = append(out, seg{lucide.OpLine, []float64{cx, cy}})
			case 'C', 'S':
				var x1, y1 float64
				k := 0
				if up == 'C' {
					x1, y1 = args[0]+ox, args[1]+oy
					k = 2
				} else if p := prev &^ 0x20; p == 'C' || p == 'S' {
					x1, y1 = 2*cx-lc[0], 2*cy-lc[1]
				} else {
					x1, y1 = cx, cy
				}
				x2, y2 := args[k]+ox, args[k+1]+oy
				x, y := args[k+2]+ox, args[k+3]+oy
				out = append(out, seg{lucide.OpCubic, []float64{x1, y1, x2, y2, x, y}})
				lc = [2]float64{x2, y2}
				cx, cy = x, y
			case 'Q', 'T':
				var x1, y1 float64
				k := 0
				if up == 'Q' {
					x1, y1 = args[0]+ox, args[1]+oy
					k = 2
				} else if p := prev &^ 0x20; p == 'Q' || p == 'T' {
					x1, y1 = 2*cx-lq[0], 2*cy-lq[1]
				} else {
					x1, y1 = cx, cy
				}
				x, y := args[k]+ox, args[k+1]+oy
				out = append(out, seg{lucide.OpQuad, []float64{x1, y1, x, y}})
				lq = [2]float64{x1, y1}
				cx, cy = x, y
			case 'A':
				x, y := args[5]+ox, args[6]+oy
				out = append(out, arc(cx, cy, args[0], args[1], args[2], args[3] != 0, args[4] != 0, x, y)...)
				cx, cy = x, y
			}
			prev = c
			if up == 'M' {
				prev = 'L'
			}
			first = false
			if i >= len(toks) || toks[i].cmd {
				break
			}
		}
	}
	return out, nil
}

type token struct {
	cmd bool
	c   byte
	v   float64
}

// tokenize splits path data into commands and numbers; an arc's two flags
// are one digit each, as SVG lets them run into the numbers around them.
func tokenize(d string) ([]token, error) {
	var toks []token
	cmd := byte(0)
	arg := 0
	for i := 0; i < len(d); {
		c := d[i]
		switch {
		case c == ' ' || c == ',' || c == '\t' || c == '\n' || c == '\r':
			i++
			continue
		case strings.IndexByte("MmLlHhVvCcSsQqTtAaZz", c) >= 0:
			cmd, arg = c, 0
			toks = append(toks, token{cmd: true, c: c})
			i++
			continue
		}
		if (cmd == 'a' || cmd == 'A') && (arg%7 == 3 || arg%7 == 4) {
			if c != '0' && c != '1' {
				return nil, fmt.Errorf("path %q: arc flag %q", d, c)
			}
			toks = append(toks, token{v: float64(c - '0')})
			i++
			arg++
			continue
		}
		j := i
		if d[j] == '+' || d[j] == '-' {
			j++
		}
		digits := func() {
			for j < len(d) && d[j] >= '0' && d[j] <= '9' {
				j++
			}
		}
		digits()
		if j < len(d) && d[j] == '.' {
			j++
			digits()
		}
		if j < len(d) && (d[j] == 'e' || d[j] == 'E') {
			j++
			if j < len(d) && (d[j] == '+' || d[j] == '-') {
				j++
			}
			digits()
		}
		v, err := strconv.ParseFloat(d[i:j], 64)
		if err != nil {
			return nil, fmt.Errorf("path %q: %w", d, err)
		}
		toks = append(toks, token{v: v})
		i = j
		arg++
	}
	return toks, nil
}

// arc converts an elliptical arc from (x1, y1) to (x2, y2) to cubics (SVG
// 1.1 F.6.5 and F.6.6: the radii are scaled up when too small).
func arc(x1, y1, rx, ry, phiDeg float64, large, sweep bool, x2, y2 float64) []seg {
	if x1 == x2 && y1 == y2 {
		return nil
	}
	rx, ry = math.Abs(rx), math.Abs(ry)
	if rx == 0 || ry == 0 {
		return []seg{{lucide.OpLine, []float64{x2, y2}}}
	}
	phi := phiDeg * math.Pi / 180
	cos, sin := math.Cos(phi), math.Sin(phi)
	dx, dy := (x1-x2)/2, (y1-y2)/2
	x1p, y1p := cos*dx+sin*dy, -sin*dx+cos*dy
	if l := x1p*x1p/(rx*rx) + y1p*y1p/(ry*ry); l > 1 {
		s := math.Sqrt(l)
		rx, ry = rx*s, ry*s
	}
	num := rx*rx*ry*ry - rx*rx*y1p*y1p - ry*ry*x1p*x1p
	den := rx*rx*y1p*y1p + ry*ry*x1p*x1p
	co := 0.0
	if num > 0 && den > 0 {
		co = math.Sqrt(num / den)
	}
	if large == sweep {
		co = -co
	}
	cxp, cyp := co*rx*y1p/ry, -co*ry*x1p/rx
	cx := cos*cxp - sin*cyp + (x1+x2)/2
	cy := sin*cxp + cos*cyp + (y1+y2)/2
	ang := func(ux, uy, vx, vy float64) float64 {
		a := math.Atan2(ux*vy-uy*vx, ux*vx+uy*vy)
		return a
	}
	t1 := ang(1, 0, (x1p-cxp)/rx, (y1p-cyp)/ry)
	dt := ang((x1p-cxp)/rx, (y1p-cyp)/ry, (-x1p-cxp)/rx, (-y1p-cyp)/ry)
	if !sweep && dt > 0 {
		dt -= 2 * math.Pi
	} else if sweep && dt < 0 {
		dt += 2 * math.Pi
	}
	n := int(math.Ceil(math.Abs(dt)/(math.Pi/2) - 1e-9))
	if n < 1 {
		n = 1
	}
	step := dt / float64(n)
	k := 4.0 / 3 * math.Tan(step/4)
	pt := func(t float64) (float64, float64) {
		x, y := rx*math.Cos(t), ry*math.Sin(t)
		return cos*x - sin*y + cx, sin*x + cos*y + cy
	}
	der := func(t float64) (float64, float64) {
		x, y := -rx*math.Sin(t), ry*math.Cos(t)
		return cos*x - sin*y, sin*x + cos*y
	}
	out := make([]seg, 0, n)
	t := t1
	for i := range n {
		ta, tb := t, t+step
		ax, ay := pt(ta)
		bx, by := pt(tb)
		if i == 0 {
			ax, ay = x1, y1
		}
		if i == n-1 {
			bx, by = x2, y2
		}
		dax, day := der(ta)
		dbx, dby := der(tb)
		out = append(out, seg{lucide.OpCubic, []float64{ax + k*dax, ay + k*day, bx - k*dbx, by - k*dby, bx, by}})
		t = tb
	}
	return out
}
