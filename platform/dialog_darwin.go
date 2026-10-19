//go:build darwin

package platform

import (
	"sync"
	"unsafe"

	"gonativeui/internal/ffi"
	"gonativeui/internal/objc"
)

// Alerts and file panels as sheets (see dialog.go for the contract). One
// per-request block kind serves all three: NSAlert, NSOpenPanel and
// NSSavePanel each copy the completion block they are given and call it once,
// on the main thread, when the sheet ends. The block carries the index of the
// dialog's record; the record holds the sheet object (retained until the
// completion), the window and the block request.
//
// A window closing under a sheet calls the completion 0 times (F1's
// measurement, D36), so Window.Close ends the sheet with NSModalResponseAbort
// first (abortDialogs), which calls it exactly once.

// NativeDialogs reports whether the OS has native alert and file panel sheets:
// true here.
const NativeDialogs = true

// maxDialogs bounds the dialogs open at once, one per window at most.
const maxDialogs = 8

type dialogRec struct {
	ticket DialogTicket // 0: the record is free
	kind   DialogKind
	w      *Window
	obj    objc.ID // the NSAlert, NSOpenPanel or NSSavePanel; retained until the sheet ends
	req    objc.Request
	// withdrawn: the app cancelled the dialog (CancelDialog) while its sheet
	// was not attached to the window, so it could not be ended then. The
	// record stays, with its request cancelled, until the sheet has been seen
	// and ended or the window closes (reapDialogs, abortDialogs): a queued
	// sheet must not appear later with nobody to answer for it.
	withdrawn bool
}

var (
	dialogOnce    sync.Once
	dialogBlock   *objc.BlockKind
	dialogs       [maxDialogs]dialogRec
	lastTicket    DialogTicket
	dialogScratch []byte // bytes handed to NSString, in memory the Go GC does not move

	clsNSAlert, clsNSOpenPanel, clsNSSavePanel, clsUTType, clsNSMutableArray, clsNSURL objc.ID

	selInit             = objc.Sel("init")
	selSetMessageText   = objc.Sel("setMessageText:")
	selSetInformative   = objc.Sel("setInformativeText:")
	selSetAlertStyle    = objc.Sel("setAlertStyle:")
	selAddButton        = objc.Sel("addButtonWithTitle:")
	selSetDestructive   = objc.Sel("setHasDestructiveAction:")
	selSetShowsSupp     = objc.Sel("setShowsSuppressionButton:")
	selShowsSupp        = objc.Sel("showsSuppressionButton")
	selSuppressionBtn   = objc.Sel("suppressionButton")
	selControlState     = objc.Sel("state")
	selSetInitialFR     = objc.Sel("setInitialFirstResponder:")
	selBeginSheet       = objc.Sel("beginSheetModalForWindow:completionHandler:")
	selEndSheet         = objc.Sel("endSheet:returnCode:")
	selAttachedSheet    = objc.Sel("attachedSheet")
	selOpenPanel        = objc.Sel("openPanel")
	selSavePanel        = objc.Sel("savePanel")
	selSetChooseFiles   = objc.Sel("setCanChooseFiles:")
	selSetChooseDirs    = objc.Sel("setCanChooseDirectories:")
	selSetMultiple      = objc.Sel("setAllowsMultipleSelection:")
	selSetCreateDirs    = objc.Sel("setCanCreateDirectories:")
	selSetNameField     = objc.Sel("setNameFieldStringValue:")
	selSetAllowedTypes  = objc.Sel("setAllowedContentTypes:")
	selSetDirectoryURL  = objc.Sel("setDirectoryURL:")
	selSetMessage       = objc.Sel("setMessage:")
	selSetPrompt        = objc.Sel("setPrompt:")
	selTypeWithID       = objc.Sel("typeWithIdentifier:")
	selTypeWithExt      = objc.Sel("typeWithFilenameExtension:")
	selURLCount         = objc.Sel("count")
	selURLAt            = objc.Sel("objectAtIndex:")
	selAddObject        = objc.Sel("addObject:")
	selURLs             = objc.Sel("URLs")
	selURL              = objc.Sel("URL")
	selFSRep            = objc.Sel("fileSystemRepresentation")
	selFileURLWithPath  = objc.Sel("fileURLWithPath:isDirectory:")
	modalResponseAbort  = ^uintptr(1000) // NSModalResponseAbort, -1001
	modalResponseOK     = int64(1)
	alertFirstButtonRet = int64(1000) // NSAlertFirstButtonReturn
)

