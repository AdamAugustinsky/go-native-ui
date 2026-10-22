package ui

import (
	"reflect"
	"testing"

	"gonativeui/gpu"
)

func TestRichLinkTabAndActivation(t *testing.T) {
	r := linkHarness(t)
	k := RichLinkKey(r.c.KeyOf(r.sig.ID), 0)
	r.in.PushKey(KeyEvent{Code: KeyTab, Down: true})
	r.step()
	if r.c.FocusKey() != k || !r.c.FocusVisible() {
		t.Fatalf("Tab focus %x, want span %x", r.c.FocusKey(), k)
	}
	for _, code := range []KeyCode{KeyEnter, KeySpace} {
		r.in.PushKey(KeyEvent{Code: code, Down: true})
		r.step()
		if r.sig.Clicked != 42 || r.sig.ClickedSpan != 0 {
			t.Fatalf("%v activation: %+v", code, r.sig)
		}
		if r.c.TakeKey(code, 0) {
			t.Fatal("activation key remained available to another handler")
		}
		r.step()
		if r.sig.Clicked != 0 {
			t.Fatal("activation repeated without input")
		}
		r.in.PushKey(KeyEvent{Code: code, Down: true, Repeat: true})
		r.step()
		if r.sig.Clicked != 0 {
			t.Fatal("key repeat activated link")
		}
		r.in.PushKey(KeyEvent{Code: code, Down: false})
		r.step()
		if r.sig.Clicked != 0 {
			t.Fatal("key release activated link")
		}
	}
}

func TestRichLinkClaimedActivationAndRemovedBox(t *testing.T) {
	r := linkHarness(t)
	k := RichLinkKey(r.c.KeyOf(r.sig.ID), 0)
	r.c.SetFocus(k)
	r.in.PushKey(KeyEvent{Code: KeySpace, Down: true})
	r.frame(func(c *Context) { c.TakeKey(KeySpace, 0); r.build(c) })
	if r.sig.Clicked != 0 {
		t.Fatal("claimed activation key clicked span")
	}
	r.in.Focused = false
	r.in.PushKey(KeyEvent{Code: KeyEnter, Down: true})
	r.step()
	if r.sig.Clicked != 0 {
		t.Fatal("inactive window activated span")
	}
	r.frame(func(c *Context) {})
	if r.c.FocusKey() != 0 {
		t.Fatal("removed RichText retained focus")
	}
}

// Pilot builds its pane before the toolbar that opens its filter. The opener
// focuses a field that will first exist on the following frame. Rich-link
// lifecycle checks must not cancel this ordinary deferred-focus request.
func TestRichLinkCleanupPreservesDeferredFieldFocus(t *testing.T) {
	r := linkHarness(t)
	r.c.SetFocus(RichLinkKey(r.c.KeyOf(r.sig.ID), 0))
	r.step()
	var buf TextBuffer
	var field TextFieldSignal
	var fieldKey, openerKey Key
	show := false
	build := func(c *Context) {
		r.build(c)
		fieldKey = HashString(c.ParentKey(), "deferred-field")
		field = TextFieldSignal{}
		if show {
			field = c.TextField("deferred-field", &buf)
		}
		c.NextSize(Px(80), Px(20))
		opener := c.Box(Clickable|Focusable, "open-field")
		openerKey = c.KeyOf(opener)
		if c.Signal(opener).Activated {
			show = true
			c.SetFocus(fieldKey)
			c.RequestFrame()
		}
	}
	r.frame(build)
	r.in.PushAction(SemRequest{Key: openerKey, Action: ActPress})
	r.frame(build)
	if !show || field.ID != 0 || r.c.FocusKey() != fieldKey {
		t.Fatalf("opener frame: show %v, field %d, focus %x, want pending %x", show, field.ID, r.c.FocusKey(), fieldKey)
	}
	r.frame(build)
	if !field.Focused || r.c.KeyOf(field.ID) != fieldKey {
		t.Fatal("deferred field did not receive focus on its first build")
	}
	r.in.AppendText("x")
	r.frame(build)
	if string(buf.Bytes()) != "x" {
		t.Fatalf("deferred field lost keyboard input: %q", buf.Bytes())
	}
}

