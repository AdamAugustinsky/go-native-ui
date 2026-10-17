package rex

import (
	"strings"

	"gonativeui/text"
	"gonativeui/ui"
)

// The terminal panes of the reference: an agent session, a git TUI and a
// small game, plus a dev server and a shell prompt for the other tabs and for
// new panes. Only the layout and colours matter; the words follow the
// screenshot closely. Rows and columns are terminal cells.

// grid places terminal cells in a pane. x0 is the left edge of column 0 and
// y0 the baseline of row 0, both relative to the pane.
type grid struct {
	r      *Rex
	c      *ui.Context
	kg     keyGen
	x0, y0 float32
	pal    palette
	ft     float32 // focus, 0..1
	w, h   float32 // pane size
}

type span struct {
	col  int
	s    string
	c    uint32 // 0xRRGGBB
	bold bool
}

func (g *grid) row(row int, spans ...span) {
	for _, sp := range spans {
		f := g.r.fMono
		if sp.bold {
			f = g.r.fMonoBold
		}
		g.cell(row, sp.col, sp.s, f, col(sp.c))
	}
}

// cell puts str at a grid cell. Runs of ASCII are one text box (the font is
// monospaced); every other character is a box of its own pinned to its
// column, so fallback faces never shift the grid.
func (g *grid) cell(row, colN int, str string, f text.FontKey, color uint32) {
	r := g.r
	if g.y0+float32(row-1)*r.rowPitch > g.h { // below the pane
		return
	}
	baseline := snap(g.y0 + float32(row)*r.rowPitch)
	put := func(s string, c int) {
		r.put(g.c, &g.kg, f, s, g.x0+float32(c)*r.adv, baseline, color, 0, 0)
	}
	start, startCol, c := 0, colN, colN
	for i := 0; i < len(str); {
		if str[i] < 0x80 {
			i++
			c++
			continue
		}
		if i > start {
			put(str[start:i], startCol)
		}
		j := i + 1
		for j < len(str) && str[j]&0xC0 == 0x80 {
			j++
		}
		put(str[i:j], c)
		c++
		i = j
		start, startCol = i, c
	}
	if start < len(str) {
		put(str[start:], startCol)
	}
}

// ---- shapes ----

func (g *grid) fill(x0, y0, x1, y1 float32, color uint32) {
	if x1 <= x0 || y1 <= y0 {
		return
	}
	at(g.c, x0, y0, x1-x0, y1-y0)
	g.c.NextBG(color)
	g.c.BoxKeyed(ui.DrawBackground|fl, g.kg.next(), nil)
}

func (g *grid) round(x0, y0, x1, y1, rad float32, color uint32) {
	at(g.c, x0, y0, x1-x0, y1-y0)
	g.c.NextBG(color)
	g.c.NextRadius(rad)
	g.c.BoxKeyed(ui.DrawBackground|fl, g.kg.next(), nil)
}

func (g *grid) lineY(row int) float32 {
	return snap(g.y0 - 10*g.r.k + float32(row)*g.r.rowPitch)
}

func (g *grid) cellCentre(c int) float32 { return g.x0 + (float32(c)+0.5)*g.r.adv }

// hline draws a horizontal line at a grid row from x0 to x1 pixels, leaving
// out the cell ranges in cuts (ordered left to right).
func (g *grid) hline(row int, x0, x1 float32, cuts [][2]int, color uint32) {
	lw := max(1, g.r.k)
	y := g.lineY(row)
	from := x0
	for _, cut := range cuts {
		cx0, cx1 := g.x0+float32(cut[0])*g.r.adv, g.x0+float32(cut[1]+1)*g.r.adv
		if cx0 > from {
			g.fill(snap(from), y, snap(min(cx0, x1)), y+lw, color)
		}
		from = max(from, cx1)
	}
	if from < x1 {
		g.fill(snap(from), y, snap(x1), y+lw, color)
	}
}

// frame is a rounded one-pixel frame between two grid columns and rows, drawn
// procedurally (as terminals do) so the lines are crisp and join the grid.
type frame struct {
	c0, c1, r0, r1   int
	topCuts, botCuts [][2]int // cell ranges [first, last] where text interrupts the line
}