func dialogInit() {
	dialogOnce.Do(func() {
		pbInit() // AppKit, NSString and its selectors
		loadFramework("/System/Library/Frameworks/UniformTypeIdentifiers.framework/UniformTypeIdentifiers")
		clsNSAlert, clsNSOpenPanel, clsNSSavePanel = objc.Cls("NSAlert"), objc.Cls("NSOpenPanel"), objc.Cls("NSSavePanel")
		clsUTType, clsNSMutableArray, clsNSURL = objc.Cls("UTType"), objc.Cls("NSMutableArray"), objc.Cls("NSURL")
		// void (^)(NSInteger): the completion handler of all three.
		dialogBlock = objc.NewBlockKind("v16@?0q8", true, func(ctx uintptr, c *ffi.CB) {
			finishDialog(int(ctx), int64(c.Int(1)))
		}, nil)
	})
}

// nsStr returns an autoreleased NSString with the text of s. The bytes go
// through a buffer the package owns: a Go string's bytes may sit on a stack
// that moves.
func nsStr(s string) objc.ID {
	dialogScratch = append(append(dialogScratch[:0], s...), 0)
	alloc := objc.Send(clsNSString, selAllocStr) // before the init call is assembled: both use the frame
	str := objc.ID(objc.F.Reset().Arg(uintptr(alloc)).Arg(uintptr(selInitBytes)).
		ArgPtr(unsafe.Pointer(&dialogScratch[0])).Arg(uintptr(len(s))).Arg(4 /* NSUTF8StringEncoding */).Call(objc.MsgSend).R0())
	return objc.Send(str, selAutorelease)
}

func b2u(b bool) uintptr {
	if b {
		return 1
	}
	return 0
}

// dialogSlot finds the record for a new dialog of w.
func (w *Window) dialogSlot() (int, error) {
	if w.win == 0 || w.aux.closed {
		return 0, ErrDialogSpec
	}
	free := -1
	for i := range dialogs {
		switch {
		case dialogs[i].ticket == 0:
			if free < 0 {
				free = i
			}
		case dialogs[i].w == w:
			return 0, ErrDialogBusy // also a withdrawn one still waiting to be dismissed
		}
	}
	if free < 0 {
		return 0, ErrDialogBusy
	}
	return free, nil
}

// beginDialog starts the sheet obj (+1, owned from here on) on w in record
// slot.
func (w *Window) beginDialog(slot int, kind DialogKind, obj objc.ID) (DialogTicket, error) {
	req, err := dialogBlock.New(uintptr(slot))
	if err != nil {
		objc.Release(obj)
		return 0, err
	}
	lastTicket++
	dialogs[slot] = dialogRec{ticket: lastTicket, kind: kind, w: w, obj: obj, req: req}
	objc.Send2(obj, selBeginSheet, uintptr(w.win), uintptr(req.Block()))
	req.Sent()
	return lastTicket, nil
}

