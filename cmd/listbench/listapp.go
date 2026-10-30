package main

import (
	"math"

	"gonativeui/app"
	"gonativeui/text"
	"gonativeui/ui"
)

// Geometry in points; the Builder converts with Context.Pt.
const (
	defRows   = 1_000_000
	rowPt     = 24
	headerPt  = 32
	trackPt   = 14
	thumbMinP = 32
	overscan  = 2
)

// Colours (0xRRGGBBAA).
const (
	colBG     = 0x1E1F22FF
	colHeader = 0x2B2D30FF
	colAlt    = 0xFFFFFF08
	colText   = 0xDFE1E5FF
	colDim    = 0x9DA0A8FF
	colAccent = 0x3574F0FF
	colTrack  = 0xFFFFFF0C
	colThumb  = 0xFFFFFF48
)

var iconColors = [...]uint32{0x5C8DE8FF, 0x6BBF73FF, 0xE0A458FF, 0xB07CD8FF}

// listApp is a file-list-like virtual list of a million rows: header,
// columns, alternating rows, hover highlight, click and keyboard selection
// (kept by row index), a draggable scrollbar thumb and smooth scrolling.
type listApp struct {
	a    *app.App
	rows int

	font      text.FontKey
	fontScale float32

	selected int // -1: none
	jump     int // row to put at the top at the next Build, -1: none

	// Scrollbar drag, in pixels.
	dragging         bool
	dragY            float32
	dragScroll       float64
	thumbY0, thumbY1 float32 // screen rows of the thumb in the last frame
	trackH           float32
	listH            float32 // list viewport height in the last frame
	scroll           float64 // list scroll offset at the start of the last Build
	listY0           float32 // list top in the last frame
	rowPx            float32

	// What the last Build did (the bench and the tests read these).
	first, last int // rows built
	counting    bool
	rowsBuilt   int
	buildCalls  int
	rowsMax     int

	script     func(frame int, in *ui.Input) bool
	scrollRows int // rows the bench script scrolls per frame
}

func newListApp(rows int) *listApp {
	return &listApp{rows: rows, selected: -1, jump: -1, scrollRows: 3}
}

// Init creates the fonts and sizes the caches for scrolling through endless
// new strings: the run cache is reserved to its limits so that, once warm,
// shaping a new row allocates nothing.
func (l *listApp) Init(a *app.App) error {
	l.a = a
	a.UI.Reserve(1024)
	a.Text.ReserveRuns(text.WorkingSetRuns, text.WorkingSetGlyphs, text.WorkingSetTextBytes)
	return nil
}

// Script implements app.Scripter.
func (l *listApp) Script(frame int, in *ui.Input) bool {
	if l.script == nil {
		return true // no input; the run is ended by its frame count
	}
	return l.script(frame, in)
}

func (l *listApp) setFonts(scale float32) {
	l.font = l.a.Text.Font(text.FontDesc{SizePt: 13, Weight: 400, Scale: scale})
	l.fontScale = scale
}

// Build is one frame of the UI.
func (l *listApp) Build(c *ui.Context) {
	if l.fontScale != c.Scale() {
		l.setFonts(c.Scale())
	}
	rh := c.Pt(rowPt)
	l.rowPx = rh
	c.PushFont(l.font)
	c.PushTextColor(colText)

	c.NextSize(ui.Pct(1), ui.Pct(1))
	c.NextBG(colBG)
	c.Open(ui.DrawBackground, "root")

	l.header(c)

	c.NextSize(ui.Pct(1), ui.Pct(1))
	c.NextAxis(ui.AxisX)
	c.Open(0, "body")
	bodyKey := c.ParentKey()
	trackKey := ui.HashString(bodyKey, "track")
	thumbKey := ui.HashString(trackKey, "thumb")

	// The list.
	c.NextSize(ui.Pct(1), ui.Pct(1))
	list := c.Box(ui.ScrollY|ui.Clip|ui.Hoverable, "list")
	listKey := c.KeyOf(list)
	r := c.Rect(list)
	if vh := r[3] - r[1]; vh > 0 {
		l.listH, l.listY0 = vh, r[1]
	} else { // first frame: the previous layout does not exist yet
		_, wh := c.Size()
		l.listH, l.listY0 = float32(wh)-c.Pt(headerPt), c.Pt(headerPt)
	}

	_, l.scroll = c.Scroll(list)
	l.scrollbarInput(c, list, trackKey, thumbKey)
	l.keys(c, list, rh)
	if l.jump >= 0 {
		c.SetScroll(list, 0, float64(min(l.jump, l.rows-1))*float64(rh))
		l.jump = -1
	}

	first, last := c.ListRange(list, l.rows, rh, overscan)
	l.first, l.last = first, last
	if l.counting {
		l.buildCalls++
		l.rowsBuilt += last - first
		l.rowsMax = max(l.rowsMax, last-first)
	}
	c.Push(list)
	for row := first; row < last; row++ {
		l.row(c, listKey, row, rh)
	}
	c.Pop()

	l.scrollbar(c, list)
	c.Close() // body
	c.Close() // root
	c.PopTextColor()
	c.PopFont()
}

