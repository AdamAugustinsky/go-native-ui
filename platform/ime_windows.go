//go:build windows

package platform

import (
	"unicode/utf16"
	"unicode/utf8"
	"unsafe"

	"gonativeui/internal/win32"
)

// Input methods through imm32: the app draws the composition (the default
// composition window is hidden in WM_IME_SETCONTEXT and WM_IME_COMPOSITION is
// handled without DefWindowProc, which therefore does not turn the result
// into WM_IME_CHAR), so the composition arrives as EvPreedit and the result as
// EvPreedit (empty) followed by EvText. The keys the input method processes
// reach the window as VK_PROCESSKEY, which the platform maps to no key, so
// they never become EvKey.
//
// This path is compiled and vetted in CI only; it has not been exercised with
// a real input method yet (docs/measurements/phase4-text.md).

// compositionForm is COMPOSITIONFORM, candidateForm CANDIDATEFORM.
type compositionForm struct {
	style uint32
	pt    point
	area  rect
}

type candidateForm struct {
	index, style uint32
	pt           point
	area         rect
}

// imeContext returns the window's input context (release it with
// imeRelease), 0 when there is none.
func (w *Window) imeContext() uintptr {
	if pImmGetContext == 0 || w.hwnd == 0 {
		return 0
	}
	return win32.Call(pImmGetContext, w.hwnd)
}

func (w *Window) imeRelease(himc uintptr) { win32.Call(pImmReleaseContext, w.hwnd, himc) }

// SetIMERect tells the input method where the text caret (or the composition,
// while one is shown) is, in client pixels with the origin at the top left;
// the candidate window is placed below it. Main thread; does nothing when the
// rectangle is unchanged, so it can be called every frame.
func (w *Window) SetIMERect(x, y, wd, h float32) {
	r := [4]float32{x, y, wd, h}
	if r == w.imeRect {
		return
	}
	w.imeRect = r
	w.imePlace()
}

// imePlace applies the rectangle to the input context.
func (w *Window) imePlace() {
	himc := w.imeContext()
	if himc == 0 {
		return
	}
	defer w.imeRelease(himc)
	x, y, h := int32(w.imeRect[0]), int32(w.imeRect[1]), int32(w.imeRect[3])
	w.compForm = compositionForm{style: cfsPoint, pt: point{x, y}}
	win32.Call(pImmSetCompWindow, himc, uintptr(unsafe.Pointer(&w.compForm)))
	w.candForm = candidateForm{style: cfsCandidatePos, pt: point{x, y + h}}
	win32.Call(pImmSetCandWindow, himc, uintptr(unsafe.Pointer(&w.candForm)))
}

// SetTextInput says whether a text field has keyboard focus. When it stops
// having it while an input method is composing, the composition is cancelled
// (EvPreedit with no text follows). Main thread.
func (w *Window) SetTextInput(active bool) {
	if active || !w.composing {
		return
	}
	if himc := w.imeContext(); himc != 0 {
		win32.Call(pImmNotifyIME, himc, niCompositionStr, cpsCancel, 0)
		w.imeRelease(himc)
	}
	w.imeEnded()
}

// imeEnded reports the end of a composition.
func (w *Window) imeEnded() {
	if w.composing {
		w.composing = false
		w.q.pushPreedit(nil, 0, 0, Now())
	}
}

// compString reads a composition string kind (GCS_*) of himc as UTF-8 into
// w.imeU8 and returns it.
func (w *Window) compString(himc, kind uintptr) []byte {
	n := int32(win32.Call(pImmGetCompStringW, himc, kind, uintptr(unsafe.Pointer(&w.imeU16[0])), uintptr(len(w.imeU16)*2)))
	w.imeU8 = w.imeU8[:0]
	if n <= 0 {
		return w.imeU8
	}
	u := w.imeU16[:min(int(n)/2, len(w.imeU16))]
	for i := 0; i < len(u); i++ {
		r := rune(u[i])
		if utf16.IsSurrogate(r) && i+1 < len(u) {
			r = utf16.DecodeRune(r, rune(u[i+1]))
			if r != utf8.RuneError {
				i++
			}
		}
		w.imeU8 = utf8.AppendRune(w.imeU8, r)
	}
	return w.imeU8
}

// onImeComposition handles WM_IME_COMPOSITION: the result string (committed
// text) and/or the composition string with its caret.
func (w *Window) onImeComposition(flags uintptr) {
	himc := w.imeContext()
	if himc == 0 {
		return
	}
	defer w.imeRelease(himc)
	if flags&gcsResultStr != 0 {
		if res := w.compString(himc, gcsResultStr); len(res) > 0 {
			w.imeEnded()
			w.q.pushText(res, Now())
		}
	}
	if flags&gcsCompStr != 0 {
		comp := w.compString(himc, gcsCompStr)
		if len(comp) == 0 {
			w.imeEnded()
			return
		}
		sel := 0
		if flags&gcsCursorPos != 0 {
			// The caret as a UTF-16 index into the composition string.
			sel = u16ToByte(comp, win32.Call(pImmGetCompStringW, himc, gcsCursorPos, 0, 0)&0xFFFF)
		} else {
			sel = len(comp)
		}
		w.composing = true
		w.q.pushPreedit(comp, int32(sel), 0, Now())
	}
}
