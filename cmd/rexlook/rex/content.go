package rex

import (
	"strings"

	"gonativeui/text"
	"gonativeui/theme"
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
		f := g.r.cells.Mono
		if sp.bold {
			f = g.r.cells.MonoBold
		}
		g.cell(row, sp.col, sp.s, f, theme.Col(sp.c))
	}
}

// cell puts str at a grid cell. Runs of ASCII are one text box (the font is
// monospaced); every other character is a box of its own pinned to its
// column, so fallback faces never shift the grid.
func (g *grid) cell(row, colN int, str string, f text.FontKey, color uint32) {
	r := g.r
	if g.y0+float32(row-1)*r.cells.RowPitch > g.h { // below the pane
		return
	}
	baseline := theme.Snap(g.y0 + float32(row)*r.cells.RowPitch)
	put := func(s string, c int) {
		r.Put(g.c, &g.kg, f, s, g.x0+float32(c)*r.cells.Adv, baseline, color, 0, 0)
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
	theme.At(g.c, x0, y0, x1-x0, y1-y0)
	g.c.NextBG(color)
	g.c.BoxKeyed(ui.DrawBackground|fl, g.kg.Next(), nil)
}

func (g *grid) round(x0, y0, x1, y1, rad float32, color uint32) {
	theme.At(g.c, x0, y0, x1-x0, y1-y0)
	g.c.NextBG(color)
	g.c.NextRadius(rad)
	g.c.BoxKeyed(ui.DrawBackground|fl, g.kg.Next(), nil)
}

func (g *grid) lineY(row int) float32 {
	return theme.Snap(g.y0 - 10*g.r.K + float32(row)*g.r.cells.RowPitch)
}

func (g *grid) cellCentre(c int) float32 { return g.x0 + (float32(c)+0.5)*g.r.cells.Adv }

// hline draws a horizontal line at a grid row from x0 to x1 pixels, leaving
// out the cell ranges in cuts (ordered left to right).
func (g *grid) hline(row int, x0, x1 float32, cuts [][2]int, color uint32) {
	lw := max(1, g.r.K)
	y := g.lineY(row)
	from := x0
	for _, cut := range cuts {
		cx0, cx1 := g.x0+float32(cut[0])*g.r.cells.Adv, g.x0+float32(cut[1]+1)*g.r.cells.Adv
		if cx0 > from {
			g.fill(theme.Snap(from), y, theme.Snap(min(cx0, x1)), y+lw, color)
		}
		from = max(from, cx1)
	}
	if from < x1 {
		g.fill(theme.Snap(from), y, theme.Snap(x1), y+lw, color)
	}
}

// frame is a rounded one-pixel frame between two grid columns and rows, drawn
// procedurally (as terminals do) so the lines are crisp and join the grid.
type frame struct {
	c0, c1, r0, r1   int
	topCuts, botCuts [][2]int // cell ranges [first, last] where text interrupts the line
}

func (g *grid) frame(b frame) {
	k := g.r.K
	lw := max(1, k)
	br := 8 * k
	color := theme.Col(g.pal.Line)
	xl, xr := theme.Snap(g.cellCentre(b.c0)), theme.Snap(g.cellCentre(b.c1))
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
	theme.At(c, x, y, br, br)
	cl := c.BoxKeyed(ui.Clip|fl, g.kg.Next(), nil)
	c.Push(cl)
	theme.At(c, -qx*br, -qy*br, 2*br, 2*br)
	c.NextRadii(radii[0], radii[1], radii[2], radii[3])
	c.NextBorder(color, lw)
	c.BoxKeyed(ui.DrawBorder|fl, g.kg.Next(), nil)
	c.Pop()
}

// ---- panes ----

func (g *grid) agent() {
	p := &g.pal
	g.row(0, span{0, "•", p.Bullet, false}, span{2, "Ran", p.FG, true}, span{6, "git", p.Blue, false},
		span{10, "diff", p.Op, false}, span{15, "--", p.Teal, false}, span{18, "demo/snake.mjs", p.Op, false},
		span{33, "&&", p.Teal, false}, span{36, "git", p.Blue, false}, span{40, "status", p.Op, false},
		span{47, "--short", p.Red, false})
	g.row(1, span{2, "└", p.Dim, false}, span{4, "diff --git a/demo/snake.mjs b/demo/snake.mjs", p.Dim, false})
	g.row(2, span{4, "index a405d8c..333798e 100644", p.Dim, false})
	g.row(3, span{4, "--- a/demo/snake.mjs", p.Dim, false})
	g.row(4, span{4, "+76 lines (ctrl+t to view transcript)", p.Dim, false})
	g.row(6, span{0, "•", p.Dim, false}, span{2, "## Review summary", p.FG, true})
	g.row(8, span{2, "I inspected ", p.FG, false}, span{14, "demo/snake.mjs", p.Green, false},
		span{28, " and ran it in an interactive terminal. The", p.FG, false})
	g.row(9, span{2, "game uses a fixed 24×14 board and advances every 125 ms. It starts", p.FG, false})
	g.row(10, span{2, "with a three-segment snake, moves using arrow keys, grows and adds one", p.FG, false})
	g.row(11, span{2, "point when it eats food, ends on wall or body collision, and supports", p.FG, false})
}

func (g *grid) git() {
	p := &g.pal
	line := theme.Col(p.Line)

	// Left column: collapsed panels as title lines, and the Files box.
	g.hline(0, g.cellCentre(0), g.cellCentre(23), [][2]int{{2, 4}, {6, 11}}, line)
	g.row(0, span{2, "[1]", p.FG, false}, span{6, "Status", p.FG, false})
	g.frame(frame{0, 23, 1, 12, [][2]int{{2, 4}, {6, 21}}, [][2]int{{16, 21}}})
	g.row(1, span{2, "[2]", p.FG, false}, span{6, "Files - Worktree", p.FG, false})
	g.row(2, span{1, "▼", p.FG, false}, span{3, "demo", p.FG, true})
	g.row(3, span{4, "M", p.Red, false}, span{6, "snake.mjs", p.FG, false})
	g.row(12, span{16, "1 of 2", p.FG, false})
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
		g.row(l.row, span{2, l.n, p.FG, false}, span{6, l.name, p.FG, false}, span{16, l.of, p.FG, false})
	}

	// Diff box.
	g.frame(frame{24, 71, 0, 12, [][2]int{{26, 28}, {30, 45}}, nil})
	g.row(0, span{26, "[0]", p.FG, false}, span{30, "Unstaged changes", p.FG, false})
	g.row(1, span{25, "diff --git a/demo/snake.mjs b/demo/snake.mjs", p.FG, true})
	g.row(2, span{25, "index a405d8c..de604b3 100644", p.FG, true})
	g.row(3, span{25, "--- a/demo/snake.mjs", p.FG, true})
	g.row(4, span{25, "+++ b/demo/snake.mjs", p.FG, true})
	g.row(5, span{25, "@@ -1,8 +1,11 @@", p.Teal, false})
	g.row(6, span{26, "#!/usr/bin/env node", p.FG, false})
	g.row(8, span{25, "-const WIDTH = 24;", p.Red, false})
	g.row(9, span{25, "-const HEIGHT = 14;", p.Red, false})
	g.row(10, span{25, "+let WIDTH = 24;", p.Green, false})
	g.row(11, span{25, "+let HEIGHT = 14;", p.Green, false})
	// Scroll thumb on the right edge of the diff box.
	k := g.r.K
	xr, y1 := theme.Snap(g.cellCentre(71)), g.lineY(1)
	g.round(xr-k, y1-16*k, xr+8*k, y1+16*k, 2*k, line)

	// Command log box.
	g.frame(frame{24, 71, 13, 15, [][2]int{{26, 36}}, nil})
	g.row(13, span{26, "Command log", p.FG, false})
	g.row(14, span{27, `git commit -m "Initial commit"`, p.FG, false})

	// Status bar, with underlined links on the right.
	g.row(16, span{0, "Stage: <space> | Commit: c | Stash: s | …", p.Blue, false},
		span{46, "Donate", p.Purple, false}, span{53, "Ask Question", p.Orange, false}, span{66, "0.65.1", p.FG, false})
	ul := func(c0, c1 int, c uint32) {
		y := theme.Snap(g.y0+16*g.r.cells.RowPitch) + 4*k
		g.fill(theme.Snap(g.x0+float32(c0)*g.r.cells.Adv), y, theme.Snap(g.x0+float32(c1+1)*g.r.cells.Adv), y+max(1, k), theme.Col(c))
	}
	ul(46, 51, p.Purple)
	ul(53, 64, p.Orange)
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

// snakeCol is the colour of a game piece at the pane's focus and the theme's mix.
func (g *grid) snakeCol(c *[4]uint32) uint32 {
	return g.r.col(theme.MixRGB(c[0], c[1], g.ft), theme.MixRGB(c[2], c[3], g.ft))
}

func (g *grid) snake() {
	r, k := g.r, g.r.K
	teal := g.snakeCol(&scTeal)
	g.cell(0, 0, "S N A K E", r.cells.Mono, teal)
	g.cell(0, 12, "Score: 43", r.cells.Mono, teal)
	g.cell(1, 0, snakeEdge, r.cells.Mono, teal)
	for row := snakeFirst; row <= snakeLast; row++ {
		g.cell(row, 0, "|", r.cells.Mono, teal)
		g.cell(row, snakeCols, "|", r.cells.Mono, teal)
	}
	g.cell(snakeLast+1, 0, snakeEdge, r.cells.Mono, teal)
	g.cell(snakeLast+2, 0, "Paused · Space resume · R restart · Ctrl+C quit", r.cells.Mono, theme.Col(g.pal.Dim))

	// Game pieces are shapes: 14 px food dots and body squares, one head.
	at := func(c, row int) (float32, float32) {
		return g.cellCentre(c), theme.Snap(g.y0-10*k+float32(row)*r.cells.RowPitch) + 0.5*k
	}
	d := 14 * k
	food, body := g.snakeCol(&scFood), g.snakeCol(&scBody)
	for _, f := range snakeFoods {
		x, y := at(f[0], f[1])
		x0, y0 := theme.Snap(x-d/2), theme.Snap(y-d/2)
		g.round(x0, y0, x0+d, y0+d, d/2, food)
	}
	for _, b := range snakeBodys {
		x, y := at(b[0], b[1])
		x0, y0 := theme.Snap(x-d/2), theme.Snap(y-d/2)
		g.fill(x0, y0, x0+d, y0+d, body)
	}
	g.cell(snakeHeadP[1], snakeHeadP[0], "◆", r.cells.Mono, g.snakeCol(&scHead))
}

func (g *grid) bun() {
	p := &g.pal
	g.row(0, span{0, "$", p.Dim, false}, span{2, "bun", p.Blue, false}, span{6, "run dev", p.Op, false})
	g.row(1, span{0, "bun run", p.FG, true}, span{8, "vite --host", p.Dim, false})
	g.row(3, span{2, "VITE v6.0.3", p.Green, true}, span{14, "ready in 412 ms", p.FG, false})
	g.row(5, span{2, "➜", p.Green, false}, span{4, "Local:", p.FG, true}, span{12, "http://localhost:5173/", p.Blue, false})
	g.row(6, span{2, "➜", p.Green, false}, span{4, "Network:", p.FG, true}, span{13, "use --host to expose", p.Dim, false})
	g.row(7, span{2, "➜", p.Green, false}, span{4, "press h + enter to show help", p.Dim, false})
	g.row(9, span{0, "10:41:07", p.Dim, false}, span{9, "[vite]", p.Teal, false}, span{16, "page reload", p.FG, false}, span{28, "index.html", p.Dim, false})
	g.row(10, span{0, "10:41:19", p.Dim, false}, span{9, "[vite]", p.Teal, false}, span{16, "hmr update", p.FG, false}, span{27, "/src/main.ts", p.Dim, false})
}

func (g *grid) shell() {
	p := &g.pal
	g.row(0, span{0, "~/Sites/rex-snake", p.Blue, false}, span{18, "main", p.Purple, false}, span{23, "±1", p.Orange, false})
	g.row(1, span{0, "❯", p.Green, true}, span{2, "ls", p.FG, false})
	g.row(2, span{0, "README.md", p.FG, false}, span{11, "demo", p.Blue, true}, span{17, "package.json", p.FG, false})
	g.row(4, span{0, "~/Sites/rex-snake", p.Blue, false}, span{18, "main", p.Purple, false}, span{23, "±1", p.Orange, false})
	g.row(5, span{0, "❯", p.Green, true}, span{2, "git status --short", p.FG, false})
	g.row(6, span{1, "M", p.Red, false}, span{3, "demo/snake.mjs", p.FG, false})
	g.row(8, span{0, "~/Sites/rex-snake", p.Blue, false}, span{18, "main", p.Purple, false}, span{23, "±1", p.Orange, false})
	g.row(9, span{0, "❯", p.Green, true})
}
