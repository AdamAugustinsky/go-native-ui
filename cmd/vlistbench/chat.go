package main

import (
	"time"

	"gonativeui/app"
	"gonativeui/internal/sysinfo"
	"gonativeui/text"
	"gonativeui/ui"
	"gonativeui/widget/base"
)

// Geometry in points; the Builder converts with Context.Pt.
const (
	defItems  = 1_000_000
	headerPt  = 40
	padPt     = 10
	bubblePad = 8
	gapPt     = 4
	barPt     = 6
	estPt     = 56 // the estimate: a two-line message
	historyN  = 200
	historyAt = 40 // load history when the first built item is this close to the top
	imageN    = 8  // one message in imageN has an image
	loadAfter = 24 // frames an image takes to "load"

	maxListSamples = 8192 // frames whose list work is kept
)

// Colours (0xRRGGBBAA).
const (
	colBG     = 0x17181BFF
	colHeader = 0x232428FF
	colMine   = 0x2F5FD0FF
	colTheirs = 0x2C2E33FF
	colText   = 0xE6E7EAFF
	colDim    = 0x9DA0A8FF
	colImage  = 0x4A5060FF
	colThumb  = 0xFFFFFF40
)

var words = [...]string{
	"the", "list", "keeps", "its", "place", "while", "history", "loads", "above",
	"and", "new", "messages", "arrive", "below", "a", "million", "rows", "of",
	"mixed", "heights", "scroll", "without", "jumps", "anchor", "by", "id",
	"images", "grow", "when", "they", "load", "stick", "to", "bottom", "reply",
	"jump", "search", "hit", "deleted", "for", "everyone", "backfill", "ok",
	"sounds", "good", "see", "you", "tomorrow", "👍", "thanks!",
}

// mix is a splitmix64 finalizer, the pseudo-random source of a message.
func mix(x uint64) uint64 {
	x = x*0x9E3779B97F4A7C15 + 0xD1B54A32D192ED03
	x = (x ^ x>>30) * 0xBF58476D1CE4E5B9
	x = (x ^ x>>27) * 0x94D049BB133111EB
	return x ^ x>>31
}

// The messages are synthetic: everything about one derives from its id, so a
// million of them cost only the list's ids and heights. Ids grow with time:
// the loaded history is [oldest, newest], older history is below it.
const firstID = 1 << 40

func mine(id uint64) bool { return mix(id)&1 == 0 }

func hasImage(id uint64) bool { return mix(id+7)%imageN == 0 }

func imagePt(id uint64) float32 { return float32(80 + mix(id+11)%160) }

// maxWords is the most words a message has.
const maxWords = 60

// msgText builds the text of message id into the frame's scratch: 1 to
// maxWords words, so from one line to about eight.
func msgText(c *ui.Context, id uint64) []byte {
	h := mix(id + 3)
	n := 1 + int(h%7)
	if h>>8%5 == 0 {
		n += int(h >> 16 % (maxWords - 6))
	}
	f := c.Fmt()
	for w := 0; w < n; w++ {
		if w > 0 {
			f.Byte(' ')
		}
		f.Str(words[mix(id*64+uint64(w))%uint64(len(words))])
	}
	return f.Bytes()
}

// loads remembers when an image message was first built, so its image
// "loads" a little later and the row grows: a small direct-mapped table,
// pointer-free (an evicted entry loads again).
type load struct {
	id    uint64
	frame int32
}

