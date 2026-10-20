//go:build darwin

package platform

import (
	"fmt"
	"strings"
	"testing"

	"gonativeui/internal/ffi"
	"gonativeui/internal/objc"
)

// fakeAX is an AXSource over a fixed tree.
type fakeAX struct {
	t       *AXTree
	exports int
}

func (f *fakeAX) AXExport(dst *AXTree) bool {
	f.exports++
	dst.Nodes = append(dst.Nodes[:0], f.t.Nodes...)
	dst.Bytes = append(dst.Bytes[:0], f.t.Bytes...)
	dst.Announce, dst.AnnounceSeq = f.t.Announce, f.t.AnnounceSeq
	return true
}
func (f *fakeAX) AXLine(key uint64, off int) (int, bool) { return off / 4, true } // 4 bytes a line
func (f *fakeAX) AXLineRange(key uint64, line int) (int, int, bool) {
	return line * 4, line*4 + 4, line < 2
}
func (f *fakeAX) AXBounds(key uint64, from, to int) ([4]float32, bool) {
	return [4]float32{float32(from * 10), 0, float32(to * 10), 20}, true
}
func (f *fakeAX) AXOffset(key uint64, x, y float32) (int, bool) { return int(x / 10), true }
func (f *fakeAX) AXVisible(key uint64) (int, int, bool)         { return 2, 6, true }
func (f *fakeAX) AXHelp(key uint64, dst []byte) []byte {
	if key == 30 {
		return append(dst, "Sends the message"...)
	}
	return dst
}

// goString is the text of NSString s.
func goString(s objc.ID) string { return string(objc.AppendUTF8(nil, s)) }

// arrayObjs is the objects of NSArray a.
func arrayObjs(a objc.ID) []objc.ID {
	n := int(objc.Send(a, objc.Sel("count")))
	out := make([]objc.ID, n)
	for i := range out {
		out[i] = objc.Send1(a, objc.Sel("objectAtIndex:"), uintptr(i))
	}
	return out
}

func axStrAttr(o objc.ID, sel string) string { return goString(objc.Send(o, objc.Sel(sel))) }

// axFrame is accessibilityFrame of o.
func axFrame(o objc.ID) objc.Rect { return objc.SendRect(o, objc.Sel("accessibilityFrame")) }

// msg sends sel with integer arguments to o and returns the frame (the
// selector is looked up first: Sel uses the frame too).
func msg(o objc.ID, sel string, args ...uintptr) *ffi.Frame {
	s := objc.Sel(sel)
	f := objc.F.Reset().Arg(uintptr(o)).Arg(uintptr(s))
	for _, a := range args {
		f.Arg(a)
	}
	return f.Call(objc.MsgSend)
}

func axRange(o objc.ID, sel string) (uintptr, uintptr) {
	f := msg(o, sel)
	return f.R0(), f.R1()
}

