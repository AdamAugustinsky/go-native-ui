//go:build windows

package platform

import (
	"runtime"
	"strings"
	"sync"
	"unsafe"

	"gonativeui/internal/win32"
)

// File panels on Windows (the phase 8 plan's W8; dialog.go has the
// contract): IFileOpenDialog and IFileSaveDialog, owned by the window. The
// call configures the dialog object and reserves its ticket, then posts
// oleRunMsg (ole_windows.go); the pump runs Show, a modal loop of the
// shell's, inside enterModal/leaveModal, so the app's windows keep drawing
// through their frame handlers while it is up. Show disables the owner
// window, so its input belongs to the dialog, as a sheet's does on macOS.
// When Show returns, the ticket is completed and the application object
// delivers the result, exactly once:
//
//   - the user confirms: DialogAnswered with the paths
//     (SIGDN_FILESYSPATH, UTF-8);
//   - the user cancels: DialogCancelled;
//   - the window closes under it (Window.Close): IFileDialog::Close ends
//     it, and the app gets DialogCancelled;
//   - the app withdraws it (CancelDialog): IFileDialog::Close, no result.
//
// Alerts are not native on Windows: Alert returns ErrUnsupported and the app
// shows its drawn dialog (D29's reasoning: TaskDialog has no dark mode).
//
// Spec mapping: Types become one filter of their patterns (".png" is *.png;
// a few UTType identifiers map to their extensions, others are left out, and
// a spec with none known is refused as on macOS); for a save panel each type
// is a filter of its own and the first gives the default extension. Dirs
// without Files picks folders (FOS_PICKFOLDERS; Windows cannot pick both in
// one dialog). Start is the folder it opens in, Title its title, Prompt the
// OK button's label. Message has no place in the Windows dialog and is not
// shown.

// NativeDialogs reports whether the OS has native alerts: false here (alerts
// are drawn; the file panels are native, NativeFilePanels).
const NativeDialogs = false

// NativeFilePanels reports whether OpenPanel and SavePanel are native: true
// here.
const NativeFilePanels = true

// maxDialogs bounds the dialogs open at once, one per window at most.
const maxDialogs = 8

// IFileDialog's vtable (shobjidl_core.h: IUnknown 0-2, IModalWindow::Show
// 3, then IFileDialog in declaration order), IFileOpenDialog's two after it,
// IShellItem's and IShellItemArray's.
const (
	vtFDShow              = 3
	vtFDSetFileTypes      = 4
	vtFDSetFileTypeIndex  = 5
	vtFDSetOptions        = 9
	vtFDGetOptions        = 10
	vtFDSetFolder         = 12
	vtFDSetFileName       = 15
	vtFDSetTitle          = 17
	vtFDSetOkButtonLabel  = 18
	vtFDGetResult         = 20
	vtFDSetDefaultExt     = 22
	vtFDClose             = 23
	vtFODGetResults       = 27
	vtSIGetDisplayName    = 5
	vtSIAGetCount         = 7
	vtSIAGetItemAt        = 8
	vtOleWindowGetWindow  = 3
	sigdnFileSysPath      = 0x80058000
	fosOverwritePrompt    = 0x2
	fosNoChangeDir        = 0x8
	fosPickFolders        = 0x20
	fosForceFileSystem    = 0x40
	fosAllowMultiSelect   = 0x200
	fosPathMustExist      = 0x800
	fosFileMustExist      = 0x1000
	wmCommand             = 0x0111
	idCancel              = 2
	dialogShowing         = 2 // dialogRec.state: Show is running
	dialogQueued          = 1 // dialogRec.state: waiting for runOLE
	maxDialogFilterTypes  = 32
	maxDialogFilterSpecLn = 1024
)