func (l *listApp) header(c *ui.Context) {
	c.NextSize(ui.Pct(1), ui.Px(c.Pt(headerPt)))
	c.NextAxis(ui.AxisX)
	c.NextPadding(c.Pt(10), 0, c.Pt(10+trackPt), 0)
	c.NextGap(c.Pt(10))
	c.NextBG(colHeader)
	c.NextBorder(0x00000060, 1)
	c.Open(ui.DrawBackground|ui.DrawBorder, "header")
	c.PushTextColor(colDim)
	c.NextSize(ui.Px(c.Pt(16)), ui.Pct(1))
	c.Box(0, "hi")
	// the titles are column headers and the rows' texts cells, so the layout
	// audit checks that each title is over its column
	c.NextSize(ui.Pct(1), ui.Pct(1))
	c.NextRole(ui.RoleColumnHeader)
	c.NextSemName("Name")
	c.Box(ui.DrawText, "Name")
	c.NextSize(ui.Px(c.Pt(90)), ui.Pct(1))
	c.NextAlign(ui.AlignRight)
	c.NextRole(ui.RoleColumnHeader)
	c.NextSemName("Size")
	c.Box(ui.DrawText, "Size")
	c.NextSize(ui.Px(c.Pt(150)), ui.Pct(1))
	c.NextRole(ui.RoleColumnHeader)
	c.NextSemName("Modified")
	c.Box(ui.DrawText, "Modified")
	c.PopTextColor()
	c.Close()
}

func (l *listApp) row(c *ui.Context, listKey ui.Key, row int, rh float32) {
	c.NextSize(ui.Pct(1), ui.Px(rh))
	c.NextAxis(ui.AxisX)
	c.NextPadding(c.Pt(10), 0, c.Pt(10), 0)
	c.NextGap(c.Pt(10))
	dim := uint32(colDim)
	switch {
	case row == l.selected:
		c.NextBG(colAccent)
		dim = colText
	case row&1 == 1:
		c.NextBG(colAlt)
	}
	c.NextRole(ui.RoleRow)
	id := c.BoxKeyed(ui.Clickable|ui.DrawBackground|ui.HotAnimation|ui.DrawHotEffects, ui.KeyN(listKey, uint64(row)), nil)
	if c.Signal(id).Pressed {
		l.selected = row
	}
	c.Push(id)

	c.NextSize(ui.Px(c.Pt(16)), ui.Pct(1))
	c.NextPadding(0, c.Pt(5), 0, c.Pt(5))
	c.Open(0, "ic")
	c.NextSize(ui.Pct(1), ui.Pct(1))
	c.NextBG(iconColors[fileKind(row)])
	c.NextRadius(c.Pt(3))
	c.Box(ui.DrawBackground, "i")
	c.Close()

	c.NextSize(ui.Pct(1), ui.Pct(1))
	name := rowName(c, row)
	c.NextRole(ui.RoleCell)
	c.NextSemNameBytes(name) // and so the row's, which reads out by its first named cell
	c.BoxKeyed(ui.DrawText|ui.FadeOverflowX, ui.KeyN(c.ParentKey(), 0), name)

	c.NextSize(ui.Px(c.Pt(90)), ui.Pct(1))
	c.NextAlign(ui.AlignRight)
	c.NextTextColor(dim)
	size := rowSize(c, row)
	c.NextRole(ui.RoleCell)
	c.NextSemNameBytes(size)
	c.BoxKeyed(ui.DrawText, ui.KeyN(c.ParentKey(), 1), size)

	c.NextSize(ui.Px(c.Pt(150)), ui.Pct(1))
	c.NextTextColor(dim)
	date := rowDate(c, row)
	c.NextRole(ui.RoleCell)
	c.NextSemNameBytes(date)
	c.BoxKeyed(ui.DrawText, ui.KeyN(c.ParentKey(), 2), date)
	c.Pop()
}