// The bridge through the NSAccessibility protocol, in process: nothing
// happens before an assistive technology asks; the first question activates
// it; elements have the roles, labels, values, text ranges, frames and
// children of their nodes (the hosted view among them, under its element);
// actions come back as events; a modal hides what is beneath it; an
// unchanged frame allocates nothing, and a change is posted on the elements
// an assistive technology has.
func TestAXBridgeInProcess(t *testing.T) {
	var errs []string
	fail := func(f string, a ...any) { errs = append(errs, fmt.Sprintf(f, a...)) }
	onMain(func() {
		w, err := Open(Options{Title: "ax", Width: 400, Height: 300, Hidden: true})
		if err != nil {
			fail("open: %v", err)
			return
		}
		defer w.Close()
		pool := objc.F.Call0(objc.PoolPush)
		defer objc.F.Call1(objc.PoolPop, pool)

		host, err := w.NewHost(HostView, HostOptions{})
		if err != nil {
			fail("host: %v", err)
			return
		}
		w.SetHosts([]HostRegion{{ID: host, Rect: [4]float32{0, 100, 100, 140}, Clip: [4]float32{0, 100, 100, 140}}})
		w.FlushShell()

		b := newAXBuild()
		g := b.add(0, 10, AXRoleGroup, "Composer")
		g.Bounds = [4]float32{0, 0, 400, 200}
		f := b.add(1, 20, AXRoleTextField, "Message")
		b.value(f, "abcdefgh")
		f.Text, f.TextSel, f.Actions, f.Bounds = AXTextField, [2]int32{2, 5}, AXFocus|AXSetValue, [4]float32{10, 10, 210, 30}
		b.add(1, 25, AXRoleHost, "").Host = uint32(host)
		bt := b.add(1, 30, AXRoleButton, "Send")
		bt.Actions, bt.Bounds = AXPress|AXFocus, [4]float32{220, 10, 300, 30}
		sl := b.add(0, 40, AXRoleSlider, "Volume")
		sl.Range, sl.Actions = [4]float64{0, 10, 3, 1}, AXIncrement|AXDecrement|AXSetValue
		src := &fakeAX{t: &b.t}
		if err := w.SetAXSource(src); err != nil {
			fail("SetAXSource: %v", err)
			return
		}

		// Lazy: nothing before an assistive technology asks.
		w.SyncAX()
		if w.AXActive() || src.exports != 0 || ax.elem != 0 && len(ax.core.slots) != 0 {
			fail("before activation: active %v, %d exports, %d slots", w.AXActive(), src.exports, len(ax.core.slots))
		}
		if n := testing.AllocsPerRun(100, w.SyncAX); n != 0 {
			fail("inactive SyncAX: %v allocations", n)
		}

		top := arrayObjs(objc.Send(w.view, selAXChildren))
		if !w.AXActive() || src.exports != 1 || len(top) != 2 {
			fail("activation: active %v, %d exports, %d top elements", w.AXActive(), src.exports, len(top))
			return
		}
		group, slider := top[0], top[1]
		if r := axStrAttr(group, "accessibilityRole"); r != "AXGroup" {
			fail("group role %q", r)
		}
		if l := axStrAttr(group, "accessibilityLabel"); l != "Composer" {
			fail("group label %q", l)
		}
		if p := objc.Send(group, objc.Sel("accessibilityParent")); p == 0 {
			fail("group has no parent")
		}
		kids := arrayObjs(objc.Send(group, objc.Sel("accessibilityChildren")))
		if len(kids) != 3 {
			fail("group has %d children, want 3 (field, host, button)", len(kids))
			return
		}
		field, hostObj, button := kids[0], kids[1], kids[2]
		hv := objc.ID(w.HostView(host))
		if hostObj != hv {
			fail("second child is %#x, want the host's view %#x", hostObj, hv)
		}
		if p := objc.Send(hv, objc.Sel("accessibilityParent")); p != group {
			fail("the host view's parent is %#x, want the group %#x", p, group)
		}
		if r, v := axStrAttr(field, "accessibilityRole"), axStrAttr(field, "accessibilityValue"); r != "AXTextField" || v != "abcdefgh" {
			fail("field: role %q value %q", r, v)
		}
		if loc, n := axRange(field, "accessibilitySelectedTextRange"); loc != 2 || n != 3 {
			fail("field selection {%d, %d}, want {2, 3}", loc, n)
		}
		if s := axStrAttr(field, "accessibilitySelectedText"); s != "cde" {
			fail("selected text %q", s)
		}
		s := objc.ID(msg(field, "accessibilityStringForRange:", 1, 4).R0())
		if goString(s) != "bcde" {
			fail("stringForRange {1,4} %q", goString(s))
		}
		if l := msg(field, "accessibilityLineForIndex:", 5).R0(); l != 1 {
			fail("line of index 5: %d, want 1", l)
		}
		if loc, n := rangeForLine(field, 1); loc != 4 || n != 4 {
			fail("range of line 1 {%d, %d}, want {4, 4}", loc, n)
		}
		fr := axFrame(field)
		sc := float64(w.scale)
		if d := fr.W - 200/sc; d < -0.01 || d > 0.01 || fr.H < 20/sc-0.01 || fr.H > 20/sc+0.01 {
			fail("field frame %+v, want %gx%g points", fr, 200/sc, 20/sc)
		}
		wf := objc.SendRect(w.win, selFrame)
		if fr.X < wf.X || fr.X+fr.W > wf.X+wf.W || fr.Y < wf.Y || fr.Y+fr.H > wf.Y+wf.H {
			fail("field frame %+v is not in the window's %+v", fr, wf)
		}
		if h := axStrAttr(button, "accessibilityHelp"); h != "Sends the message" {
			fail("button help %q", h)
		}
		if !objc.SendBool(button, objc.Sel("isAccessibilityEnabled")) {
			fail("button disabled")
		}
		if v := objc.F.Reset().Arg(uintptr(objc.Send(slider, objc.Sel("accessibilityValue")))).Arg(uintptr(selDoubleValue)).Call(objc.MsgSend).F64(0); v != 3 {
			fail("slider value %g", v)
		}
		if !objc.SendBool1(slider, objc.Sel("isAccessibilitySelectorAllowed:"), uintptr(objc.Sel("accessibilityPerformIncrement"))) ||
			objc.SendBool1(button, objc.Sel("isAccessibilitySelectorAllowed:"), uintptr(objc.Sel("accessibilityPerformIncrement"))) ||
			objc.SendBool1(button, objc.Sel("isAccessibilitySelectorAllowed:"), uintptr(objc.Sel("setAccessibilityLabel:"))) {
			fail("allowed selectors: increment on the slider and not the button; no setter of NSAccessibilityElement's own")
		}
		names := arrayObjs(objc.Send(button, objc.Sel("accessibilityActionNames")))
		if len(names) != 1 || goString(names[0]) != "AXPress" {
			fail("button actions %d", len(names))
		}

		// Actions come back as events.
		w.q.reset()
		objc.SendBool(button, objc.Sel("accessibilityPerformPress"))
		objc.Send1(field, objc.Sel("setAccessibilityFocused:"), 1)
		objc.Send(slider, objc.Sel("accessibilityPerformIncrement"))
		objc.Send1(button, objc.Sel("accessibilityPerformAction:"), uintptr(objc.Str("AXScrollToVisible"))) // not offered: nothing
		var got []string
		for _, e := range w.q.ev {
			if e.Kind == EvAccessibility {
				got = append(got, fmt.Sprintf("%d:%d", e.Tag, e.Code))
			}
		}
		if g := strings.Join(got, " "); g != "30:1 20:2 40:4" {
			fail("action events %q, want 30:1 20:2 40:4", g)
		}

		// Hit testing: the button's centre, in screen points.
		bf := axFrame(button)
		hit := objc.ID(objc.F.Reset().Arg(uintptr(w.view)).Arg(uintptr(selAXHitTest)).ArgPoint(bf.X+bf.W/2, bf.Y+bf.H/2).Call(objc.MsgSend).R0())
		if hit != button {
			fail("hit test at the button's centre found %#x, want %#x", hit, button)
		}

		// An unchanged frame allocates nothing; a change is posted.
		if n := testing.AllocsPerRun(100, w.SyncAX); n != 0 {
			fail("active SyncAX of an unchanged tree: %v allocations", n)
		}
		posted := ax.posted
		b.t.Nodes[b.idx(40)].Range[2] = 4
		w.SyncAX()
		if ax.posted != posted+1 {
			fail("a slider value change posted %d notifications, want 1", ax.posted-posted)
		}
		// An announcement is posted with its frame.
		posted = ax.posted
		b.t.Announce, b.t.AnnounceSeq = [2]uint32{uint32(len(b.t.Bytes)), 4}, 1
		b.t.Bytes = append(b.t.Bytes, "Sent"...)
		w.SyncAX()
		b.t.Announce = [2]uint32{}
		if ax.posted != posted+1 {
			fail("an announcement posted %d notifications, want 1", ax.posted-posted)
		}

		// A modal overlay: the view's children are the dialog alone.
		d := b.add(0, 50, AXRoleDialog, "Delete?")
		d.State |= AXModal
		b.add(b.idx(50), 51, AXRoleButton, "Delete").Actions = AXPress
		w.SyncAX()
		top = arrayObjs(objc.Send(w.view, selAXChildren))
		if len(top) != 1 || axStrAttr(top[0], "accessibilitySubrole") != "AXApplicationDialog" {
			fail("with a modal, the view has %d children", len(top))
		}
		// The window's own children reach the elements through the view.
		var found bool
		for _, o := range arrayObjs(objc.Send(w.win, selAXChildren)) {
			if o == top[0] {
				found = true
			}
		}
		if !found {
			fail("the window's accessibilityChildren do not include the dialog's element")
		}
	})
	for _, e := range errs {
		t.Error(e)
	}
}