var (
	clsidFileOpenDialog = win32.GUID{Data1: 0xDC1C5A9C, Data2: 0xE88A, Data3: 0x4DDE, Data4: [8]byte{0xA5, 0xA1, 0x60, 0xF8, 0x2A, 0x20, 0xAE, 0xF7}}
	clsidFileSaveDialog = win32.GUID{Data1: 0xC0B4E2F3, Data2: 0xBA21, Data3: 0x4773, Data4: [8]byte{0x8D, 0xBA, 0x33, 0x5E, 0xC9, 0x46, 0xEB, 0x8B}}
	iidIFileOpenDialog  = win32.GUID{Data1: 0xD57C7288, Data2: 0xD4AD, Data3: 0x4768, Data4: [8]byte{0xBE, 0x02, 0x9D, 0x96, 0x95, 0x32, 0xD9, 0x60}}
	iidIFileSaveDialog  = win32.GUID{Data1: 0x84BCCD23, Data2: 0x5FDE, Data3: 0x4CDB, Data4: [8]byte{0xAE, 0xA4, 0xAF, 0x64, 0xB8, 0x3D, 0x78, 0xAB}}
	iidIShellItem       = win32.GUID{Data1: 0x43826D1E, Data2: 0xE718, Data3: 0x42EE, Data4: [8]byte{0xBC, 0x55, 0xA1, 0xE2, 0x61, 0xC3, 0x7B, 0xFE}}
	iidIOleWindow       = win32.GUID{Data1: 0x00000114, Data2: 0x0000, Data3: 0x0000, Data4: [8]byte{0xC0, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x46}}
)

// dialogRec is one file dialog, from the call to the end of its Show.
type dialogRec struct {
	ticket    DialogTicket // 0: the record is free
	kind      DialogKind
	w         *Window
	obj       uintptr // the IFileOpenDialog or IFileSaveDialog (owned)
	state     uint8
	withdrawn bool // CancelDialog: no result
	closed    bool // the window closed: a cancelled result
	owner     uintptr
	strs      []*uint16 // the strings handed to the dialog, kept until it ends
}

// filterSpec is COMDLG_FILTERSPEC.
type filterSpec struct{ name, spec *uint16 }

var (
	dialogOnce    sync.Once
	dialogs       [maxDialogs]dialogRec
	dialogTickets *Tickets // the results, delivered by the application object
	dialogPaths   []byte   // a finished dialog's paths, NUL-ended (main thread)
	dialogFilters [maxDialogFilterTypes]filterSpec
	dialogOut     uintptr // out-parameter word for the dialog's COM calls
	dialogOut2    uintptr
	dialogCount   uint32
	dialogOpts    uint32
	dialogPWSTR   uintptr
	dialogHWND    uintptr

	// liveDialogs counts dialog objects created and not yet released, for
	// the leak tests.
	liveDialogs int
)

func dialogInit() {
	dialogOnce.Do(func() {
		dialogTickets = NewTickets(SourceDialog, 2*maxDialogs, 4<<10)
	})
}

// Alert is not native on Windows: it returns ErrUnsupported, and the app
// shows its drawn dialog.
func (w *Window) Alert(AlertSpec) (DialogTicket, error) { return 0, ErrUnsupported }

// dialogSlot finds the record for a new dialog of w.
func (w *Window) dialogSlot() (int, error) {
	if w.hwnd == 0 {
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
			return 0, ErrDialogBusy
		}
	}
	if free < 0 {
		return 0, ErrDialogBusy
	}
	return free, nil
}

// OpenPanel shows an open dialog owned by the window and returns its ticket.
// The paths arrive in its SourceDialog AppEvService (DialogResultOf). Main
// thread, at a safe point.
func (w *Window) OpenPanel(s OpenSpec) (DialogTicket, error) {
	opts := uint32(fosForceFileSystem | fosNoChangeDir | fosPathMustExist)
	if s.Dirs && !s.Files {
		opts |= fosPickFolders
	} else {
		opts |= fosFileMustExist
	}
	if s.Multiple {
		opts |= fosAllowMultiSelect
	}
	return w.fileDialog(DialogOpen, &clsidFileOpenDialog, &iidIFileOpenDialog, opts, s.Types, s.Start, "", s.Title, s.Prompt, s.Dirs && !s.Files)
}