// chat is a messenger timeline of a million messages on base.VList.
type chat struct {
	a  *app.App
	st base.VListState
	o  base.VListOpts

	font      text.FontKey
	fontScale float32

	n                int
	oldest, newest   uint64
	historyLeft      int
	buf              [historyN]uint64
	loads            [4096]load
	frame            int32
	jumpTop, jumpEnd bool
	jumpTo           int // an index to jump to (a reply), -1 none
	backfill         uint64

	// the checks: the box of the item at the top and its place, before an
	// edit above it, and the last item's box while pinned
	watchID   uint64
	watchY    float32 // its top, plus the scroll the frame of the edit applies
	watchBox  ui.ID
	watching  bool
	ids       [512]uint64 // the items built in the last frame and their boxes
	boxes     [512]ui.ID
	nb        int
	lastBox   ui.ID
	listBox   ui.ID
	pinned    bool // the last frame was stuck to the bottom with the last item built
	jumps     int  // edits above the anchor that moved it
	unpinned  int  // frames stuck to the bottom whose last item did not end at the bottom
	arrivals  int
	histLoads int
	rebuilds  int
	wasPend   bool

	// what the last Build did (the bench and the tests read these)
	v          base.VList
	counting   bool
	built      int
	buildCalls int
	builtMax   int
	editMax    time.Duration // the most a frame spent in edits and BeginVList
	editSum    time.Duration
	// listUs holds, per counted frame, the list's own work in µs: the edits,
	// BeginVList (with a rebuild's slice), the Item calls and EndVList; the
	// items' contents, layout and drawing are the frame's (BenchReport).
	listUs []float64

	script func(frame int, in *ui.Input) bool
	pre    func(c *chat) // the script's edits, run in Build before the list
}

func newChat(n int) *chat {
	ch := &chat{n: n, jumpTo: -1, listUs: make([]float64, 0, maxListSamples)}
	ch.o = base.VListOpts{Estimate: estPt, StickToBottom: true}
	ch.st.Reserve(n + n/2)
	ids := make([]uint64, n)
	for i := range ids {
		ids[i] = firstID + uint64(i)
	}
	ch.st.Append(ids)
	ch.oldest, ch.newest = firstID, firstID+uint64(n)-1
	ch.historyLeft = 1 << 30
	return ch
}

// Init creates the fonts and sizes the caches for scrolling through endless
// new strings: the run cache is reserved to its limits so that, once warm,
// shaping a new message allocates nothing, and the paragraph cache, as a chat
// app declares it, for many screens of messages (about 7 MiB).
func (ch *chat) Init(a *app.App) error {
	ch.a = a
	a.UI.Reserve(4096)
	a.Text.ReserveParas(text.ParaBudget{Paras: 4096, Glyphs: 1 << 18, Text: 1 << 18})
	a.Text.ReserveRuns(text.MaxCachedRuns, text.MaxCachedGlyphs, text.MaxCachedTextBytes)
	return nil
}

// Script implements app.Scripter.
func (ch *chat) Script(frame int, in *ui.Input) bool {
	if ch.script == nil {
		return true
	}
	return ch.script(frame, in)
}

func (ch *chat) setFonts(scale float32) {
	ch.font = ch.a.Text.Font(text.FontDesc{SizePt: 13, Weight: 400, Scale: scale})
	ch.fontScale = scale
	// The text system's shaping scratch grows to the longest run it has
	// shaped: shape the longest message there can be once, now, rather than
	// whenever scrolling first meets a longer one.
	var long []byte
	for w := 0; w < maxWords; w++ {
		long = append(long, "everyone "...)
	}
	ch.a.Text.Shape(ch.font, long)
}

// arrive appends a new message, as the network delivers one.
func (ch *chat) arrive() {
	ch.newest++
	ch.buf[0] = ch.newest
	ch.st.Append(ch.buf[:1])
	ch.arrivals++
}

// loadHistory prepends the next older page of messages.
func (ch *chat) loadHistory() {
	for i := range ch.buf {
		ch.buf[i] = ch.oldest - historyN + uint64(i)
	}
	ch.oldest -= historyN
	ch.st.Prepend(ch.buf[:])
	ch.histLoads++
}

// watch remembers where the item at the top is before an edit above it, moved
// by the scrolling ui applied to the list this frame.
func (ch *chat) watch(c *ui.Context) {
	id, _ := ch.st.Anchor()
	if b, ok := ch.boxOf(id); ok {
		ch.watchID, ch.watchBox, ch.watching = id, b, true
		ch.watchY = c.Rect(b)[1] - c.Signal(ch.v.Body).Scroll[1]
	}
}

// boxOf is the item box built for id in the last frame.
func (ch *chat) boxOf(id uint64) (ui.ID, bool) {
	for i := range ch.ids {
		if ch.ids[i] == id {
			return ch.boxes[i], true
		}
	}
	return 0, false
}