func rangeForLine(o objc.ID, line uintptr) (uintptr, uintptr) {
	f := msg(o, "accessibilityRangeForLine:", line)
	return f.R0(), f.R1()
}

// axSelf is the accessibility object of view v (a control's is its cell).
func axSelf(v objc.ID) objc.ID {
	if d := objc.ID(objc.F.Call1(axk.unignoredDescendant, uintptr(v))); d != 0 {
		return d
	}
	return v
}

// axReach walks the accessibility children from o (at most depth levels and
// 200 objects) and reports whether it reaches want, and whether it reaches
// one of the window's own elements or views (a cycle back into the app).
func axReach(w *Window, o, want objc.ID, depth int) (found, cycle bool) {
	seen := 0
	var walk func(o objc.ID, d int)
	walk = func(o objc.ID, d int) {
		if o == 0 || d > depth || seen > 200 {
			return
		}
		seen++
		if o == want {
			found = true
		}
		if o == w.view || o == w.content || objc.SendBool1(o, selIsKindOfClass, uintptr(ax.elem)) {
			cycle = true
			return
		}
		for _, ch := range arrayObjs(objc.Send(o, selAXChildren)) {
			walk(ch, d+1)
		}
	}
	walk(o, 0)
	return found, cycle
}

// Native views inside the app's tree: a glass host's controls are reached as
// themselves (the glass's content view keeps NSView's answers); a host box
// with a role of its own holds its native view as its last child, and a
// native view that took the keyboard is the focused element whatever the
// tree says. Also the native list and text properties the tree carries: the
// row count of a virtual list and a text's visible range.
func TestAXBridgeNativeChildren(t *testing.T) {
	var errs []string
	fail := func(f string, a ...any) { errs = append(errs, fmt.Sprintf(f, a...)) }
	onMain(func() {
		w, err := Open(Options{Title: "ax", Width: 400, Height: 300, Hidden: true})
		if err != nil {
			fail("open: %v", err)
			return
		}
		defer w.Close()
		pool := objc.F.Call0(objc.PoolPush)
		defer objc.F.Call1(objc.PoolPop, pool)

		glass, err := w.NewHost(HostGlass, HostOptions{Radius: 8})
		if err != nil {
			fail("glass host: %v", err)
			return
		}
		btn := newViewOf(objc.Cls("NSButton"), 4, 4, 60, 24)
		objc.Send1(btn, selSetTitle, uintptr(objc.Str("Native")))
		objc.Send1(objc.ID(w.HostView(glass)), selAddSubview, uintptr(btn))
		objc.Send(btn, selReleaseObj)
		tf := newViewOf(objc.Cls("NSTextField"), 0, 0, 100, 24)
		edit, err := w.NewHost(HostView, HostOptions{View: uintptr(tf)})
		objc.Send(tf, selReleaseObj)
		if err != nil {
			fail("view host: %v", err)
			return
		}
		w.SetHosts([]HostRegion{
			{ID: glass, Rect: [4]float32{0, 100, 100, 140}, Clip: [4]float32{0, 100, 100, 140}},
			{ID: edit, Rect: [4]float32{0, 150, 100, 174}, Clip: [4]float32{0, 150, 100, 174}},
		})
		w.FlushShell()

		b := newAXBuild()
		b.add(0, 10, AXRoleHost, "").Host = uint32(glass)
		ed := b.add(0, 20, AXRoleGroup, "Editor")
		ed.Host = uint32(edit)
		b.add(0, 30, AXRoleList, "Messages")
		r := b.add(b.idx(30), 31, AXRoleListItem, "Message 500")
		r.Pos = [2]int32{500, 1_000_000}
		f := b.add(0, 40, AXRoleTextField, "Search")
		b.value(f, "abcdefgh")
		f.Text = AXTextField
		src := &fakeAX{t: &b.t}
		if err := w.SetAXSource(src); err != nil {
			fail("SetAXSource: %v", err)
			return
		}
		top := arrayObjs(objc.Send(w.view, selAXChildren))
		if len(top) != 4 {
			fail("%d top elements, want 4", len(top))
			return
		}

		// The glass: its button, and no way back into the app's tree.
		found, cycle := axReach(w, top[0], axSelf(btn), 4)
		if !found || cycle {
			fail("from the glass host: the button reached %v, a cycle %v", found, cycle)
		}
		if kids := arrayObjs(objc.Send(objc.ID(w.HostView(glass)), selAXChildren)); len(kids) != 1 || kids[0] != axSelf(btn) {
			fail("the glass content's children are %d objects, want its button %s", len(kids), goString(objc.Send(kids[0], objc.Sel("description"))))
		}

		// The role'd host box: the native view is inside it.
		kids := arrayObjs(objc.Send(top[1], selAXChildren))
		if len(kids) != 1 || kids[0] != axSelf(tf) {
			fail("the Editor group has %d children, want the text field %s", len(kids), goString(objc.Send(kids[0], objc.Sel("description"))))
		} else if p := objc.Send(kids[0], objc.Sel("accessibilityParent")); p != top[1] {
			fail("the text field's parent is %#x, want the Editor group", p)
		}

		// Virtual list: the total, one row built.
		if n := msg(top[2], "accessibilityRowCount").R0(); n != 1_000_000 {
			fail("row count %d, want 1000000", n)
		}
		if rows := arrayObjs(objc.Send(top[2], objc.Sel("accessibilityRows"))); len(rows) != 1 {
			fail("%d rows materialised, want 1", len(rows))
		}

		// The visible range is the app's (fakeAX: bytes 2..6).
		if loc, n := axRange(top[3], "accessibilityVisibleCharacterRange"); loc != 2 || n != 4 {
			fail("visible range {%d, %d}, want {2, 4}", loc, n)
		}

		// The user clicks into the text field: it is focused, though no
		// node of the tree says so.
		w.FocusHost(edit)
		if w.hosts.focus == 0 {
			fail("the text field did not take the keyboard")
			return
		}
		w.SyncAX()
		want := objc.Send(tf, selAXFocused)
		if want == 0 {
			want = tf
		}
		if got := objc.Send(w.view, selAXFocused); got != want {
			fail("focused element %#x, want the text field's %#x (the field %#x)", got, want, tf)
		}
	})
	for _, e := range errs {
		t.Error(e)
	}
}