// Alert shows an alert sheet on the window and returns its ticket. The answer
// arrives as an EvDialog (Window.DialogResult). Main thread, at a safe point.
// It returns ErrDialogSpec for a spec with no or too many buttons and
// ErrDialogBusy when the window already has a dialog open.
func (w *Window) Alert(s AlertSpec) (DialogTicket, error) {
	if err := s.Validate(); err != nil {
		return 0, err
	}
	dialogInit()
	slot, err := w.dialogSlot()
	if err != nil {
		return 0, err
	}
	pool := objc.F.Call0(objc.PoolPush)
	defer objc.F.Call1(objc.PoolPop, pool)

	a := objc.Send(objc.Send(clsNSAlert, selAlloc), selInit)
	if a == 0 {
		return 0, ErrDialogSpec
	}
	objc.Send1(a, selSetMessageText, uintptr(nsStr(s.Message)))
	if s.Info != "" {
		objc.Send1(a, selSetInformative, uintptr(nsStr(s.Info)))
	}
	style := [...]uintptr{AlertInformational: 1, AlertWarning: 0, AlertCritical: 2} // NSAlertStyle
	if int(s.Style) < len(style) {
		objc.Send1(a, selSetAlertStyle, style[s.Style])
	}
	var first objc.ID
	for i, title := range s.Buttons {
		b := objc.Send1(a, selAddButton, uintptr(nsStr(title)))
		for _, d := range s.Destructive {
			if d == i {
				objc.Send1(b, selSetDestructive, 1)
			}
		}
		if i == 0 {
			first = b
		}
	}
	if s.Suppression != "" {
		objc.Send1(a, selSetShowsSupp, 1)
		objc.Send1(objc.Send(a, selSuppressionBtn), selSetTitle, uintptr(nsStr(s.Suppression)))
	}
	// Keyboard users: the default button is the first responder, so that
	// Space presses it and Tab moves on from it (AppKit leaves the window
	// itself first otherwise, and full keyboard access then has nothing to
	// start from).
	objc.Send1(objc.Send(a, selWindow), selSetInitialFR, uintptr(first))
	return w.beginDialog(slot, DialogAlert, a)
}

// OpenPanel shows an open panel sheet and returns its ticket. The paths arrive
// in the EvDialog (Window.DialogResult). Main thread, at a safe point.
func (w *Window) OpenPanel(s OpenSpec) (DialogTicket, error) {
	dialogInit()
	slot, err := w.dialogSlot()
	if err != nil {
		return 0, err
	}
	pool := objc.F.Call0(objc.PoolPush)
	defer objc.F.Call1(objc.PoolPop, pool)

	types, err := contentTypes(s.Types)
	if err != nil {
		return 0, err
	}
	p := objc.Retain(objc.Send(clsNSOpenPanel, selOpenPanel))
	objc.Send1(p, selSetChooseFiles, b2u(s.Files || !s.Dirs))
	objc.Send1(p, selSetChooseDirs, b2u(s.Dirs))
	objc.Send1(p, selSetMultiple, b2u(s.Multiple))
	objc.Send1(p, selSetCreateDirs, b2u(s.Dirs))
	configurePanel(p, types, s.Start, s.Title, s.Message, s.Prompt)
	return w.beginDialog(slot, DialogOpen, p)
}

// SavePanel shows a save panel sheet and returns its ticket. The path arrives
// in the EvDialog (Window.DialogResult). Main thread, at a safe point.
func (w *Window) SavePanel(s SaveSpec) (DialogTicket, error) {
	dialogInit()
	slot, err := w.dialogSlot()
	if err != nil {
		return 0, err
	}
	pool := objc.F.Call0(objc.PoolPush)
	defer objc.F.Call1(objc.PoolPop, pool)

	types, err := contentTypes(s.Types)
	if err != nil {
		return 0, err
	}
	p := objc.Retain(objc.Send(clsNSSavePanel, selSavePanel))
	objc.Send1(p, selSetCreateDirs, 1)
	if s.Name != "" {
		objc.Send1(p, selSetNameField, uintptr(nsStr(s.Name)))
	}
	configurePanel(p, types, s.Start, s.Title, s.Message, s.Prompt)
	return w.beginDialog(slot, DialogSave, p)
}

