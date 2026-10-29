// Command shelldemo is the native shell's demo (phase 6, N3a and N3b): a
// window laid out like D0's Finder scene (tools/appkitscenes finder,
// docs/design/liquid-glass-reference.md), with the live materials AppKit uses
// there and the app's own pixels around them:
//
//   - a flush, full-height glass sidebar and inspector (material regions);
//
//   - a unified 52-pt toolbar whose items sit in 36-pt glass capsules
//     (material regions in one glass container), with drawn glyphs;
//
//   - a scrolled table under the toolbar, whose scroll edge is a bar and a
//     hairline once rows are under it, or while the window is inactive;
//
//   - hosted native views: an NSTextField and a native button in the
//     inspector, and a native button in a table row that scrolls and clips
//     with the table. The search capsule opens a drawn popover over the
//     inspector, which hides the native views it covers (the z-order rule).
//
//     shelldemo [-mode system|light|dark] [-scroll pt] [-popover] [-quit-after d]
//     shelldemo -o out.png [-mode light|dark]   offscreen, hosts as placeholders
package main

import (
	"encoding/json"
	"flag"
	"fmt"
	"image/png"
	"os"
	"strconv"
	"sync/atomic"
	"time"

	"gonativeui/app"
	"gonativeui/icon"
	"gonativeui/platform"
	"gonativeui/text"
	"gonativeui/theme"
	"gonativeui/ui"
)

// The window: D0's finder scene, 1120 × 640 points.
const (
	winW, winH  = 1120, 640
	toolbarH    = 52
	sidebarW    = 180
	inspectorW  = 230
	headerH     = 28
	rowH        = 24
	rowInset    = 16  // row padding and cell padding: where the Name column starts
	dateCol     = 200 // the Date Modified column's left edge, from the pane's right edge
	slotW       = 22  // the actions column before it: the row's native button
	cellGap     = 8
	rows        = 60
	hostRow     = 3 // the table row with a native button
	cmdHostRow  = 11
	cmdInfo     = 12
	capsuleH    = 36
	capsuleY    = 8
	titleX      = 284
	sideRowH    = 32
	sideHeaderH = 19
)

// capsule is one toolbar capsule of the finder scene (D0's dump), in points.
type capsule struct {
	x, w  float32
	icons []icon.ID
}

// The scene's extra icons, after icon.NumStandard.
const (
	icSidebar = icon.NumStandard + iota
	icBack
	icForward
	icGrid
	icShare
	icTag
	icMore
	icInspector
	icClock
	icApps
	icDesktop
	icDocs
	icDownload
	icDisk
	icNet
	numIcons
)

var extraIcons = []icon.Spec{
	{Symbol: "sidebar.left", Fallback: '◧', Pt: 16},
	{Symbol: "chevron.left", Fallback: '‹', Pt: 16},
	{Symbol: "chevron.right", Fallback: '›', Pt: 16},
	{Symbol: "square.grid.2x2", Fallback: '▦', Pt: 16},
	{Symbol: "square.and.arrow.up", Fallback: '↑', Pt: 16},
	{Symbol: "tag", Fallback: '◇', Pt: 16},
	{Symbol: "ellipsis.circle", Fallback: '…', Pt: 16},
	{Symbol: "sidebar.right", Fallback: '◨', Pt: 16},
	{Symbol: "clock", Fallback: '◷', Pt: 15},
	{Symbol: "app.badge", Fallback: '▣', Pt: 15},
	{Symbol: "menubar.dock.rectangle", Fallback: '▭', Pt: 15},
	{Symbol: "doc", Fallback: '▯', Pt: 15},
	{Symbol: "arrow.down.circle", Fallback: '↓', Pt: 15},
	{Symbol: "internaldrive", Fallback: '▤', Pt: 15},
	{Symbol: "network", Fallback: '◎', Pt: 15},
}

var capsules = []capsule{
	{x: 96, w: 36, icons: []icon.ID{icSidebar}},
	{x: 188, w: 86, icons: []icon.ID{icBack, icForward}},
	{x: 599.5, w: 46, icons: []icon.ID{icGrid}},
	{x: 654, w: 47.5, icons: []icon.ID{icon.ChevronDown}},
	{x: 706, w: 83.5, icons: []icon.ID{icShare, icTag}},
	{x: 794, w: 44.5, icons: []icon.ID{icMore}},
	{x: 846.5, w: 36, icons: []icon.ID{icon.Search}},
	{x: 898.5, w: 36, icons: []icon.ID{icInspector}},
}