func (g *grid) frame(b frame) {
	k := g.r.k
	lw := max(1, k)
	br := 8 * k
	color := col(g.pal.line)
	xl, xr := snap(g.cellCentre(b.c0)), snap(g.cellCentre(b.c1))
	yt, yb := g.lineY(b.r0), g.lineY(b.r1)
	g.fill(xl, yt+br, xl+lw, yb+lw-br, color)
	g.fill(xr, yt+br, xr+lw, yb+lw-br, color)
	g.hline(b.r0, xl+br, xr+lw-br, b.topCuts, color)
	g.hline(b.r1, xl+br, xr+lw-br, b.botCuts, color)
	// Corners: one quarter of a rounded ring, shown through a clip box.
	g.corner(xl, yt, br, lw, color, [4]float32{br, 0, 0, 0}, 0, 0)
	g.corner(xr+lw-br, yt, br, lw, color, [4]float32{0, br, 0, 0}, 1, 0)
	g.corner(xl, yb+lw-br, br, lw, color, [4]float32{0, 0, 0, br}, 0, 1)
	g.corner(xr+lw-br, yb+lw-br, br, lw, color, [4]float32{0, 0, br, 0}, 1, 1)
}

// corner draws one br×br corner at (x, y): the quarter (qx, qy) of a ring of
// size 2br whose rounded corner is the one in radii.
func (g *grid) corner(x, y, br, lw float32, color uint32, radii [4]float32, qx, qy float32) {
	c := g.c
	at(c, x, y, br, br)
	cl := c.BoxKeyed(ui.Clip|fl, g.kg.next(), nil)
	c.Push(cl)
	at(c, -qx*br, -qy*br, 2*br, 2*br)
	c.NextRadii(radii[0], radii[1], radii[2], radii[3])
	c.NextBorder(color, lw)
	c.BoxKeyed(ui.DrawBorder|fl, g.kg.next(), nil)
	c.Pop()
}

// ---- panes ----

func (g *grid) agent() {
	p := &g.pal
	g.row(0, span{0, "•", p.bullet, false}, span{2, "Ran", p.fg, true}, span{6, "git", p.blue, false},
		span{10, "diff", p.op, false}, span{15, "--", p.teal, false}, span{18, "demo/snake.mjs", p.op, false},
		span{33, "&&", p.teal, false}, span{36, "git", p.blue, false}, span{40, "status", p.op, false},
		span{47, "--short", p.red, false})
	g.row(1, span{2, "└", p.dim, false}, span{4, "diff --git a/demo/snake.mjs b/demo/snake.mjs", p.dim, false})
	g.row(2, span{4, "index a405d8c..333798e 100644", p.dim, false})
	g.row(3, span{4, "--- a/demo/snake.mjs", p.dim, false})
	g.row(4, span{4, "+76 lines (ctrl+t to view transcript)", p.dim, false})
	g.row(6, span{0, "•", p.dim, false}, span{2, "## Review summary", p.fg, true})
	g.row(8, span{2, "I inspected ", p.fg, false}, span{14, "demo/snake.mjs", p.green, false},
		span{28, " and ran it in an interactive terminal. The", p.fg, false})
	g.row(9, span{2, "game uses a fixed 24×14 board and advances every 125 ms. It starts", p.fg, false})
	g.row(10, span{2, "with a three-segment snake, moves using arrow keys, grows and adds one", p.fg, false})
	g.row(11, span{2, "point when it eats food, ends on wall or body collision, and supports", p.fg, false})
}