// SavePanel shows a save dialog owned by the window and returns its ticket.
// The path arrives in its SourceDialog AppEvService (DialogResultOf). Main
// thread, at a safe point.
func (w *Window) SavePanel(s SaveSpec) (DialogTicket, error) {
	opts := uint32(fosForceFileSystem | fosNoChangeDir | fosPathMustExist | fosOverwritePrompt)
	return w.fileDialog(DialogSave, &clsidFileSaveDialog, &iidIFileSaveDialog, opts, s.Types, s.Start, s.Name, s.Title, s.Prompt, false)
}

// fileDialog creates and configures the dialog, reserves its ticket and
// posts it to the pump.
func (w *Window) fileDialog(kind DialogKind, clsid, iid *win32.GUID, opts uint32, types []string, start, name, title, prompt string, folders bool) (DialogTicket, error) {
	dialogInit()
	slot, err := w.dialogSlot()
	if err != nil {
		return 0, err
	}
	var pats []string
	if len(types) > 0 && !folders {
		if pats = dialogPatterns(types); len(pats) == 0 {
			return 0, ErrDialogSpec
		}
	}
	if !ensureOLE() || pCoCreateInstance == 0 {
		return 0, ErrUnsupported
	}
	if hr := newCOM(clsid, iid, &dialogOut); hr < 0 || dialogOut == 0 {
		return 0, ErrUnsupported
	}
	r := dialogRec{kind: kind, w: w, obj: dialogOut, owner: w.hwnd}
	liveDialogs++
	str := func(s string) *uint16 {
		p := utf16z(s)
		if p != nil {
			r.strs = append(r.strs, p)
		}
		return p
	}
	arg := func(s string) uintptr { return uintptr(unsafe.Pointer(str(s))) }
	if win32.HR(win32.Com(r.obj, vtFDGetOptions, uintptr(unsafe.Pointer(&dialogOpts)))) >= 0 {
		opts |= dialogOpts
	}
	win32.Com(r.obj, vtFDSetOptions, uintptr(opts))
	if len(pats) > 0 {
		n := 0
		if kind == DialogSave {
			for _, p := range pats {
				if n == len(dialogFilters) {
					break
				}
				label := strings.ToUpper(strings.TrimPrefix(p, "*.")) + " (" + p + ")"
				if p == "*.*" {
					label = "All files (*.*)"
				}
				dialogFilters[n] = filterSpec{str(label), str(p)}
				n++
			}
		} else {
			all := strings.Join(pats, ";")
			dialogFilters[0] = filterSpec{str(all), str(all)}
			n = 1
		}
		win32.Com(r.obj, vtFDSetFileTypes, uintptr(n), uintptr(unsafe.Pointer(&dialogFilters[0])))
		win32.Com(r.obj, vtFDSetFileTypeIndex, 1)
		dialogFilters = [maxDialogFilterTypes]filterSpec{}
		if kind == DialogSave {
			if ext := strings.TrimPrefix(pats[0], "*."); ext != "*" {
				win32.Com(r.obj, vtFDSetDefaultExt, arg(ext))
			}
		}
	}
	if start != "" && pSHCreateItem != 0 {
		if p := arg(start); p != 0 && win32.HR(win32.Call(pSHCreateItem, p, 0, uintptr(unsafe.Pointer(&iidIShellItem)), uintptr(unsafe.Pointer(&dialogOut2)))) >= 0 {
			win32.Com(r.obj, vtFDSetFolder, dialogOut2)
			win32.Release(dialogOut2)
			dialogOut2 = 0
		}
	}
	if name != "" {
		win32.Com(r.obj, vtFDSetFileName, arg(name))
	}
	if title != "" {
		win32.Com(r.obj, vtFDSetTitle, arg(title))
	}
	if prompt != "" {
		win32.Com(r.obj, vtFDSetOkButtonLabel, arg(prompt))
	}
	tk, err := dialogTickets.Reserve(0)
	if err != nil {
		r.release()
		return 0, ErrDialogBusy
	}
	r.ticket, r.state = DialogTicket(tk), dialogQueued
	dialogs[slot] = r
	postOLE(oleRunDialogs)
	return r.ticket, nil
}