// Build is one frame of the UI.
func (ch *chat) Build(c *ui.Context) {
	if ch.fontScale != c.Scale() {
		ch.setFonts(c.Scale())
	}
	ch.frame++
	ch.check(c)

	c.PushFont(ch.font)
	c.PushTextColor(colText)
	c.NextSize(ui.Pct(1), ui.Pct(1))
	c.NextBG(colBG)
	c.Open(ui.DrawBackground, "root")
	ch.header(c)

	t0 := sysinfo.ThreadCPUNs()
	if p := ch.pre; p != nil {
		ch.pre = nil
		p(ch)
	}
	if ch.v.First < historyAt && ch.v.Len > 0 && ch.historyLeft > 0 && !ch.st.Pending() {
		ch.watch(c)
		ch.loadHistory()
	}
	if ch.jumpTop {
		ch.st.ScrollTo(0, base.AlignStart)
		ch.jumpTop = false
	}
	if ch.jumpEnd {
		ch.st.ScrollTo(ch.st.Len()-1, base.AlignEnd)
		ch.jumpEnd = false
	}
	if ch.jumpTo >= 0 {
		ch.st.ScrollTo(min(ch.jumpTo, ch.st.Len()-1), base.AlignCenter)
		ch.jumpTo = -1
	}
	if p := ch.st.Pending(); p && !ch.wasPend {
		ch.rebuilds++
		ch.wasPend = true
	} else if !p {
		ch.wasPend = false
	}

	c.NextSize(ui.Pct(1), ui.Pct(1))
	c.NextAxis(ui.AxisX)
	c.Open(0, "body")
	c.NextSize(ui.Pct(1), ui.Pct(1))
	v := base.BeginVList(c, "transcript", &ch.st, ch.o)
	el := time.Duration(sysinfo.ThreadCPUNs() - t0)
	if ch.counting {
		ch.editMax = max(ch.editMax, el)
		ch.editSum += el
	}
	ch.listBox = v.Outer
	ch.nb = 0
	var itemNs time.Duration // wall time: the calls are far shorter than a thread-clock tick
	for i := v.First; i < v.Last; i++ {
		t := time.Now()
		id := v.Item(i)
		itemNs += time.Since(t)
		ch.message(c, id)
		if ch.nb < len(ch.ids) {
			ch.ids[ch.nb], ch.boxes[ch.nb] = id, c.Parent()
			ch.nb++
		}
		if i == v.Len-1 {
			ch.lastBox = c.Parent()
		}
	}
	ch.pinned = ch.st.AtEnd() && v.Last == v.Len && v.Len > 0
	t := time.Now()
	base.EndVList(&v)
	itemNs += time.Since(t)
	if ch.counting && len(ch.listUs) < cap(ch.listUs) {
		ch.listUs = append(ch.listUs, float64((el+itemNs).Nanoseconds())/1e3)
	}
	ch.v = v
	ch.scrollbar(c, v)
	if ch.counting {
		n := v.Last - v.First
		ch.buildCalls++
		ch.built += n
		ch.builtMax = max(ch.builtMax, n)
	}
	c.Close() // body
	c.Close() // root
	c.PopTextColor()
	c.PopFont()
}

// check compares the last frame's layout with what the edits promised: the
// item that was at the top before an edit above it is still at its place,
// and a list stuck to the bottom ends at the bottom.
func (ch *chat) check(c *ui.Context) {
	if ch.watching {
		ch.watching = false
		if c.Rect(ch.watchBox)[1] != ch.watchY {
			if id, _ := ch.st.Anchor(); id == ch.watchID {
				ch.jumps++
			}
		}
	}
	if ch.pinned && ch.lastBox != 0 {
		if c.Rect(ch.lastBox)[3] != c.Rect(ch.listBox)[3] {
			ch.unpinned++
		}
	}
}

