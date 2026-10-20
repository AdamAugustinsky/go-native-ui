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
	dst.Announce = f.t.Announce
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
		b.t.Announce = [2]uint32{uint32(len(b.t.Bytes)), 4}
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
