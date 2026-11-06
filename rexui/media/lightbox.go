package media

import (
	"strconv"

	"gonativeui/icon"
	"gonativeui/img"
	"gonativeui/rexui"
	"gonativeui/theme"
	"gonativeui/ui"
	"gonativeui/widget/base"
)

// LightboxState is a lightbox's state: whether it shows, the picture of the
// gallery it shows, and its zoom. Keep one per window; the zero value is
// closed.
type LightboxState struct {
	// Open: it shows; Index is the picture shown (0 to Count-1).
	Open  bool
	Index int

	zoom    bool
	pan     [2]float32 // the zoomed picture's offset from the centre, in pixels
	from    [2]float32 // pan when the drag began
	dragged bool       // the press moved: it pans, it does not toggle the zoom
}

// Show opens the lightbox at picture i, fitted.
func (st *LightboxState) Show(i int) {
	*st = LightboxState{Open: true, Index: max(i, 0)}
}

// Close closes it.
func (st *LightboxState) Close() { *st = LightboxState{} }

// Zoomed reports whether the picture shows at twice its fitted size.
func (st *LightboxState) Zoomed() bool { return st.zoom }

// LightboxRecipe is the lightbox's look, in points: T3 Code's media lightbox
// (a backdrop of ink at 80 %, the picture inside 48 of margin, a toolbar along
// the top) in a3-app's colours.
type LightboxRecipe struct {
	// Backdrop is the ink under the picture, Alpha its opacity.
	Backdrop uint32
	Alpha    float32
	// Margin keeps the fitted picture off the window's edges; Bar is the
	// toolbar's height.
	Margin, Bar float32
	// Fade is the fade in, in seconds (none under Reduce Motion).
	Fade float32
	// Zoom is the zoomed picture's size over its fitted one.
	Zoom float32
	// Button is the toolbar's and the arrows' buttons, light on the ink.
	Button rexui.ButtonRecipe
}

// LightboxLook is the lightbox's recipe.
var LightboxLook = LightboxRecipe{
	Backdrop: 0x14110FFF, Alpha: 0.8, Margin: 48, Bar: 52, Fade: 0.16, Zoom: 2,
	Button: rexui.ButtonRecipe{
		Fill: rexui.OfRGBA(0xFFFFFF1F), Label: rexui.OfRGBA(0xFFFFFFF2), FillInactive: rexui.OfRGBA(0xFFFFFF1F), LabelInactive: rexui.OfRGBA(0xFFFFFFF2),
		FillHover: rexui.OfRGBA(0xFFFFFF33), FillPressed: rexui.OfRGBA(0xFFFFFF47), LabelPressed: rexui.OfRGBA(0xFFFFFFFF),
		FillDisabled: rexui.OfRGBA(0xFFFFFF0F), LabelDisabled: rexui.OfRGBA(0xFFFFFF66),
		Rim: rexui.None, RimInactive: rexui.None, Highlight: rexui.None, Hover: base.HoverOn,
	},
}

// LightboxOpts are what a lightbox shows this frame.
type LightboxOpts struct {
	// Count is the gallery's size; Item the picture at LightboxState.Index.
	Count int
	Item  Item
	// Failed is the text in place of a picture that cannot show (Item.Err set
	// or the bytes not a picture); Loading the spinner's label.
	Failed, Loading string
	// Actions are the toolbar's buttons, before the close button.
	Actions []string
	// Close, Prev and Next name the buttons; their icons (0: a glyph).
	Close, Prev, Next             string
	CloseIcon, PrevIcon, NextIcon icon.ID
	// Lead keeps the toolbar's start clear of the window's own controls (macOS's
	// traffic lights over a full-size content view), in points.
	Lead float32
	// Still shows it at rest, without its fade (offscreen renders).
	Still bool
}

// LightboxSignal is what a lightbox reports.
type LightboxSignal struct {
	// Action is the index in Actions of the button clicked, -1 for none.
	Action int
	// Closed: Escape, a click on the backdrop or the close button closed it
	// (the state is closed already). Moved: ← or → or an arrow showed another
	// picture.
	Closed, Moved bool
}