func (ch *chat) header(c *ui.Context) {
	c.NextSize(ui.Pct(1), ui.Px(c.Pt(headerPt)))
	c.NextAxis(ui.AxisX)
	c.NextPadding(c.Pt(14), 0, c.Pt(14), 0)
	c.NextBG(colHeader)
	c.NextBorder(0x00000060, 1)
	c.Open(ui.DrawBackground|ui.DrawBorder, "header")
	c.NextSize(ui.Pct(1), ui.Pct(1))
	c.Box(ui.DrawText, "Timeline")
	c.NextSize(ui.Px(c.Pt(160)), ui.Pct(1))
	c.NextAlign(ui.AlignRight)
	c.NextTextColor(colDim)
	f := c.Fmt().Int(ch.st.Len()).Str(" messages")
	c.BoxKeyed(ui.DrawText, ui.KeyN(c.ParentKey(), 1), f.Bytes())
	c.Close()
}

// message builds one row: a bubble on the left (theirs) or the right
// (mine), its text wrapped, and maybe an image under it whose height is known
// only once it has loaded.
func (ch *chat) message(c *ui.Context, id uint64) {
	c.NextSize(ui.Pct(1), ui.Children(1))
	c.NextAxis(ui.AxisX)
	c.NextPadding(c.Pt(padPt), c.Pt(gapPt/2), c.Pt(padPt), c.Pt(gapPt/2))
	c.Open(0, "row")
	if mine(id) {
		c.NextSize(ui.Pct(0.3), ui.Px(0))
		c.Box(0, "pad")
	}
	bg := uint32(colTheirs)
	if mine(id) {
		bg = colMine
	}
	c.NextSize(ui.Pct(0.7), ui.Children(1))
	c.NextBG(bg)
	c.NextRadius(c.Pt(12))
	c.NextPad(c.Pt(bubblePad))
	c.NextGap(c.Pt(6))
	c.Open(ui.DrawBackground, "bubble")
	c.NextSize(ui.Pct(1), ui.TextSize(0))
	c.BoxKeyed(ui.DrawText|ui.WrapText, ui.KeyN(c.ParentKey(), 0), msgText(c, id))
	if hasImage(id) {
		h := float32(0)
		if ch.loaded(id) {
			h = c.Pt(imagePt(id))
		}
		c.NextSize(ui.Pct(1), ui.Px(h))
		c.NextBG(colImage)
		c.NextRadius(c.Pt(8))
		c.Box(ui.DrawBackground, "img")
	}
	c.Close() // bubble
	c.Close() // row
}

// loaded reports whether the image of id has loaded: loadAfter frames after
// the message was first built.
func (ch *chat) loaded(id uint64) bool {
	e := &ch.loads[mix(id)%uint64(len(ch.loads))]
	if e.id != id {
		*e = load{id: id, frame: ch.frame}
		return false
	}
	return ch.frame-e.frame >= loadAfter
}

// scrollbar draws a thumb from the list's scroll extent (the estimated total).
func (ch *chat) scrollbar(c *ui.Context, v base.VList) {
	content, view := c.ScrollExtent(v.Body)
	_, sy := c.Scroll(v.Body)
	w := c.Pt(barPt + 4)
	c.NextSize(ui.Px(w), ui.Pct(1))
	track := c.Box(0, "track")
	if content[1] <= view[1] || view[1] <= 0 {
		return
	}
	th := max(view[1]*view[1]/content[1], c.Pt(28))
	top := (view[1] - th) * float32(min(max(sy/float64(content[1]-view[1]), 0), 1))
	c.Push(track)
	c.NextSize(ui.Px(c.Pt(barPt)), ui.Px(th))
	c.NextPos(c.Pt(2), top)
	c.NextBG(colThumb)
	c.NextRadius(c.Pt(barPt / 2))
	c.Box(ui.DrawBackground|ui.FloatingX|ui.FloatingY, "thumb")
	c.Pop()
}

// warmRebuild runs a long edit and its inverse to the end before the session,
// so that the back buffer (allocated the first time a list needs one) exists
// before the measured frames.
func (ch *chat) warmRebuild() {
	n := ch.st.Len()
	for _, m := range [2][2]int{{n / 4, 3 * n / 4}, {3 * n / 4, n / 4}} {
		ch.st.Move(m[0], 1, m[1])
		for f := uint32(1); ch.st.Pending(); f++ {
			ch.st.Sync(1<<31 + f)
		}
	}
}
