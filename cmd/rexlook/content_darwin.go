//go:build darwin

package main

import (
	"strings"
)

// The three terminal panes of the reference: an agent session (focused), a
// git TUI and a small game. Only the layout and colours matter; the words
// follow the screenshot closely. Rows and columns are terminal cells.

type span struct {
	col  int
	s    string
	c    uint32 // 0xRRGGBB
	bold bool
}

func (sc *scene) row(t *term, row int, spans ...span) {
	for _, sp := range spans {
		f := sc.fMono
		if sp.bold {
			f = sc.fMonoBold
		}
		sc.cell(t, row, sp.col, sp.s, f, col(sp.c))
	}
}

func (sc *scene) contentAgent(t *term) {
	p := t.pal
	sc.row(t, 0, span{0, "•", p.bullet, false}, span{2, "Ran", p.fg, true}, span{6, "git", p.blue, false},
		span{10, "diff", p.op, false}, span{15, "--", p.teal, false}, span{18, "demo/snake.mjs", p.op, false},
		span{33, "&&", p.teal, false}, span{36, "git", p.blue, false}, span{40, "status", p.op, false},
		span{47, "--short", p.red, false})
	sc.row(t, 1, span{2, "└", p.dim, false}, span{4, "diff --git a/demo/snake.mjs b/demo/snake.mjs", p.dim, false})
	sc.row(t, 2, span{4, "index a405d8c..333798e 100644", p.dim, false})
	sc.row(t, 3, span{4, "--- a/demo/snake.mjs", p.dim, false})
	sc.row(t, 4, span{4, "+76 lines (ctrl+t to view transcript)", p.dim, false})
	sc.row(t, 6, span{0, "•", p.dim, false}, span{2, "## Review summary", p.fg, true})
	sc.row(t, 8, span{2, "I inspected ", p.fg, false}, span{14, "demo/snake.mjs", p.green, false},
		span{28, " and ran it in an interactive terminal. The", p.fg, false})
	sc.row(t, 9, span{2, "game uses a fixed 24×14 board and advances every 125 ms. It starts", p.fg, false})
	sc.row(t, 10, span{2, "with a three-segment snake, moves using arrow keys, grows and adds one", p.fg, false})
	sc.row(t, 11, span{2, "point when it eats food, ends on wall or body collision, and supports", p.fg, false})
}

// box is a rounded one-pixel frame between two grid columns and rows, drawn
// procedurally (as terminals do) so the lines are crisp and join the grid.
type box struct {
	c0, c1, r0, r1   int
	topCuts, botCuts [][2]int // cell ranges [first, last] where text interrupts the line
}

func (sc *scene) lineY(t *term, row int) float32 {
	return snap(t.y0 - 10*sc.k + float32(row)*sc.rowPitch)
}

func (sc *scene) cellCentre(t *term, c int) float32 { return t.x0 + (float32(c)+0.5)*sc.adv }

// hline draws a horizontal line at a grid row from x0 to x1 pixels, leaving
// out the cell ranges in cuts.
func (sc *scene) hline(t *term, row int, x0, x1 float32, cuts [][2]int, c uint32) {
	lw := max(1, sc.k)
	y := sc.lineY(t, row)
	from := x0
	// cuts are ordered left to right
	for _, cut := range cuts {
		cx0, cx1 := t.x0+float32(cut[0])*sc.adv, t.x0+float32(cut[1]+1)*sc.adv
		if cx0 > from {
			sc.add(flat(rect{snap(from), y, snap(min(cx0, x1)), y + lw}, c))
		}
		from = max(from, cx1)
	}
	if from < x1 {
		sc.add(flat(rect{snap(from), y, snap(x1), y + lw}, c))
	}
}