func (g *grid) git() {
	p := &g.pal
	line := col(p.line)

	// Left column: collapsed panels as title lines, and the Files box.
	g.hline(0, g.cellCentre(0), g.cellCentre(23), [][2]int{{2, 4}, {6, 11}}, line)
	g.row(0, span{2, "[1]", p.fg, false}, span{6, "Status", p.fg, false})
	g.frame(frame{0, 23, 1, 12, [][2]int{{2, 4}, {6, 21}}, [][2]int{{16, 21}}})
	g.row(1, span{2, "[2]", p.fg, false}, span{6, "Files - Worktree", p.fg, false})
	g.row(2, span{1, "▼", p.fg, false}, span{3, "demo", p.fg, true})
	g.row(3, span{4, "M", p.red, false}, span{6, "snake.mjs", p.fg, false})
	g.row(12, span{16, "1 of 2", p.fg, false})
	for _, l := range [...]struct {
		row      int
		n, name  string
		of       string
		nameCuts [][2]int
	}{
		{13, "[3]", "Local bran", "1 of 1", [][2]int{{2, 4}, {6, 21}}},
		{14, "[4]", "Commits - ", "1 of 1", [][2]int{{2, 4}, {6, 21}}},
		{15, "[5]", "Stash", "0 of 0", [][2]int{{2, 4}, {6, 10}, {16, 21}}},
	} {
		g.hline(l.row, g.cellCentre(0), g.cellCentre(23), l.nameCuts, line)
		g.row(l.row, span{2, l.n, p.fg, false}, span{6, l.name, p.fg, false}, span{16, l.of, p.fg, false})
	}

	// Diff box.
	g.frame(frame{24, 71, 0, 12, [][2]int{{26, 28}, {30, 45}}, nil})
	g.row(0, span{26, "[0]", p.fg, false}, span{30, "Unstaged changes", p.fg, false})
	g.row(1, span{25, "diff --git a/demo/snake.mjs b/demo/snake.mjs", p.fg, true})
	g.row(2, span{25, "index a405d8c..de604b3 100644", p.fg, true})
	g.row(3, span{25, "--- a/demo/snake.mjs", p.fg, true})
	g.row(4, span{25, "+++ b/demo/snake.mjs", p.fg, true})
	g.row(5, span{25, "@@ -1,8 +1,11 @@", p.teal, false})
	g.row(6, span{26, "#!/usr/bin/env node", p.fg, false})
	g.row(8, span{25, "-const WIDTH = 24;", p.red, false})
	g.row(9, span{25, "-const HEIGHT = 14;", p.red, false})
	g.row(10, span{25, "+let WIDTH = 24;", p.green, false})
	g.row(11, span{25, "+let HEIGHT = 14;", p.green, false})
	// Scroll thumb on the right edge of the diff box.
	k := g.r.k
	xr, y1 := snap(g.cellCentre(71)), g.lineY(1)
	g.round(xr-k, y1-16*k, xr+8*k, y1+16*k, 2*k, line)

	// Command log box.
	g.frame(frame{24, 71, 13, 15, [][2]int{{26, 36}}, nil})
	g.row(13, span{26, "Command log", p.fg, false})
	g.row(14, span{27, `git commit -m "Initial commit"`, p.fg, false})

	// Status bar, with underlined links on the right.
	g.row(16, span{0, "Stage: <space> | Commit: c | Stash: s | …", p.blue, false},
		span{46, "Donate", p.purple, false}, span{53, "Ask Question", p.orange, false}, span{66, "0.65.1", p.fg, false})
	ul := func(c0, c1 int, c uint32) {
		y := snap(g.y0+16*g.r.rowPitch) + 4*k
		g.fill(snap(g.x0+float32(c0)*g.r.adv), y, snap(g.x0+float32(c1+1)*g.r.adv), y+max(1, k), col(c))
	}
	ul(46, 51, p.purple)
	ul(53, 64, p.orange)
}

var (
	snakeFoods = [][2]int{{3, 4}, {39, 7}, {19, 9}, {41, 10}, {47, 17}, {13, 23}, {55, 23}, {5, 24}, {31, 24}, {9, 25}, {49, 27}, {21, 28}}
	snakeBodys = [][2]int{{17, 13}, {19, 13}, {21, 13}, {23, 13}, {25, 13}, {27, 13}, {17, 14}, {27, 14}, {15, 15}, {17, 15}, {27, 15}, {27, 16}}
	snakeHeadP = [2]int{27, 17}
)

const (
	snakeCols  = 57 // the board's right edge sits at this column
	snakeFirst = 2  // first and last board rows
	snakeLast  = 30
)

// snakeEdge is "+---…---+" across the board.
var snakeEdge = "+" + strings.Repeat("-", snakeCols-1) + "+"