type sideItem struct {
	header bool
	ic     icon.ID
	name   string
}

var sidebar = []sideItem{
	{header: true, name: "Favorites"},
	{ic: icClock, name: "Recents"},
	{ic: icApps, name: "Applications"},
	{ic: icDesktop, name: "Desktop"},
	{ic: icDocs, name: "Documents"},
	{ic: icDownload, name: "Downloads"},
	{header: true, name: "Locations"},
	{ic: icDisk, name: "Macintosh HD"},
	{ic: icNet, name: "Network"},
}

// demo is the app.
type demo struct {
	a     *app.App
	ts    *text.System
	icons *icon.Atlas
	scale float32
	body, bold, small, title,
	semi text.FontKey

	force    int // 0 system, 1 light, 2 dark
	popover  bool
	scrollTo float32
	field    platform.HostID // inspector NSTextField
	button   platform.HostID // inspector native button
	rowBtn   platform.HostID // the table row's native button
	table    ui.ID
	names    [rows][]byte
	clicks   int
	clickBuf [32]byte
	builds   atomic.Int64 // frames built (-idle)
}

func main() {
	out := flag.String("o", "", "render offscreen at 2240×1280 px into this PNG and exit")
	mode := flag.String("mode", "system", "system, light or dark")
	scroll := flag.Float64("scroll", 0, "scroll the table this many points at start (content under the toolbar)")
	pop := flag.Bool("popover", false, "open the search popover at start (it hides the native views it covers)")
	quit := flag.Duration("quit-after", 0, "quit after this long")
	bench := flag.Bool("bench", false, "scroll the table and toggle the popover every frame (the hosts move, hide and show); print the bench report as JSON")
	gate := flag.Bool("gate", false, "with -bench: exit with status 3 unless the measured frames allocated nothing")
	idle := flag.Duration("idle", 0, "show the window, wait 1.5 s, then print how many frames were built over this long while nothing happens, and exit")
	flag.Parse()
	d := &demo{popover: *pop, scrollTo: float32(*scroll)}
	switch *mode {
	case "light":
		d.force = 1
	case "dark":
		d.force = 2
	}
	for i := range d.names {
		d.names[i] = fmt.Appendf(nil, "Document %02d.txt", i+1)
	}
	var err error
	switch {
	case *out != "":
		err = d.render(*out)
	case *bench:
		err = runBench(d, *gate)
	case *idle > 0:
		go func() {
			time.Sleep(1500 * time.Millisecond)
			n := d.builds.Load()
			time.Sleep(*idle)
			fmt.Printf("{\"IdleSeconds\": %g, \"IdleFrames\": %d}\n", idle.Seconds(), d.builds.Load()-n)
			os.Exit(0)
		}()
		err = app.Run(config(), d)
	default:
		if *quit > 0 {
			go func() { time.Sleep(*quit); os.Exit(0) }()
		}
		err = app.Run(config(), d)
	}
	if err != nil {
		fmt.Fprintln(os.Stderr, "shelldemo:", err)
		os.Exit(1)
	}
}

func config() app.Config {
	return app.Config{
		Title: "Documents", Width: winW, Height: winH, MinWidth: 760, MinHeight: 400,
		Titlebar: toolbarH, TrafficLights: theme.TrafficCentre(theme.ToolbarUnified),
		Materials: true,
		// Windows: the text field is a Win32 EDIT, which draws with GDI.
		ChildWindows: true,
	}
}

func (d *demo) render(path string) error {
	o, err := app.NewOffscreen(config(), winW*2, winH*2, 2, d)
	if err != nil {
		return err
	}
	defer o.Close()
	o.UI.SetSystemAppearance(ui.Appearance{Dark: d.force == 2, Known: true})
	img, _ := o.Frame()
	for i := 0; i < 8; i++ {
		img, _ = o.Frame()
	}
	f, err := os.Create(path)
	if err != nil {
		return err
	}
	defer f.Close()
	return png.Encode(f, img)
}

// benchDemo is the demo under -bench: its script scrolls the table back and
// forth (the row's native button moves, clips and hides with it, and the
// scroll edge changes) and opens and closes the popover (the inspector's
// native views hide and show), so the shell's views change in most frames.
type benchDemo struct{ *demo }

