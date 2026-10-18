//go:build linux

package platform

import (
	"unicode/utf8"
	"unsafe"

	"gonativeui/internal/wl"
)

// Input methods. Wayland: zwp_text_input_v3 (this file and the handlers
// below). X11 has no input-method support yet: it would need XIM
// (XOpenIM with XIMPreeditCallbacks) or an IBus/fcitx client, and the calls
// are no-ops there; keys still arrive as EvKey and EvText from the keymap.
//
// The protocol: the compositor sends enter when the surface has keyboard
// focus; the client enable()s the text input while a text field is focused,
// says where the caret is (set_cursor_rectangle) and commit()s the state. The
// input method then sends preedit_string / commit_string / delete_surrounding
// _text events and a done, which applies them as one change. Mapping:
//
//	commit_string  -> EvPreedit (empty, if composing), then EvText
//	preedit_string -> EvPreedit with the caret or selection in bytes of the text
//	nothing but done, while composing -> EvPreedit (empty): the composition ended
//
// Surrounding text is never sent, so delete_surrounding_text is ignored.
//
// Opcodes: zwp_text_input_manager_v3: destroy 0, get_text_input 1;
// zwp_text_input_v3: destroy 0, enable 1, disable 2, set_content_type 5,
// set_cursor_rectangle 6, commit 7. Events: enter 0, leave 1, preedit_string 2,
// commit_string 3, delete_surrounding_text 4, done 5.

// maxPreedit bounds the text taken from one preedit or commit event.
const maxPreedit = 4096

// wlIM is the window's text input state.
type wlIM struct {
	mgr, ti    uintptr
	entered    bool // the surface has keyboard focus (enter / leave)
	active     bool // the app has a text field focused (SetTextInput)
	enabled    bool // enable() sent and not disabled since
	composing  bool // an EvPreedit with text is outstanding
	rect       [4]int32
	rectSet    bool
	pre, com   []byte // pending preedit and commit text, until done
	preB, preE int32  // pending preedit cursor, bytes; -1: hidden
}

// wlInitIM creates the seat's text input once both globals are bound.
func (w *Window) wlInitIM() {
	y := &w.wy
	if y.im.mgr == 0 || y.seat == 0 || y.im.ti != 0 {
		return
	}
	y.args[0], y.args[1] = 0, uint64(y.seat)
	y.im.ti = w.req(y.im.mgr, 1, wl.ZwpTextInputV3.Addr(), version(y.im.mgr), 0)
	listen(y.im.ti)
	y.im.pre, y.im.com = make([]byte, 0, maxPreedit), make([]byte, 0, maxPreedit)
}

// wlIMClose releases the text input.
func (w *Window) wlIMClose() {
	y := &w.wy
	w.release(y.im.ti, 0, 1)
	w.release(y.im.mgr, 0, 1)
	y.im = wlIM{}
}

// copyCString copies the NUL-terminated string at p (0: none) into dst[:0],
// at most cap(dst) bytes, cut at a rune boundary.
func copyCString(dst []byte, p uintptr) []byte {
	dst = dst[:0]
	if p == 0 {
		return dst
	}
	for len(dst) < cap(dst) {
		c := *(*byte)(ptr(p + uintptr(len(dst))))
		if c == 0 {
			return dst
		}
		dst = append(dst, c)
	}
	for i := 0; i < 3 && len(dst) > 0 && !utf8.Valid(dst); i++ { // cut inside a rune
		dst = dst[:len(dst)-1]
	}
	return dst
}

func (w *Window) onTextInput(op uint32, a unsafe.Pointer) {
	im := &w.wy.im
	switch op {
	case 0: // enter(surface)
		im.entered = true
		w.imSync()
	case 1: // leave(surface)
		im.entered, im.enabled = false, false
		w.imEndComposition()
	case 2: // preedit_string(text, cursor_begin, cursor_end)
		im.pre = copyCString(im.pre, argP(a, 0))
		im.preB, im.preE = int32(argU(a, 1)), int32(argU(a, 2))
	case 3: // commit_string(text)
		im.com = copyCString(im.com, argP(a, 0))
	case 5: // done(serial): apply the pending change
		w.imDone()
	}
}