// scrollbarInput handles the thumb drag and track clicks from the frame's
// input, before the list's range is taken, so the rows built are the rows of
// the scroll offset the layout will use. The geometry is the last frame's, the
// same one the pointer is hit-tested against.
func (l *listApp) scrollbarInput(c *ui.Context, list ui.ID, trackKey, thumbKey ui.Key) {
	in := c.Input()
	content := float64(l.rows) * float64(l.rowPx)
	view := float64(l.listH)
	span := content - view
	if span <= 0 {
		l.dragging = false
		return
	}
	_, sy := c.Scroll(list)
	if in.Pressed[ui.ButtonLeft] {
		switch c.HotKey() {
		case thumbKey:
			l.dragging, l.dragY, l.dragScroll = true, in.MouseY, sy
		case trackKey: // page toward the click
			if in.MouseY < l.thumbY0 {
				c.ScrollTo(list, 0, max(sy-view, 0))
			} else if in.MouseY > l.thumbY1 {
				c.ScrollTo(list, 0, min(sy+view, span))
			}
		}
	}
	if !l.dragging {
		return
	}
	if !in.Down[ui.ButtonLeft] {
		l.dragging = false
		return
	}
	if room := float64(l.trackH) - float64(l.thumbY1-l.thumbY0); room > 0 {
		c.SetScroll(list, 0, min(max(l.dragScroll+float64(in.MouseY-l.dragY)*span/room, 0), span))
	}
}

// keys moves the selection with the arrow, page, home and end keys and scrolls
// it into view.
func (l *listApp) keys(c *ui.Context, list ui.ID, rh float32) {
	in := c.Input()
	for i := 0; i < in.NKeys; i++ {
		k := &in.Keys[i]
		if !k.Down {
			continue
		}
		page := max(int(l.listH/rh)-1, 1)
		cur := max(l.selected, 0)
		switch k.Code {
		case ui.KeyDown:
			l.moveTo(c, list, rh, l.selected+1)
		case ui.KeyUp:
			l.moveTo(c, list, rh, cur-1)
		case ui.KeyPageDown:
			l.moveTo(c, list, rh, cur+page)
		case ui.KeyPageUp:
			l.moveTo(c, list, rh, cur-page)
		case ui.KeyHome:
			l.moveTo(c, list, rh, 0)
		case ui.KeyEnd:
			l.moveTo(c, list, rh, l.rows-1)
		case ui.KeyEscape:
			if l.a != nil {
				l.a.Quit()
			}
		}
	}
}

func (l *listApp) moveTo(c *ui.Context, list ui.ID, rh float32, sel int) {
	sel = min(max(sel, 0), l.rows-1)
	l.selected = sel
	top := float64(sel) * float64(rh)
	bottom := top + float64(rh)
	_, sy := c.Scroll(list)
	view := float64(l.listH)
	var target float64
	switch {
	case top < sy:
		target = top
	case bottom > sy+view:
		target = bottom - view
	default:
		return
	}
	// A far target (Home, End) jumps: animating across a million rows would
	// only blur past them.
	if math.Abs(target-sy) > 4*view {
		c.SetScroll(list, 0, target)
	} else {
		c.ScrollTo(list, 0, target)
	}
}

// scrollbar builds the track and thumb. The thumb is a floating child of the
// track, placed from the scroll position.
func (l *listApp) scrollbar(c *ui.Context, list ui.ID) {
	tw := c.Pt(trackPt)
	c.NextSize(ui.Px(tw), ui.Pct(1))
	c.NextBG(colTrack)
	track := c.Box(ui.DrawBackground|ui.Clickable, "track")
	trackTop := l.listY0
	if tr := c.Rect(track); tr[3] > tr[1] {
		trackTop, l.trackH = tr[1], tr[3]-tr[1]
	} else { // first frame
		l.trackH = l.listH
	}

	content := float64(l.rows) * float64(l.rowPx)
	view := float64(l.listH)
	if content <= view {
		l.thumbY0, l.thumbY1 = 0, 0
		return
	}
	span := content - view
	_, sy := c.Scroll(list)
	thumbH := max(float64(l.trackH)*view/content, float64(c.Pt(thumbMinP)))
	top := (float64(l.trackH) - thumbH) * min(max(sy/span, 0), 1)
	l.thumbY0, l.thumbY1 = trackTop+float32(top), trackTop+float32(top+thumbH)

	c.Push(track)
	c.NextSize(ui.Px(tw-c.Pt(4)), ui.Px(float32(thumbH)))
	c.NextPos(c.Pt(2), float32(top))
	c.NextBG(colThumb)
	c.NextRadius(c.Pt(4))
	c.NextCursor(ui.CursorPointer) // also while the thumb is dragged
	c.Box(ui.DrawBackground|ui.Clickable|ui.FloatingX|ui.FloatingY|ui.HotAnimation|ui.DrawHotEffects, "thumb")
	c.Pop()
}