// dialogPatterns maps a spec's types to file patterns (*.png), leaving out
// the identifiers it does not know.
func dialogPatterns(types []string) []string {
	var out []string
	for _, t := range types {
		switch {
		case len(t) > 1 && t[0] == '.' && !strings.ContainsAny(t, ";*?"):
			out = append(out, "*"+strings.ToLower(t))
		default:
			for _, e := range utTypeExts[t] {
				out = append(out, "*."+e)
			}
		}
	}
	return out
}

// utTypeExts are the extensions of the uniform type identifiers apps name
// most (macOS's UTType); "*" is any file.
var utTypeExts = map[string][]string{
	"public.item": {"*"}, "public.data": {"*"}, "public.content": {"*"},
	"public.text": {"txt", "md", "csv", "json", "xml", "html", "htm"}, "public.plain-text": {"txt"},
	"public.utf8-plain-text": {"txt"}, "public.json": {"json"}, "public.xml": {"xml"},
	"public.html": {"html", "htm"}, "public.comma-separated-values-text": {"csv"},
	"net.daringfireball.markdown": {"md", "markdown"}, "com.adobe.pdf": {"pdf"},
	"public.image": {"png", "jpg", "jpeg", "gif", "bmp", "webp", "heic", "tif", "tiff"},
	"public.png":   {"png"}, "public.jpeg": {"jpg", "jpeg"}, "com.compuserve.gif": {"gif"},
	"com.microsoft.bmp": {"bmp"}, "org.webmproject.webp": {"webp"}, "public.heic": {"heic"},
	"public.tiff": {"tif", "tiff"}, "public.svg-image": {"svg"},
	"public.movie": {"mp4", "mov", "m4v", "webm", "mkv", "avi"}, "public.mpeg-4": {"mp4"},
	"com.apple.quicktime-movie": {"mov"}, "public.audio": {"mp3", "m4a", "wav", "aac", "flac", "ogg"},
	"public.mp3": {"mp3"}, "com.microsoft.waveform-audio": {"wav"}, "public.zip-archive": {"zip"},
	"public.source-code": {"go", "c", "h", "cpp", "rs", "py", "js", "ts", "swift", "java"},
}

// runNextDialog shows the first queued dialog and finishes it; false when
// none is queued. Called by runOLE, one at a time.
func runNextDialog() bool {
	i := -1
	for j := range dialogs {
		if dialogs[j].ticket != 0 && dialogs[j].state == dialogQueued {
			i = j
			break
		}
	}
	if i < 0 {
		return false
	}
	r := &dialogs[i]
	r.state = dialogShowing
	enterModal()
	hr := win32.HR(win32.Com(r.obj, vtFDShow, r.owner))
	leaveModal()
	r = &dialogs[i]
	switch {
	case r.withdrawn: // CancelDialog: the ticket was cancelled, no result
	case r.closed || hr < 0:
		dialogTickets.Complete(Ticket(r.ticket), dialogA(r.kind, DialogCancelled, false), 0, nil)
	default:
		n := appendDialogPaths(r)
		if n == 0 {
			dialogTickets.Complete(Ticket(r.ticket), dialogA(r.kind, DialogCancelled, false), 0, nil)
		} else {
			dialogTickets.Complete(Ticket(r.ticket), dialogA(r.kind, DialogAnswered, false), int64(n), dialogPaths)
		}
	}
	r.release()
	return true
}