// contentTypes resolves the types of a panel to an NSArray of UTType (0 for no
// filter). Entries AppKit does not know are left out; if none is known the
// spec is refused, since an empty filter would allow everything.
func contentTypes(types []string) (objc.ID, error) {
	if len(types) == 0 {
		return 0, nil
	}
	arr := objc.Send(clsNSMutableArray, selArray)
	n := 0
	for _, t := range types {
		var ut objc.ID
		if len(t) > 1 && t[0] == '.' {
			ut = objc.Send1(clsUTType, selTypeWithExt, uintptr(nsStr(t[1:])))
		} else if t != "" {
			ut = objc.Send1(clsUTType, selTypeWithID, uintptr(nsStr(t)))
		}
		if ut != 0 {
			objc.Send1(arr, selAddObject, uintptr(ut))
			n++
		}
	}
	if n == 0 {
		return 0, ErrDialogSpec
	}
	return arr, nil
}

// configurePanel sets what open and save panels share.
func configurePanel(p, types objc.ID, start, title, message, prompt string) {
	if types != 0 {
		objc.Send1(p, selSetAllowedTypes, uintptr(types))
	}
	if start != "" {
		str := nsStr(start)
		u := objc.ID(objc.F.Reset().Arg(uintptr(clsNSURL)).Arg(uintptr(selFileURLWithPath)).Arg(uintptr(str)).ArgBool(true).Call(objc.MsgSend).R0())
		if u != 0 {
			objc.Send1(p, selSetDirectoryURL, uintptr(u))
		}
	}
	if title != "" {
		objc.Send1(p, selSetTitle, uintptr(nsStr(title)))
	}
	if message != "" {
		objc.Send1(p, selSetMessage, uintptr(nsStr(message)))
	}
	if prompt != "" {
		objc.Send1(p, selSetPrompt, uintptr(nsStr(prompt)))
	}
}

// finishDialog is the completion handler of record i: it turns the sheet's
// response into the EvDialog, frees the record and lets the sheet object go.
// It runs on the main thread, inside the event pump, and only for a request
// that was live: a withdrawn dialog's completion is stale and never gets here.
func finishDialog(i int, resp int64) {
	r := &dialogs[i]
	if r.ticket == 0 {
		return
	}
	w := r.w
	pool := objc.F.Call0(objc.PoolPush)
	now := Now()
	var e Event
	switch r.kind {
	case DialogAlert:
		supp := objc.SendBool(r.obj, selShowsSupp) && objc.Send(objc.Send(r.obj, selSuppressionBtn), selControlState) == 1
		if idx := resp - alertFirstButtonRet; idx >= 0 && idx < MaxAlertButtons {
			e = dialogEvent(r.ticket, r.kind, DialogAnswered, uint32(idx), supp, 0, 0, now)
		} else {
			e = dialogEvent(r.ticket, r.kind, DialogCancelled, 0, supp, 0, 0, now)
		}
	default:
		if resp != modalResponseOK {
			e = dialogEvent(r.ticket, r.kind, DialogCancelled, 0, false, 0, 0, now)
			break
		}
		off := len(w.q.text)
		n := w.appendPanelPaths(r)
		e = dialogEvent(r.ticket, r.kind, DialogAnswered, uint32(n), false, off, len(w.q.text)-off, now)
	}
	w.q.push(e)
	r.release()
	objc.F.Call1(objc.PoolPop, pool)
}

// appendPanelPaths appends the chosen paths of a finished panel to the
// window's text buffer, each ended by a NUL, and returns how many.
func (w *Window) appendPanelPaths(r *dialogRec) int {
	n := 0
	add := func(u objc.ID) {
		// The file system representation, bytes as the file system has them,
		// not the NSString path: a name that is not valid UTF-8 survives.
		if p := uintptr(objc.Send(u, selFSRep)); p != 0 {
			w.q.text = append(appendCString(w.q.text, p), 0)
			n++
		}
	}
	if r.kind == DialogSave {
		if u := objc.Send(r.obj, selURL); u != 0 {
			add(u)
		}
		return n
	}
	urls := objc.Send(r.obj, selURLs)
	for j, cnt := 0, int(objc.Send(urls, selURLCount)); j < cnt; j++ {
		add(objc.Send1(urls, selURLAt, uintptr(j)))
	}
	return n
}