func TestRichLinkBuildOrderAndTextOnlyTab(t *testing.T) {
	r := linkHarness(t)
	r.body = "first and second"
	r.spans = []Span{spanOf(r.body, "first", 1, SpanLink, 11), spanOf(r.body, "and", 2, 0, 0), spanOf(r.body, "second", 1, SpanMention, 22)}
	var before, after Key
	build := func(c *Context) {
		c.NextSize(Px(80), Px(20))
		before = c.KeyOf(c.Box(Focusable|FocusText, "before"))
		r.build(c)
		c.NextSize(Px(80), Px(20))
		after = c.KeyOf(c.Box(Focusable|FocusList, "after"))
	}
	r.frame(build)
	k0, k1 := RichLinkKey(r.c.KeyOf(r.sig.ID), 0), RichLinkKey(r.c.KeyOf(r.sig.ID), 2)
	if k0 != KeyN(KeyN(r.c.KeyOf(r.sig.ID), 0x6c696e6b), 0) {
		t.Fatal("semantic identity changed")
	}
	tab := func(mods Mods, want Key) {
		t.Helper()
		r.in.PushKey(KeyEvent{Code: KeyTab, Mods: mods, Down: true})
		r.frame(build)
		if got := r.c.FocusKey(); got != want {
			t.Fatalf("Tab(%v) = %x, want %x", mods, got, want)
		}
	}
	for _, k := range []Key{before, k0, k1, after, before} {
		tab(0, k)
	}
	for _, k := range []Key{after, k1, k0, before, after} {
		tab(ModShift, k)
	}
	r.c.SetTabReachesControls(false)
	r.c.SetFocus(before)
	tab(0, after)
	r.c.SetFocus(k0)
	tab(0, after)
	r.c.SetFocus(k1)
	tab(ModShift, before)
	r.c.SetFocus(k1)
	r.in.PushKey(KeyEvent{Code: KeyEnter, Down: true})
	r.frame(build)
	if r.sig.Clicked != 22 {
		t.Fatal("explicit focus should activate even with keyboard navigation off")
	}
	r.in.PushKey(KeyEvent{Code: KeyTab, Down: true})
	r.frame(func(c *Context) { c.TakeKey(KeyTab, 0); build(c) })
	if r.c.FocusKey() != k1 {
		t.Fatal("claimed Tab moved focus")
	}
}

func TestRichLinkLifecycle(t *testing.T) {
	for _, change := range []string{"disabled", "removed", "plain", "clipped", "hidden", "truncated"} {
		t.Run(change, func(t *testing.T) {
			r := linkHarness(t)
			changed := false
			build := func(c *Context) {
				if changed {
					switch change {
					case "disabled":
						c.NextFlags(Disabled)
					case "removed":
						r.spans = nil
					case "plain":
						r.spans[0].Flags = 0
					case "clipped":
						c.NextFlags(FloatingX | FloatingY)
						c.NextPos(2000, 2000)
					case "hidden":
						c.NextWidth(Px(80))
						c.BeginFlow("flow", FlowOpts{MaxLines: 1})
						c.NextSize(Px(80), Px(20))
						c.Box(0, "first")
						c.NextSize(Px(80), Px(20))
					case "truncated":
						r.o.MaxW, r.o.MaxLines, r.o.Ellipsis = 30, 1, true
					}
				}
				r.build(c)
				if changed && change == "hidden" {
					c.EndFlow()
				}
			}
			k := RichLinkKey(r.c.KeyOf(r.sig.ID), 0)
			r.c.SetFocus(k)
			changed = true
			r.frame(build)
			if r.c.FocusKey() != 0 {
				t.Fatalf("%s retained focus %x", change, r.c.FocusKey())
			}
			r.c.SetFocus(k) // a stale application key must not activate
			r.in.PushKey(KeyEvent{Code: KeyEnter, Down: true})
			r.frame(build)
			if r.sig.Clicked != 0 || r.c.FocusKey() != 0 {
				t.Fatal("stale target activated or retained focus")
			}
		})
	}
}

func TestRichLinkAncestorClipping(t *testing.T) {
	r := linkHarness(t)
	w := float32(70) // link starts at x=48: partly visible
	build := func(c *Context) {
		c.NextSize(Px(w), Px(10))
		c.Open(Clip, "clip")
		c.NextSize(Px(136), Px(14))
		r.build(c)
		c.Close()
	}
	r.frame(build)
	k := RichLinkKey(r.c.KeyOf(r.sig.ID), 0)
	r.c.SetFocus(k)
	r.c.SetFocusVisible(true)
	r.in.PushKey(KeyEvent{Code: KeySpace, Down: true})
	r.frame(build)
	if r.sig.Clicked != 42 || r.c.FocusKey() != k {
		t.Fatal("partly visible link lost focus or activation")
	}
	rings := 0
	for _, q := range r.list.Quads {
		if q.BorderColor == colLink {
			rings++
			if q.Clip != [4]float32{0, 0, 70, 10} {
				t.Fatalf("ring escaped ancestor clip: %v", q.Clip)
			}
		}
	}
	if rings != 1 {
		t.Fatalf("%d partial-link rings", rings)
	}
	w = 40
	r.frame(build)
	if r.c.FocusKey() != 0 {
		t.Fatal("ancestor-clipped link retained focus")
	}
}