func (sc *scene) box(t *term, b box) {
	lw := max(1, sc.k)
	br := 8 * sc.k
	c := col(t.pal.line)
	xl, xr := snap(sc.cellCentre(t, b.c0)), snap(sc.cellCentre(t, b.c1))
	yt, yb := sc.lineY(t, b.r0), sc.lineY(t, b.r1)
	sc.add(flat(rect{xl, yt + br, xl + lw, yb + lw - br}, c))
	sc.add(flat(rect{xr, yt + br, xr + lw, yb + lw - br}, c))
	sc.hline(t, b.r0, xl+br, xr+lw-br, b.topCuts, c)
	sc.hline(t, b.r1, xl+br, xr+lw-br, b.botCuts, c)
	corner := func(r rect, radii [4]float32, clip rect) {
		q := flat(r, 0)
		q.Radii, q.Border, q.BorderColor = radii, lw, c
		q.Clip = clip.arr()
		sc.add(q)
	}
	corner(rect{xl, yt, xl + 2*br, yt + 2*br}, [4]float32{br, 0, 0, 0}, rect{xl, yt, xl + br, yt + br})
	corner(rect{xr + lw - 2*br, yt, xr + lw, yt + 2*br}, [4]float32{0, br, 0, 0}, rect{xr + lw - br, yt, xr + lw, yt + br})
	corner(rect{xl, yb + lw - 2*br, xl + 2*br, yb + lw}, [4]float32{0, 0, 0, br}, rect{xl, yb + lw - br, xl + br, yb + lw})
	corner(rect{xr + lw - 2*br, yb + lw - 2*br, xr + lw, yb + lw}, [4]float32{0, 0, br, 0}, rect{xr + lw - br, yb + lw - br, xr + lw, yb + lw})
}

func (sc *scene) contentGit(t *term) {
	p := t.pal
	line := col(p.line)

	// Left column: collapsed panels as title lines, and the Files box.
	sc.hline(t, 0, sc.cellCentre(t, 0), sc.cellCentre(t, 23), [][2]int{{2, 4}, {6, 11}}, line)
	sc.row(t, 0, span{2, "[1]", p.fg, false}, span{6, "Status", p.fg, false})
	sc.box(t, box{0, 23, 1, 12, [][2]int{{2, 4}, {6, 21}}, [][2]int{{16, 21}}})
	sc.row(t, 1, span{2, "[2]", p.fg, false}, span{6, "Files - Worktree", p.fg, false})
	sc.row(t, 2, span{1, "▼", p.fg, false}, span{3, "demo", p.fg, true})
	sc.row(t, 3, span{4, "M", p.red, false}, span{6, "snake.mjs", p.fg, false})
	sc.row(t, 12, span{16, "1 of 2", p.fg, false})
	for _, l := range []struct {
		row      int
		n, name  string
		of       string
		nameCuts [][2]int
	}{
		{13, "[3]", "Local bran", "1 of 1", [][2]int{{2, 4}, {6, 21}}},
		{14, "[4]", "Commits - ", "1 of 1", [][2]int{{2, 4}, {6, 21}}},
		{15, "[5]", "Stash", "0 of 0", [][2]int{{2, 4}, {6, 10}, {16, 21}}},
	} {
		sc.hline(t, l.row, sc.cellCentre(t, 0), sc.cellCentre(t, 23), l.nameCuts, line)
		sc.row(t, l.row, span{2, l.n, p.fg, false}, span{6, l.name, p.fg, false}, span{16, l.of, p.fg, false})
	}

	// Diff box.
	sc.box(t, box{24, 71, 0, 12, [][2]int{{26, 28}, {30, 45}}, nil})
	sc.row(t, 0, span{26, "[0]", p.fg, false}, span{30, "Unstaged changes", p.fg, false})
	sc.row(t, 1, span{25, "diff --git a/demo/snake.mjs b/demo/snake.mjs", p.fg, true})
	sc.row(t, 2, span{25, "index a405d8c..de604b3 100644", p.fg, true})
	sc.row(t, 3, span{25, "--- a/demo/snake.mjs", p.fg, true})
	sc.row(t, 4, span{25, "+++ b/demo/snake.mjs", p.fg, true})
	sc.row(t, 5, span{25, "@@ -1,8 +1,11 @@", p.teal, false})
	sc.row(t, 6, span{26, "#!/usr/bin/env node", p.fg, false})
	sc.row(t, 8, span{25, "-const WIDTH = 24;", p.red, false})
	sc.row(t, 9, span{25, "-const HEIGHT = 14;", p.red, false})
	sc.row(t, 10, span{25, "+let WIDTH = 24;", p.green, false})
	sc.row(t, 11, span{25, "+let HEIGHT = 14;", p.green, false})
	// Scroll thumb on the right edge of the diff box.
	xr, y1 := snap(sc.cellCentre(t, 71)), sc.lineY(t, 1)
	thumb := rounded(rect{xr - sc.k, y1 - 16*sc.k, xr + 8*sc.k, y1 + 16*sc.k}, col(p.line), 2*sc.k)
	sc.add(thumb)

	// Command log box.
	sc.box(t, box{24, 71, 13, 15, [][2]int{{26, 36}}, nil})
	sc.row(t, 13, span{26, "Command log", p.fg, false})
	sc.row(t, 14, span{27, `git commit -m "Initial commit"`, p.fg, false})

	// Status bar, with underlined links on the right.
	sc.row(t, 16, span{0, "Stage: <space> | Commit: c | Stash: s | …", p.blue, false},
		span{46, "Donate", p.purple, false}, span{53, "Ask Question", p.orange, false}, span{66, "0.65.1", p.fg, false})
	ul := func(c0, c1 int, c uint32) {
		y := snap(t.y0+16*sc.rowPitch) + 4*sc.k
		sc.add(flat(rect{snap(t.x0 + float32(c0)*sc.adv), y, snap(t.x0 + float32(c1+1)*sc.adv), y + max(1, sc.k)}, col(c)))
	}
	ul(46, 51, p.purple)
	ul(53, 64, p.orange)
}