// appendCString appends the bytes of the NUL-terminated C string at p.
func appendCString(dst []byte, p uintptr) []byte {
	n := 0
	for *(*byte)(unsafe.Add(ffi.PtrFromC(p), n)) != 0 {
		n++
	}
	return append(dst, unsafe.Slice((*byte)(ffi.PtrFromC(p)), n)...)
}

// release lets the sheet object go (autoreleased: the completion may still be
// running inside it) and frees the record.
func (r *dialogRec) release() {
	objc.Send(r.obj, selAutorelease)
	*r = dialogRec{}
}

// sheetWindow is the NSWindow of the dialog's sheet.
func (r *dialogRec) sheetWindow() objc.ID {
	if r.kind == DialogAlert {
		return objc.Send(r.obj, selWindow)
	}
	return r.obj
}

// endSheet ends the dialog's sheet with NSModalResponseAbort if it is attached
// to the window now, and reports whether it was.
func (r *dialogRec) endSheet() bool {
	sheet := r.sheetWindow()
	if sheet == 0 || objc.Send(r.w.win, selAttachedSheet) != sheet {
		return false
	}
	objc.Send2(r.w.win, selEndSheet, uintptr(sheet), modalResponseAbort)
	return true
}

// dialogOpen reports whether w has a dialog open: the sheet owns the window's
// input then.
func (w *Window) dialogOpen() bool {
	for i := range dialogs {
		if dialogs[i].ticket != 0 && dialogs[i].w == w && !dialogs[i].withdrawn {
			return true
		}
	}
	return false
}

// CancelDialog withdraws a dialog the app no longer wants: the sheet closes
// and no result is delivered. It reports whether the ticket was open (not
// yet answered or cancelled). A sheet that is not attached yet (queued behind
// another) cannot be closed at once: it is dismissed the moment it attaches,
// and meanwhile the window counts as having a dialog (ErrDialogBusy). Main
// thread.
func (w *Window) CancelDialog(t DialogTicket) bool {
	for i := range dialogs {
		r := &dialogs[i]
		if r.ticket != t || r.w != w || t == 0 || r.withdrawn {
			continue
		}
		pool := objc.F.Call0(objc.PoolPush)
		r.req.Cancel() // the completion that follows is stale and dropped
		if r.endSheet() {
			r.release()
		} else {
			r.withdrawn = true
		}
		objc.F.Call1(objc.PoolPop, pool)
		return true
	}
	return false
}

// reapDialogs ends the withdrawn dialogs of w whose sheets have attached since
// (Wait calls it after each pump of the event loop).
func (w *Window) reapDialogs() {
	for i := range dialogs {
		if r := &dialogs[i]; r.ticket != 0 && r.withdrawn && r.w == w && r.endSheet() {
			r.release()
		}
	}
}

// abortDialogs ends the sheets of w before its window closes. AppKit calls a
// sheet's completion 0 times when its window closes under it, so each sheet is
// ended with NSModalResponseAbort first, which calls it once: the app gets a
// cancelled result. A sheet that was not attached yet, or whose completion did
// not run at once, is cancelled and answered here, so every ticket gets
// exactly one result and no request keeps its slot.
func (w *Window) abortDialogs() {
	pool := objc.F.Call0(objc.PoolPush)
	for i := range dialogs {
		r := &dialogs[i]
		if r.ticket == 0 || r.w != w {
			continue
		}
		r.endSheet()                      // the completion may run inside this call (finishDialog)
		if r.ticket != 0 && r.withdrawn { // the app withdrew it: no result
			r.release()
		} else if r.ticket != 0 {
			r.req.Cancel() // a late completion is stale
			w.q.push(dialogEvent(r.ticket, r.kind, DialogCancelled, 0, false, 0, 0, Now()))
			r.release()
		}
	}
	objc.F.Call1(objc.PoolPop, pool)
}