func (b benchDemo) Script(frame int, in *ui.Input) bool {
	t := frame % 240
	if t > 120 {
		t = 240 - t
	}
	b.scrollTo = float32(t) + 1
	if frame%120 == 0 {
		b.popover = !b.popover
	}
	return true
}

func runBench(d *demo, gate bool) error {
	r, err := app.RunBench(config(), benchDemo{d}, app.BenchOptions{Warm: 600, Frames: 600, Windows: 3, Timeout: 2 * time.Minute})
	if err != nil {
		return err
	}
	j, _ := json.MarshalIndent(r, "", "  ")
	fmt.Println(string(j))
	if gate && r.Mallocs != 0 {
		fmt.Fprintf(os.Stderr, "shelldemo: gate: %d allocations in %d measured frames\n", r.Mallocs, r.Frames)
		os.Exit(3)
	}
	if msg := r.OwnAllocFailure(); gate && msg != "" { // D27: a clean window passes over the runtime's one-offs only
		fmt.Fprintln(os.Stderr, "shelldemo: gate:", msg)
		os.Exit(3)
	}
	return nil
}

// Init implements app.Initer: fonts, icons and the hosts.
func (d *demo) Init(a *app.App) error {
	d.a, d.ts = a, a.Text
	d.icons = icon.New(a.Text, extraIcons...)
	d.icons.Attach(a.R)
	a.UI.Reserve(1024)
	if a.Win != nil {
		d.makeHosts(a.Win)
	} else { // offscreen: placeholders where the native views would be
		d.field, d.button, d.rowBtn = 1, 2, 3
	}
	return nil
}

func (d *demo) makeHosts(w *platform.Window) {
	var err error
	if d.field, err = w.NewHost(platform.HostView, platform.HostOptions{View: newTextField("Documents"), Focusable: true}); err != nil {
		fmt.Fprintln(os.Stderr, "shelldemo: text field host:", err)
	}
	d.button, _ = w.NewHost(platform.HostButton, platform.HostOptions{Symbol: "info.circle", Label: "Get Info", Style: platform.ButtonGlass, Command: cmdInfo})
	d.rowBtn, _ = w.NewHost(platform.HostButton, platform.HostOptions{Symbol: "ellipsis.circle", Label: "More", Command: cmdHostRow})
}

func (d *demo) setScale(s float32) {
	d.scale = s
	f := func(pt float32, wt int) text.FontKey {
		return d.ts.Font(text.FontDesc{SizePt: pt, Weight: wt, Scale: s})
	}
	d.body, d.semi, d.bold, d.small, d.title = f(13, 400), f(13, 600), f(11, 700), f(11, 400), f(15, 700)
	d.icons.Raster(s)
}

// palette is the scene's colours (D0) in the shown mode.
type palette struct {
	label, label2, header, title, sel, stripe, line, bar, popFill, popEdge uint32
}

func (d *demo) colours(dark, active bool) palette {
	if dark {
		p := palette{label: 0xFFFFFFD8, label2: 0xFFFFFF8C, header: 0x8C8C8CFF, title: 0xE9E9E9FF, sel: 0x434343FF,
			stripe: 0xFFFFFF0C, line: 0x40403FFF, bar: 0x262626FF, popFill: 0x2A2A2AFF, popEdge: 0xFFFFFF26}
		if !active {
			p.title, p.sel, p.label = 0x5C5C5CFF, 0x333333FF, 0xFFFFFF80
		}
		return p
	}
	p := palette{label: 0x000000D8, label2: 0x0000007F, header: 0x878787FF, title: 0x4D4D4DFF, sel: 0xD7D7D7FF,
		stripe: 0xF4F5F5FF, line: 0xE5E5E5FF, bar: 0xFFFFFFFF, popFill: 0xFFFFFFFF, popEdge: 0x00000026}
	if !active {
		p.title, p.sel, p.label, p.header = 0xB8B8B8FF, 0xE7E7E7FF, 0x727272FF, 0xA6A6A6FF
	}
	return p
}