// appendDialogPaths puts the chosen paths of a confirmed dialog in
// dialogPaths, each ended by a NUL, and returns how many.
func appendDialogPaths(r *dialogRec) int {
	dialogPaths = dialogPaths[:0]
	n := 0
	add := func(item uintptr) {
		dialogPWSTR = 0
		if win32.HR(win32.Com(item, vtSIGetDisplayName, sigdnFileSysPath, uintptr(unsafe.Pointer(&dialogPWSTR)))) >= 0 && dialogPWSTR != 0 {
			if p := pwstrString(dialogPWSTR); p != "" {
				dialogPaths = append(append(dialogPaths, p...), 0)
				n++
			}
			win32.Call(pCoTaskMemFree, dialogPWSTR)
			dialogPWSTR = 0
		}
	}
	if r.kind == DialogSave {
		if win32.HR(win32.Com(r.obj, vtFDGetResult, uintptr(unsafe.Pointer(&dialogOut)))) >= 0 && dialogOut != 0 {
			add(dialogOut)
			win32.Release(dialogOut)
		}
		dialogOut = 0
		return n
	}
	if win32.HR(win32.Com(r.obj, vtFODGetResults, uintptr(unsafe.Pointer(&dialogOut)))) < 0 || dialogOut == 0 {
		return 0
	}
	arr := dialogOut
	dialogOut = 0
	if win32.HR(win32.Com(arr, vtSIAGetCount, uintptr(unsafe.Pointer(&dialogCount)))) >= 0 {
		for j := uint32(0); j < dialogCount; j++ {
			if win32.HR(win32.Com(arr, vtSIAGetItemAt, uintptr(j), uintptr(unsafe.Pointer(&dialogOut2)))) >= 0 && dialogOut2 != 0 {
				add(dialogOut2)
				win32.Release(dialogOut2)
			}
			dialogOut2 = 0
		}
	}
	win32.Release(arr)
	return n
}

// release lets the dialog object go and frees the record.
func (r *dialogRec) release() {
	if r.obj != 0 {
		win32.Release(r.obj)
		liveDialogs--
	}
	runtime.KeepAlive(r.strs)
	*r = dialogRec{}
}

// CancelDialog withdraws a dialog the app no longer wants: it closes and no
// result is delivered. It reports whether the ticket was open (not yet
// answered or cancelled). Main thread.
func (w *Window) CancelDialog(t DialogTicket) bool {
	for i := range dialogs {
		r := &dialogs[i]
		if r.ticket != t || r.w != w || t == 0 || r.withdrawn {
			continue
		}
		dialogTickets.Cancel(Ticket(t))
		if r.state == dialogQueued {
			r.release()
			return true
		}
		r.withdrawn = true
		r.end() // Show returns once its loop unwinds
		return true
	}
	return false
}

// end ends a dialog on screen: IFileDialog::Close, and, should the dialog
// not take that from outside its own callbacks, its Cancel button
// (WM_COMMAND IDCANCEL posted to its window; harmless once it is gone).
func (r *dialogRec) end() {
	h := dialogHWNDOf(r)
	win32.Com(r.obj, vtFDClose, hrCancelled)
	if h != 0 {
		win32.Call(pPostMessageW, h, wmCommand, idCancel, 0)
	}
}

// abortDialogs ends the dialogs of w before its window closes: a queued one
// is answered as cancelled at once, a showing one is closed and answered as
// cancelled when its Show returns.
func (w *Window) abortDialogs() {
	for i := range dialogs {
		r := &dialogs[i]
		if r.ticket == 0 || r.w != w {
			continue
		}
		switch {
		case r.state == dialogQueued:
			dialogTickets.Complete(Ticket(r.ticket), dialogA(r.kind, DialogCancelled, false), 0, nil)
			r.release()
		case !r.closed:
			r.closed = true
			r.end()
		}
	}
}

// dialogWindow is the HWND of the dialog of ticket t while it is on screen,
// 0 otherwise (tests).
func dialogWindow(t DialogTicket) uintptr {
	for i := range dialogs {
		if r := &dialogs[i]; r.ticket == t && r.state == dialogShowing {
			return dialogHWNDOf(r)
		}
	}
	return 0
}

// dialogHWNDOf is the dialog's window (IOleWindow), 0 before it has one.
func dialogHWNDOf(r *dialogRec) uintptr {
	if win32.QueryInterface(r.obj, &iidIOleWindow, &dialogOut2) < 0 || dialogOut2 == 0 {
		return 0
	}
	ow := dialogOut2
	dialogOut2 = 0
	dialogHWND = 0
	win32.Com(ow, vtOleWindowGetWindow, uintptr(unsafe.Pointer(&dialogHWND)))
	win32.Release(ow)
	return dialogHWND
}