func TestRichLinkOverlayFocusAndAX(t *testing.T) {
	r := linkHarness(t)
	k := RichLinkKey(r.c.KeyOf(r.sig.ID), 0)
	r.in.PushAction(SemRequest{Key: k, Action: ActFocus})
	r.step()
	if r.c.FocusKey() != k {
		t.Fatal("AX focus did not resolve the span")
	}
	if n, ok := linkNode(r.c); !ok || n.Key != k || n.State&SemFocused == 0 || n.Actions&ActFocus == 0 {
		t.Fatalf("focused link: %+v", n)
	}
	modal := true
	var popupKey Key
	build := func(c *Context) {
		r.build(c)
		if modal {
			c.BeginOverlay(1, OverlayModal, [4]float32{0, 0, 300, 200}, PlaceCenter)
			sig := c.RichText(2, []byte("local action"), []Span{{Len: 12, Flags: SpanLink, Tag: 99}}, r.st, RichOpts{MaxW: 200})
			popupKey = RichLinkKey(c.KeyOf(sig.ID), 0)
			c.EndOverlay()
		}
	}
	r.frame(build)
	if r.c.FocusKey() != popupKey {
		t.Fatal("modal did not focus its first span")
	}
	r.in.PushKey(KeyEvent{Code: KeyTab, Down: true})
	r.frame(build)
	if r.c.FocusKey() != popupKey {
		t.Fatal("Tab escaped modal")
	}
	r.in.PushKey(KeyEvent{Code: KeyTab, Mods: ModShift, Down: true})
	r.frame(build)
	if r.c.FocusKey() != popupKey {
		t.Fatal("Shift+Tab escaped modal")
	}
	r.c.SetFocus(k)
	r.in.PushKey(KeyEvent{Code: KeySpace, Down: true})
	r.frame(build)
	if r.sig.Clicked != 0 {
		t.Fatal("underlying span activated beneath modal")
	}
	r.c.SetFocus(popupKey)
	modal = false
	r.frame(build)
	if r.c.FocusKey() != k {
		t.Fatal("closing modal failed to restore span focus")
	}
}

func TestRichLinkRingAndWarmAllocations(t *testing.T) {
	for _, dir := range []Dir{DirLTR, DirRTL} {
		body := "a long link that wraps"
		r := newRich(t, body, []Span{spanOf(body, "long link that", 1, SpanLink, 9)}, RichOpts{MaxW: 100, Dir: dir, FocusColor: 0x123456FF})
		r.pad = 4
		r.step()
		r.step()
		unfocused := append([]gpu.Quad(nil), r.list.Quads...)
		k := RichLinkKey(r.c.KeyOf(r.sig.ID), 0)
		r.c.SetFocus(k)
		r.step()
		if !reflect.DeepEqual(unfocused, r.list.Quads) {
			t.Fatal("programmatic non-visible focus changed drawing")
		}
		r.c.SetFocusVisible(true)
		r.step()
		var got [][4]float32
		for _, q := range r.list.Quads {
			if q.BorderColor == r.o.FocusColor {
				got = append(got, q.Rect)
			}
		}
		want := r.c.RichRangeRects(r.sig.ID, int(r.spans[0].Off), int(r.spans[0].Off+r.spans[0].Len), nil)
		if len(want) < 2 || !reflect.DeepEqual(got, want) {
			t.Fatalf("dir %v ring %v, want actual line rectangles %v", dir, got, want)
		}
		for range 40 {
			r.step()
		}
		if n := testing.AllocsPerRun(60, r.step); n != 0 {
			t.Fatalf("focused multi-line link: %v allocations/frame", n)
		}
		if n := testing.AllocsPerRun(60, func() {
			r.in.PushKey(KeyEvent{Code: KeyTab, Down: true})
			r.step()
		}); n != 0 {
			t.Fatalf("span Tab traversal: %v allocations/frame", n)
		}
		r.in.Focused = false
		r.step()
		for _, q := range r.list.Quads {
			if q.BorderColor == r.o.FocusColor {
				t.Fatal("inactive window ring")
			}
		}
	}
}