// Build implements app.Builder.
func (d *demo) Build(c *ui.Context) {
	d.builds.Add(1)
	if c.Scale() != d.scale {
		d.setScale(c.Scale())
	}
	dark := c.SystemAppearance().Dark
	switch d.force {
	case 1:
		dark = false
	case 2:
		dark = true
	}
	c.SetChromeDark(dark)
	active := c.Input().Focused
	p := d.colours(dark, active)
	if _, y := c.Scroll(d.table); dark && d.table != 0 && y > 0 {
		p.bar = 0x292929FF // the dark pocket over content (D0 §4)
	}
	for {
		id, ok := c.TakeCommand()
		if !ok {
			break
		}
		if id == cmdHostRow || id == cmdInfo {
			d.clicks++
		}
	}
	pt := c.Pt
	W, H := c.Size()
	w, h := float32(W), float32(H)

	// The sidebar and the inspector: flush, full-height glass.
	d.region(c, "sidebar", 0, 0, pt(sidebarW), h, 0, ui.AreaSidebar, uint8(theme.MatSidebar), solid(dark, 0xECECECFF, 0x2A2A2AFF))
	d.region(c, "inspector", w-pt(inspectorW), 0, pt(inspectorW), h, 0, ui.AreaInspector, uint8(theme.MatSidebar), solid(dark, 0xECECECFF, 0x2A2A2AFF))
	d.sidebarRows(c, p)

	// The table, under the toolbar: header at the toolbar's bottom, rows
	// scrolled beneath it.
	paneX, paneW := pt(sidebarW), w-pt(sidebarW+inspectorW)
	d.tableView(c, p, paneX, paneW, h)

	// The scroll edge: the toolbar and the table header make one pocket
	// (D0 §4, finder), a bar of the header's colour under the capsules, with
	// the header's line at its bottom; rows scroll under the header.
	c.NextFlags(theme.Floating)
	c.NextPos(paneX, 0)
	c.NextSize(ui.Px(paneW), ui.Px(pt(toolbarH)))
	bar := c.Box(0, "edge")
	c.Material(bar, ui.AreaTitlebar, uint8(theme.MatScrollEdge), p.bar)

	// The toolbar's glass capsules and their drawn glyphs, and the title.
	for i := range capsules {
		d.capsule(c, i, p, dark)
	}
	c.NextFlags(theme.Floating)
	c.NextPos(pt(titleX), pt(16.5))
	c.NextFont(d.title)
	c.NextTextColor(p.title)
	c.Box(ui.DrawText, "Documents##title")

	d.inspectorView(c, p, w)
	if d.popover {
		d.popoverView(c, p, w)
	}
}

func solid(dark bool, light, darkC uint32) uint32 {
	if dark {
		return darkC
	}
	return light
}

// region builds a floating box at (x, y, wd, ht) pixels and marks it as a
// material region.
func (d *demo) region(c *ui.Context, key string, x, y, wd, ht, radius float32, area ui.MaterialArea, tok uint8, sol uint32) ui.ID {
	c.NextFlags(theme.Floating)
	c.NextPos(x, y)
	c.NextSize(ui.Px(wd), ui.Px(ht))
	c.NextRadius(radius)
	id := c.Box(0, key)
	c.Material(id, area, tok, sol)
	return id
}

func (d *demo) capsule(c *ui.Context, i int, p palette, dark bool) {
	cp := capsules[i]
	pt := c.Pt
	W, _ := c.Size()
	x := pt(cp.x)
	if cp.x > sidebarW+400 { // right-hand items keep their distance from the right edge
		x = float32(W) - pt(winW-cp.x)
	}
	key := ui.KeyN(ui.HashString(0, "capsule"), uint64(i))
	c.NextFlags(theme.Floating | ui.Clickable)
	c.NextPos(x, pt(capsuleY))
	c.NextSize(ui.Px(pt(cp.w)), ui.Px(pt(capsuleH)))
	c.NextRadius(pt(capsuleH / 2))
	c.NextAxis(ui.AxisX)
	c.NextCross(ui.CrossCenter)
	c.NextJustify(ui.JustifyCenter)
	c.NextGap(pt(14))
	id := c.BoxKeyed(ui.Clickable, key, nil)
	c.Material(id, ui.AreaToolbarCapsule, uint8(theme.MatToolbar), solid(dark, 0xF2F2F2FF, 0x333333FF))
	if c.Signal(id).Clicked && cp.icons[0] == icon.Search {
		d.popover = !d.popover
	}
	c.Push(id)
	for j, ic := range cp.icons {
		f, b := d.icons.Prepare(c, ic, p.label)
		c.BoxKeyed(f, ui.KeyN(key, uint64(j)), b)
	}
	c.Pop()
}