var (
	snakeFood  = [][2]int{{3, 4}, {39, 7}, {19, 9}, {41, 10}, {47, 17}, {13, 23}, {55, 23}, {5, 24}, {31, 24}, {9, 25}, {49, 27}, {21, 28}}
	snakeBody  = [][2]int{{17, 13}, {19, 13}, {21, 13}, {23, 13}, {25, 13}, {27, 13}, {17, 14}, {27, 14}, {15, 15}, {17, 15}, {27, 15}, {27, 16}}
	snakeHead  = [2]int{27, 17}
	snakeCols  = 57 // the board's right edge sits at this column
	snakeFirst = 2  // first and last board rows
	snakeLast  = 30
)

func (sc *scene) contentSnake(t *term) {
	p := t.pal
	teal := col(0x67A3B3)
	sc.cell(t, 0, 0, "S N A K E", sc.fMono, teal)
	sc.cell(t, 0, 12, "Score: 43", sc.fMono, teal)
	edge := "+" + strings.Repeat("-", snakeCols-1) + "+"
	sc.cell(t, 1, 0, edge, sc.fMono, teal)
	for r := snakeFirst; r <= snakeLast; r++ {
		sc.cell(t, r, 0, "|", sc.fMono, teal)
		sc.cell(t, r, snakeCols, "|", sc.fMono, teal)
	}
	sc.cell(t, snakeLast+1, 0, edge, sc.fMono, teal)
	sc.cell(t, snakeLast+2, 0, "Paused · Space resume · R restart · Ctrl+C quit", sc.fMono, col(p.dim))

	// Game pieces are shapes: 14 px food dots and body squares, one head.
	at := func(c, r int) (float32, float32) {
		return sc.cellCentre(t, c), snap(t.y0-10*sc.k+float32(r)*sc.rowPitch) + 0.5*sc.k
	}
	d := 14 * sc.k
	for _, f := range snakeFood {
		x, y := at(f[0], f[1])
		sc.add(rounded(rect{snap(x - d/2), snap(y - d/2), snap(x-d/2) + d, snap(y-d/2) + d}, col(0xBC5263), d/2))
	}
	for _, b := range snakeBody {
		x, y := at(b[0], b[1])
		sc.add(flat(rect{snap(x - d/2), snap(y - d/2), snap(x-d/2) + d, snap(y-d/2) + d}, col(0x558E65)))
	}
	sc.cell(t, snakeHead[1], snakeHead[0], "◆", sc.fMono, col(0x5BA26E))
}