func (w *Window) imEndComposition() {
	if im := &w.wy.im; im.composing {
		im.composing = false
		w.q.pushPreedit(nil, 0, 0, nanotime())
	}
}

// imDone applies the pending commit and preedit as the protocol's done does:
// the old preedit is replaced, the commit is inserted, a new preedit follows.
func (w *Window) imDone() {
	im := &w.wy.im
	t := nanotime()
	if len(im.com) > 0 {
		w.imEndComposition()
		w.q.pushText(im.com, t)
	}
	if n := int32(len(im.pre)); n > 0 {
		b, e := im.preB, im.preE
		if b < 0 || e < 0 { // cursor hidden: at the end
			b, e = n, n
		}
		b, e = min(b, n), min(e, n)
		if b > e {
			b, e = e, b
		}
		w.q.pushPreedit(im.pre, b, e-b, t)
		im.composing = true
	} else {
		w.imEndComposition()
	}
	im.pre, im.com = im.pre[:0], im.com[:0]
}

// imSync enables or disables the text input to match focus and the app's
// text field.
func (w *Window) imSync() {
	y := &w.wy
	im := &y.im
	if im.ti == 0 {
		return
	}
	want := im.entered && im.active
	if want == im.enabled {
		return
	}
	im.enabled = want
	if want {
		w.req(im.ti, 1, 0, version(im.ti), 0) // enable
		w.imSendRect()
	} else {
		w.req(im.ti, 2, 0, version(im.ti), 0) // disable
		w.imEndComposition()
	}
	w.req(im.ti, 7, 0, version(im.ti), 0) // commit
	w.wlFlush()
}

func (w *Window) imSendRect() {
	im := &w.wy.im
	if !im.rectSet {
		return
	}
	for i, v := range im.rect {
		w.wy.args[i] = uint64(uint32(v))
	}
	w.req(im.ti, 6, 0, version(im.ti), 0) // set_cursor_rectangle
}

// SetIMERect tells the input method where the text caret (or the composition,
// while composing) is, in window pixels, so that it can put its candidate
// window next to it. The rectangle is copied; calling it every frame costs a
// comparison. Wayland only.
func (w *Window) SetIMERect(x, y, wd, h float32) {
	if !w.wl || w.wy.im.ti == 0 {
		return
	}
	im := &w.wy.im
	s := float32(w.s120) / 120
	r := [4]int32{int32(x / s), int32(y / s), int32(wd/s + 0.5), int32(h/s + 0.5)}
	if im.rectSet && r == im.rect {
		return
	}
	im.rect, im.rectSet = r, true
	if im.enabled {
		w.imSendRect()
		w.req(im.ti, 7, 0, version(im.ti), 0) // commit
		w.wlFlush()
	}
}

// SetTextInput says whether a text field has keyboard focus: the input method
// is only on while one has. When it stops, a composition in progress is
// cancelled (EvPreedit with no text follows, no EvText). Wayland only.
func (w *Window) SetTextInput(active bool) {
	if !w.wl {
		return
	}
	w.wy.im.active = active
	w.imSync()
}

// DiscardComposition cancels a composition in progress without committing it:
// EvPreedit with no text follows, and the input method starts afresh.
func (w *Window) DiscardComposition() {
	if !w.wl {
		return
	}
	im := &w.wy.im
	w.imEndComposition()
	if im.enabled { // disable and enable again: the input method drops its state
		w.req(im.ti, 2, 0, version(im.ti), 0)
		w.req(im.ti, 7, 0, version(im.ti), 0)
		w.req(im.ti, 1, 0, version(im.ti), 0)
		w.imSendRect()
		w.req(im.ti, 7, 0, version(im.ti), 0)
		w.wlFlush()
	}
}