func (d *demo) sidebarRows(c *ui.Context, p palette) {
	pt := c.Pt
	y := pt(toolbarH + 2)
	for i, it := range d.sidebarItems() {
		key := ui.KeyN(ui.HashString(0, "side"), uint64(i))
		if it.header {
			if i > 0 {
				y += pt(13)
			}
			c.NextFlags(theme.Floating)
			c.NextPos(pt(14), y+pt(3))
			c.NextFont(d.bold)
			c.NextTextColor(p.header)
			c.Box(ui.DrawText, it.name)
			y += pt(sideHeaderH)
			continue
		}
		selected := it.name == "Documents"
		if selected {
			c.NextFlags(theme.Floating)
			c.NextPos(pt(10), y)
			c.NextSize(ui.Px(pt(sidebarW-20)), ui.Px(pt(sideRowH)))
			c.NextRadius(pt(8))
			c.NextBG(p.sel)
			c.BoxKeyed(ui.DrawBackground, ui.KeyN(key, 1), nil)
		}
		f, b := d.icons.Prepare(c, it.ic, 0x007AFFFF)
		c.NextFlags(theme.Floating)
		c.NextPos(pt(19), y+pt(6))
		c.BoxKeyed(f, ui.KeyN(key, 2), b)
		c.NextFlags(theme.Floating)
		c.NextPos(pt(43), y+pt(8))
		if selected {
			c.NextFont(d.semi)
		} else {
			c.NextFont(d.body)
		}
		c.NextTextColor(p.label)
		c.Box(ui.DrawText, it.name)
		y += pt(sideRowH)
	}
}

func (d *demo) sidebarItems() []sideItem { return sidebar }

func (d *demo) tableView(c *ui.Context, p palette, x, wd, h float32) {
	pt := c.Pt
	top := pt(toolbarH)
	// header: the pocket's lower part, drawn (no capsule over it)
	c.NextFlags(theme.Floating)
	c.NextPos(x, top)
	c.NextSize(ui.Px(wd), ui.Px(pt(headerH)))
	c.NextBG(p.bar)
	c.Box(ui.DrawBackground, "hdrbg")
	c.NextFlags(theme.Floating)
	c.NextPos(x+pt(rowInset), top+pt(6))
	c.NextFont(d.small)
	c.NextTextColor(p.label2)
	c.NextRole(ui.RoleColumnHeader)
	c.NextSemName("Name")
	c.Box(ui.DrawText, "Name##hdr")
	c.NextFlags(theme.Floating)
	c.NextPos(x+wd-pt(dateCol), top+pt(6))
	c.NextFont(d.small)
	c.NextTextColor(p.label2)
	c.NextRole(ui.RoleColumnHeader)
	c.NextSemName("Date Modified")
	c.Box(ui.DrawText, "Date Modified##hdr2")
	c.NextFlags(theme.Floating)
	c.NextPos(x, top+pt(headerH-1))
	c.NextSize(ui.Px(wd), ui.Px(pt(1)))
	c.NextBG(p.line)
	c.Box(ui.DrawBackground, "hdrline")

	// rows: a clipped scroll box from the header's bottom down
	c.NextFlags(theme.Floating)
	c.NextPos(x, top+pt(headerH))
	c.NextSize(ui.Px(wd), ui.Px(h-top-pt(headerH)))
	c.NextAxis(ui.AxisY)
	c.NextPadding(0, pt(4), 0, pt(4))
	d.table = c.Box(ui.ScrollY|ui.Clip|ui.Hoverable, "table")
	if d.scrollTo > 0 {
		c.SetScroll(d.table, 0, float64(pt(d.scrollTo)))
		d.scrollTo = 0
	}
	// The columns: Name from rowInset, the actions slot (every row has
	// one, so no row's dates move), Date Modified from dateCol.
	nameW := wd - pt(rowInset+cellGap+slotW+cellGap+dateCol)
	c.Push(d.table)
	for r := 0; r < rows; r++ {
		key := ui.KeyN(ui.HashString(0, "row"), uint64(r))
		c.NextSize(ui.Px(wd), ui.Px(pt(rowH)))
		c.NextPadding(pt(10), 0, pt(10), 0)
		c.NextRole(ui.RoleRow)
		row := c.BoxKeyed(0, key, nil)
		c.Push(row)
		if r%2 == 1 { // the stripe: an inset rounded band, as the selection
			c.NextBG(p.stripe)
			c.NextRadius(pt(8))
		}
		c.NextSize(ui.Pct(1), ui.Pct(1))
		c.NextAxis(ui.AxisX)
		c.NextCross(ui.CrossCenter)
		c.NextPadding(pt(rowInset-10), 0, pt(rowInset-10), 0)
		c.NextGap(pt(cellGap))
		c.Push(c.BoxKeyed(ui.DrawBackground, ui.KeyN(key, 9), nil))
		c.NextSize(ui.Px(nameW), ui.Pct(1))
		c.NextAxis(ui.AxisX)
		c.NextCross(ui.CrossCenter)
		c.NextGap(pt(cellGap))
		c.NextRole(ui.RoleCell)
		c.NextSemNameBytes(d.names[r]) // and so the row's, which reads out by its first named cell
		c.Push(c.BoxKeyed(ui.Clip, ui.KeyN(key, 2), nil))
		f, b := d.icons.Prepare(c, icDocs, 0x1E90FFFF)
		c.BoxKeyed(f, ui.KeyN(key, 1), b)
		c.NextFont(d.body)
		c.NextTextColor(p.label)
		c.BoxKeyed(ui.DrawText, ui.KeyN(key, 3), d.names[r])
		c.Pop()
		c.NextSize(ui.Px(pt(slotW)), ui.Px(pt(slotW)))
		if r == hostRow {
			c.Host(uint32(d.rowBtn), ui.KeyN(key, 4), 0)
		} else {
			c.BoxKeyed(0, ui.KeyN(key, 4), nil)
		}
		c.NextFont(d.body)
		c.NextTextColor(p.label2)
		c.NextRole(ui.RoleCell)
		c.NextSemName("Today at 09:41")
		c.Box(ui.DrawText, "Today at 09:41")
		c.Pop()
		c.Pop()
	}
	c.Pop()
}

