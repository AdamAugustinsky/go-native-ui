//go:build windows

package webview

import (
	"encoding/base64"
	"encoding/json"
	"fmt"
	"strings"
	"syscall"
	"unsafe"

	"gonativeui/internal/win32"
)

// Eval and Capture on Windows (D94): the DevTools protocol through
// ICoreWebView2::CallDevToolsProtocolMethod, which needs no DevTools window
// and no AreDevToolsEnabled. Eval is Runtime.evaluate of an async arrow in
// the page's main world, awaited and returned by value; Capture is
// Page.captureScreenshot as a PNG. The completion handler's word is the call's
// ID and the event (hEval, hCapture); rt.calls maps the call to its view.

type evalParams struct {
	Expression    string `json:"expression"`
	AwaitPromise  bool   `json:"awaitPromise"`
	ReturnByValue bool   `json:"returnByValue"`
	UserGesture   bool   `json:"userGesture"`
}

func (v *view) eval(id uint32, body string) error {
	p, err := json.Marshal(evalParams{Expression: "(async () => {\n" + body + "\n})()", AwaitPromise: true, ReturnByValue: true, UserGesture: true})
	if err != nil {
		return fmt.Errorf("%w: %v", ErrRuntime, err)
	}
	return v.devTools(id, hEval, "Runtime.evaluate", string(p))
}

func (v *view) capture(id uint32) error {
	return v.devTools(id, hCapture, "Page.captureScreenshot", `{"format":"png"}`)
}

// devTools calls method with params (JSON) for call id, whose completion is
// handler event ev.
func (v *view) devTools(id uint32, ev uint8, method, params string) error {
	if v.wv == 0 {
		return fmt.Errorf("%w: the view is not ready yet", ErrRuntime)
	}
	o, _, err := rt.handlers.New(uintptr(id)<<8 | uintptr(ev))
	if err != nil {
		return fmt.Errorf("%w: %v", ErrRuntime, err)
	}
	rt.calls[id] = v.id
	hr := comh(v.wv, wvCallDevTools, uintptr(unsafe.Pointer(wide(method))), uintptr(unsafe.Pointer(wide(params))), o.Ptr())
	o.Release()
	if hr < 0 {
		delete(rt.calls, id)
		return fmt.Errorf("%w: %s failed (0x%08X)", ErrRuntime, method, uint32(hr))
	}
	return nil
}

// devToolsDone is a call's completion, Invoke(HRESULT, LPCWSTR json): the
// JSON is the runtime's, valid during the call.
func devToolsDone(id uint32, ev uint8, hr int32, out uintptr) {
	vid, ok := rt.calls[id]
	if !ok {
		return
	}
	delete(rt.calls, id)
	v := rt.views[vid]
	if v == nil || v.closed {
		return
	}
	var r Result
	if hr < 0 {
		r.Err = fmt.Errorf("%w: the DevTools protocol call failed (0x%08X)", ErrRuntime, uint32(hr))
	} else if ev == hEval {
		r = evalResult(readWide(out))
	} else {
		r = captureResult(readWide(out))
	}
	kind := EvalEnded
	if ev == hCapture {
		kind = CaptureEnded
	}
	if v.end(id, r) {
		rt.ring.OfferID(uint64(v.id), int64(kind), int64(id), nil)
	}
}

// evalResult reads Runtime.evaluate's answer: the string asyncBody's
// JSON.stringify made, or the exception.
func evalResult(s string) Result {
	var out struct {
		Result struct {
			Type  string          `json:"type"`
			Value json.RawMessage `json:"value"`
		} `json:"result"`
		Exception *struct {
			Text      string `json:"text"`
			Exception *struct {
				Description string          `json:"description"`
				Value       json.RawMessage `json:"value"`
			} `json:"exception"`
		} `json:"exceptionDetails"`
	}
	if err := json.Unmarshal([]byte(s), &out); err != nil {
		return Result{Err: fmt.Errorf("%w: an answer that is not JSON: %v", ErrRuntime, err)}
	}
	if e := out.Exception; e != nil {
		msg := e.Text
		if x := e.Exception; x != nil {
			if x.Description != "" {
				msg = x.Description
			} else if len(x.Value) > 0 {
				msg = string(x.Value)
			}
		}
		msg, _, _ = strings.Cut(msg, "\n    at ") // the stack
		msg = strings.TrimPrefix(msg, "Error: ")
		return Result{Err: fmt.Errorf("%w: %s", ErrScript, msg)}
	}
	var js string
	if out.Result.Type != "string" || json.Unmarshal(out.Result.Value, &js) != nil {
		return Result{JSON: "null"}
	}
	return Result{JSON: js}
}

// captureResult reads Page.captureScreenshot's answer, {"data": base64}.
func captureResult(s string) Result {
	var out struct {
		Data string `json:"data"`
	}
	if err := json.Unmarshal([]byte(s), &out); err != nil || out.Data == "" {
		return Result{Err: fmt.Errorf("%w: no image in the answer", ErrRuntime)}
	}
	b, err := base64.StdEncoding.DecodeString(out.Data)
	if err != nil {
		return Result{Err: fmt.Errorf("%w: %v", ErrRuntime, err)}
	}
	return Result{PNG: b}
}

// readWide copies a NUL-terminated UTF-16 string the runtime owns.
func readWide(p uintptr) string {
	if p == 0 {
		return ""
	}
	n := 0
	for *(*uint16)(unsafe.Add(win32.Ptr(p), 2*n)) != 0 {
		n++
	}
	return syscall.UTF16ToString(unsafe.Slice((*uint16)(win32.Ptr(p)), n))
}

// forgetCalls drops a closing view's running calls: their completions find
// nothing.
func (v *view) forgetCalls() {
	for id, vid := range rt.calls {
		if vid == v.id {
			delete(rt.calls, id)
		}
	}
}