func (g *grid) snake() {
	r, k := g.r, g.r.k
	teal := col(mixRGB(scTeal[0], scTeal[1], g.ft))
	g.cell(0, 0, "S N A K E", r.fMono, teal)
	g.cell(0, 12, "Score: 43", r.fMono, teal)
	g.cell(1, 0, snakeEdge, r.fMono, teal)
	for row := snakeFirst; row <= snakeLast; row++ {
		g.cell(row, 0, "|", r.fMono, teal)
		g.cell(row, snakeCols, "|", r.fMono, teal)
	}
	g.cell(snakeLast+1, 0, snakeEdge, r.fMono, teal)
	g.cell(snakeLast+2, 0, "Paused · Space resume · R restart · Ctrl+C quit", r.fMono, col(g.pal.dim))

	// Game pieces are shapes: 14 px food dots and body squares, one head.
	at := func(c, row int) (float32, float32) {
		return g.cellCentre(c), snap(g.y0-10*k+float32(row)*r.rowPitch) + 0.5*k
	}
	d := 14 * k
	food, body := col(mixRGB(scFood[0], scFood[1], g.ft)), col(mixRGB(scBody[0], scBody[1], g.ft))
	for _, f := range snakeFoods {
		x, y := at(f[0], f[1])
		x0, y0 := snap(x-d/2), snap(y-d/2)
		g.round(x0, y0, x0+d, y0+d, d/2, food)
	}
	for _, b := range snakeBodys {
		x, y := at(b[0], b[1])
		x0, y0 := snap(x-d/2), snap(y-d/2)
		g.fill(x0, y0, x0+d, y0+d, body)
	}
	g.cell(snakeHeadP[1], snakeHeadP[0], "◆", r.fMono, col(mixRGB(scHead[0], scHead[1], g.ft)))
}

func (g *grid) bun() {
	p := &g.pal
	g.row(0, span{0, "$", p.dim, false}, span{2, "bun", p.blue, false}, span{6, "run dev", p.op, false})
	g.row(1, span{0, "bun run", p.fg, true}, span{8, "vite --host", p.dim, false})
	g.row(3, span{2, "VITE v6.0.3", p.green, true}, span{14, "ready in 412 ms", p.fg, false})
	g.row(5, span{2, "➜", p.green, false}, span{4, "Local:", p.fg, true}, span{12, "http://localhost:5173/", p.blue, false})
	g.row(6, span{2, "➜", p.green, false}, span{4, "Network:", p.fg, true}, span{13, "use --host to expose", p.dim, false})
	g.row(7, span{2, "➜", p.green, false}, span{4, "press h + enter to show help", p.dim, false})
	g.row(9, span{0, "10:41:07", p.dim, false}, span{9, "[vite]", p.teal, false}, span{16, "page reload", p.fg, false}, span{28, "index.html", p.dim, false})
	g.row(10, span{0, "10:41:19", p.dim, false}, span{9, "[vite]", p.teal, false}, span{16, "hmr update", p.fg, false}, span{27, "/src/main.ts", p.dim, false})
}

func (g *grid) shell() {
	p := &g.pal
	g.row(0, span{0, "~/Sites/rex-snake", p.blue, false}, span{18, "main", p.purple, false}, span{23, "±1", p.orange, false})
	g.row(1, span{0, "❯", p.green, true}, span{2, "ls", p.fg, false})
	g.row(2, span{0, "README.md", p.fg, false}, span{11, "demo", p.blue, true}, span{17, "package.json", p.fg, false})
	g.row(4, span{0, "~/Sites/rex-snake", p.blue, false}, span{18, "main", p.purple, false}, span{23, "±1", p.orange, false})
	g.row(5, span{0, "❯", p.green, true}, span{2, "git status --short", p.fg, false})
	g.row(6, span{1, "M", p.red, false}, span{3, "demo/snake.mjs", p.fg, false})
	g.row(8, span{0, "~/Sites/rex-snake", p.blue, false}, span{18, "main", p.purple, false}, span{23, "±1", p.orange, false})
	g.row(9, span{0, "❯", p.green, true})
}