func (d *demo) inspectorView(c *ui.Context, p palette, w float32) {
	pt := c.Pt
	x := w - pt(inspectorW) + pt(16)
	c.NextFlags(theme.Floating)
	c.NextPos(x, pt(toolbarH+12))
	c.NextFont(d.semi)
	c.NextTextColor(p.label)
	c.Box(ui.DrawText, "Info##insp")
	c.NextFlags(theme.Floating)
	c.NextPos(x, pt(toolbarH+40))
	c.NextFont(d.small)
	c.NextTextColor(p.label2)
	c.Box(ui.DrawText, "Name##nm")
	c.NextFlags(theme.Floating)
	c.NextPos(x, pt(toolbarH+58))
	c.NextSize(ui.Px(pt(inspectorW-32)), ui.Px(pt(22)))
	c.Host(uint32(d.field), ui.HashString(0, "field"), ui.HostFocusable|ui.HostTextEntry)
	c.NextFlags(theme.Floating)
	c.NextPos(x, pt(toolbarH+96))
	c.NextSize(ui.Px(pt(28)), ui.Px(pt(28)))
	c.Host(uint32(d.button), ui.HashString(0, "infobtn"), 0)
	c.NextFlags(theme.Floating)
	c.NextPos(x+pt(40), pt(toolbarH+102))
	c.NextFont(d.small)
	c.NextTextColor(p.label2)
	b := append(strconv.AppendInt(d.clickBuf[:0], int64(d.clicks), 10), " clicks"...)
	c.BoxKeyed(ui.DrawText, ui.HashString(0, "clicks"), b)
}

func (d *demo) popoverView(c *ui.Context, p palette, w float32) {
	pt := c.Pt
	anchor := [4]float32{w - pt(winW-846.5), pt(capsuleY), w - pt(winW-882.5), pt(capsuleY + capsuleH)}
	c.NextSize(ui.Px(pt(260)), ui.Px(pt(140)))
	c.NextBG(p.popFill)
	c.NextBorder(p.popEdge, 1)
	c.NextRadius(pt(12))
	c.NextPad(pt(14))
	c.NextAxis(ui.AxisY)
	c.NextGap(pt(6))
	c.NextFlags(ui.DrawBackground | ui.DrawBorder | ui.DrawShadow)
	sig := c.BeginOverlay(ui.HashString(0, "popover"), ui.OverlayPopup, anchor, ui.PlaceBelow)
	c.NextFont(d.semi)
	c.NextTextColor(p.label)
	c.Box(ui.DrawText, "Search##pop")
	c.NextFont(d.small)
	c.NextTextColor(p.label2)
	c.Box(ui.DrawText, "Drawn: the native views under it hide.##pop2")
	c.EndOverlay()
	if sig.Dismissed {
		d.popover = false
	}
}