// Lightbox builds the lightbox over the window while st.Open: the picture
// fitted inside the margins on an ink backdrop, the counter ("2 / 5") and
// the name, the app's actions and a close button along the top, and arrows
// on the sides for a gallery. A click on the picture toggles between fitted
// and twice that (a drag pans it then); ← and → move through the gallery;
// Escape, a click on the backdrop or the close button close it. Build it
// last in the window's root.
//
//	if v.box.Open {
//		it := v.items[v.box.Index]
//		sig := media.Lightbox(k, "Imagem", &v.box, media.LightboxOpts{Count: len(v.items), Item: it, Actions: acts})
//		if sig.Action == 0 { download(it) }
//	}
func Lightbox(k *base.Kit, label string, st *LightboxState, o LightboxOpts) LightboxSignal {
	sig := LightboxSignal{Action: -1}
	if !st.Open || o.Count <= 0 {
		return sig
	}
	c, r := k.Context(), k.Theme()
	rc := &LightboxLook
	sc := k.Scale()
	st.Index = min(max(st.Index, 0), o.Count-1)
	_, ident := ui.SplitLabel(label)
	key := ui.HashString(ui.HashString(c.ParentKey(), "lightbox"), ident)
	rate := theme.RateFor(rc.Fade)
	if k.ReduceMotion || r.Settings.ReduceMotion || o.Still {
		rate = theme.Instant
	}
	t := c.AnimateValueFrom(ui.KeyN(key, 1), 0, 1, rate)
	w, h := c.Size()
	W, H := float32(w), float32(h)
	c.NextSize(ui.Pct(1), ui.Pct(1))
	c.NextRole(ui.RoleDialog)
	c.NextSemName(label)
	closed := c.BeginOverlay(key, ui.OverlayModal, [4]float32{0, 0, W, H}, ui.PlaceCenter).Dismissed
	c.PushOpacity(t)
	// the keys: Escape closes, ← and → move
	switch {
	case c.TakeKey(ui.KeyEscape, 0):
		closed = true
	case c.TakeKey(ui.KeyLeft, 0) && st.Index > 0:
		st.Index--
		st.zoom, st.pan, sig.Moved = false, [2]float32{}, true
	case c.TakeKey(ui.KeyRight, 0) && st.Index < o.Count-1:
		st.Index++
		st.zoom, st.pan, sig.Moved = false, [2]float32{}, true
	}

	theme.At(c, 0, 0, W, H)
	c.NextBG(theme.WithAlpha(rc.Backdrop, rc.Alpha))
	c.NextAuditExempt(ui.AuditCovered.Mask())
	if c.Signal(c.BoxKeyed(base.Floating|ui.Clickable|ui.DrawBackground, ui.KeyN(key, 2), nil)).Clicked {
		closed = true
	}

	// the picture's room: inside the margins, under the toolbar
	m := theme.Snap(rc.Margin * sc)
	top := max(m, theme.Snap(rc.Bar*sc))
	aw, ah := max(W-2*m, 1), max(H-top-m, 1)
	cx, cy := m+aw/2, top+ah/2
	it := &o.Item
	ic := k.Images()
	var hd img.Handle
	var info img.Info
	ready, failed := false, it.Err != ""
	if ic != nil && it.Data != nil && !failed {
		hash := it.key()
		var s img.State
		hd, s = ic.Request(img.Key{Hash: hash, W: int32(aw), H: int32(ah)}, img.Bytes(it.Data))
		failed = s == img.Failed
		info, ready = ic.Info(hd)
		ready = ready && info.W > 0 && info.H > 0
	}
	switch {
	case failed:
		c.NextPos(m, cy-k.Pt(10))
		c.NextSize(ui.Px(aw), ui.Children(1))
		c.NextAlign(ui.AlignCenter)
		c.Push(c.Box(base.Floating, "failed"))
		rexui.Label(k, o.Failed+"###failed", rexui.LabelOpts{Color: rexui.OfRGBA(0xFFFFFFCC), Wrap: true})
		c.Pop()
	case !ready:
		s := theme.Snap(32 * sc)
		theme.At(c, cx-s/2, cy-s/2, s, s)
		c.Push(c.Box(base.Floating, "loading"))
		rexui.Spinner(k, o.Loading, rexui.SpinnerOpts{Still: k.ReduceMotion || o.Still})
		c.Pop()
	default:
		// fitted: its own size (one pixel a point, as a browser's), no larger than the room
		nw, nh := float32(info.W)*sc, float32(info.H)*sc
		f := min(1, aw/nw, ah/nh)
		fw, fh := theme.Snap(nw*f), theme.Snap(nh*f)
		dw, dh := fw, fh
		show := hd
		if st.zoom {
			dw, dh = theme.Snap(fw*rc.Zoom), theme.Snap(fh*rc.Zoom)
			if zh, zs := ic.Request(img.Key{Hash: it.key(), W: int32(dw), H: int32(dh)}, img.Bytes(it.Data)); zs == img.Ready {
				show = zh
			}
			lx, ly := max((dw-aw)/2, 0), max((dh-ah)/2, 0)
			st.pan[0], st.pan[1] = min(max(st.pan[0], -lx), lx), min(max(st.pan[1], -ly), ly)
		} else {
			st.pan = [2]float32{}
		}
		theme.At(c, theme.Snap(cx-dw/2+st.pan[0]), theme.Snap(cy-dh/2+st.pan[1]), dw, dh)
		cur := ui.CursorPointer
		if st.zoom {
			cur = ui.CursorGrab
		}
		c.NextCursor(cur)
		c.NextRole(ui.RoleImage)
		c.NextSemName(it.Name)
		pic := c.BoxKeyed(base.Floating|ui.Clickable, ui.KeyN(key, 3), nil)
		c.Push(pic)
		k.NextWidth(ui.Pct(1))
		k.NextHeight(ui.Pct(1))
		k.Image(1, show, ui.FitFill, 0, "")
		c.Pop()
		ps := c.Signal(pic)
		if ps.Pressed {
			st.from, st.dragged = st.pan, false
		}
		if ps.Dragging || ps.Released {
			d := ps.DragDelta
			if d[0]*d[0]+d[1]*d[1] > 16*sc*sc {
				st.dragged = true
			}
			if st.zoom && st.dragged {
				st.pan = [2]float32{st.from[0] + d[0], st.from[1] + d[1]}
				c.RequestFrame()
			}
		}
		if ps.Clicked && !st.dragged {
			st.zoom = !st.zoom
			st.pan = [2]float32{}
		}
	}

	// the arrows
	if o.Count > 1 {
		bs := theme.Snap(36 * sc)
		arrow := func(n uint64, x float32, ic icon.ID, name, glyph string, on bool) bool {
			theme.At(c, x, cy-bs/2, bs, bs)
			c.Push(c.BoxKeyed(base.Floating, ui.KeyN(key, n), nil))
			var s base.Signal
			if ic != 0 {
				s = rexui.IconButton(k, ic, name, rexui.IconButtonOpts{Recipe: &rc.Button, Round: true, Size: theme.SizeLarge})
			} else {
				s = rexui.Button(k, glyph+"###"+name, rexui.ButtonOpts{Recipe: &rc.Button})
			}
			c.Pop()
			return on && s.Clicked
		}
		if arrow(4, theme.Snap(12*sc), o.PrevIcon, o.Prev, "‹", st.Index > 0) {
			st.Index--
			st.zoom, st.pan, sig.Moved = false, [2]float32{}, true
		}
		if arrow(5, W-theme.Snap(12*sc)-bs, o.NextIcon, o.Next, "›", st.Index < o.Count-1) {
			st.Index++
			st.zoom, st.pan, sig.Moved = false, [2]float32{}, true
		}
	}

	// the toolbar: the counter and the name, the actions, the close button
	bar := theme.Snap(rc.Bar * sc)
	pad := theme.Snap(12 * sc)
	theme.At(c, 0, 0, W, bar)
	c.NextAxis(ui.AxisX)
	c.NextCross(ui.CrossCenter)
	c.NextPadding(pad+theme.Snap(o.Lead*sc), 0, pad, 0)
	c.NextGap(8 * sc)
	c.Push(c.BoxKeyed(base.Floating, ui.KeyN(key, 6), nil))
	if o.Count > 1 {
		f := c.Fmt()
		f.Int(st.Index + 1).Str(" / ").Int(o.Count)
		rexui.LabelBytes(k, f.Bytes(), rexui.LabelOpts{Color: rexui.OfRGBA(0xFFFFFFF2), MetaPt: theme.MetaPt + 1})
	}
	c.NextShrinkFirst()
	k.NextWidth(ui.Grow(1))
	rexui.Label(k, it.Name+"###name", rexui.LabelOpts{Color: rexui.OfRGBA(0xFFFFFFB3), Truncate: true})
	for i, a := range o.Actions {
		if rexui.Button(k, a+"###a"+strconv.Itoa(i), rexui.ButtonOpts{Recipe: &rc.Button}).Clicked {
			sig.Action = i
		}
	}
	var cs base.Signal
	if o.CloseIcon != 0 {
		cs = rexui.IconButton(k, o.CloseIcon, o.Close, rexui.IconButtonOpts{Recipe: &rc.Button, Round: true})
	} else {
		cs = rexui.Button(k, "×###"+o.Close, rexui.ButtonOpts{Recipe: &rc.Button})
	}
	if cs.Clicked {
		closed = true
	}
	c.Pop()

	c.PopOpacity()
	c.EndOverlay()
	if closed {
		st.Close()
		sig.Closed = true
		c.RequestFrame()
	}
	return sig
}
