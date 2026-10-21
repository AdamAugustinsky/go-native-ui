//go:build darwin

package selfupdate

import (
	"errors"
	"fmt"
	"os"
	"runtime"
	"sync"
	"syscall"
	"time"
	"unsafe"

	"github.com/ebitengine/purego"

	"gonativeui/internal/cf"
	"gonativeui/internal/ffi"
	"gonativeui/internal/objc"
)

// The system calls, from whatever goroutine calls: every call takes a frame
// of internal/cf and, where it sends Objective-C messages, an autorelease
// pool of its own. Security.framework's code-signing calls and NSFileManager
// are thread-safe.

// Security.framework flags and codes (CSCommon.h, SecStaticCode.h, SecCode.h).
const (
	csCheckAllArchitectures = 1 << 0
	csCheckNestedCode       = 1 << 3
	csStrictValidate        = 1 << 4
	csRestrictSymlinks      = 1 << 7
	csSigningInformation    = 1 << 1
	codeSignatureAdhoc      = 0x0002

	// verifyFlags: every architecture, nested code, strict validation
	// (Info.plist, resources and their xattrs as the signature sealed
	// them), and no link that leaves the bundle.
	verifyFlags = csCheckAllArchitectures | csCheckNestedCode | csStrictValidate | csRestrictSymlinks

	errSecCSUnsigned           = -67062
	errSecCSSignatureFailed    = -67061
	errSecCSResourcesNotSealed = -67057
	errSecCSResourcesNotFound  = -67056
	errSecCSResourcesInvalid   = -67055
	errSecCSBadResource        = -67054
	errSecCSReqFailed          = -67050
	errSecCSInfoPlistFailed    = -67030

	cfNumberSInt64 = 4
	renameSwapFlag = 0x2 // RENAME_SWAP (sys/stdio.h)
)

type sysLib struct {
	once sync.Once
	err  error

	staticCreate, checkValidity, copySelf, copyDR       uintptr
	reqCreate, reqCopyString, copySigningInfo, errorMsg uintptr
	infoFlags                                           uintptr // kSecCodeInfoFlags

	dictGet, numberGet, errorDesc uintptr

	renamex, errnoLoc, pidPath, listPids uintptr
}

var sys sysLib

func (s *sysLib) load() {
	if s.err = cf.Init(); s.err != nil {
		return
	}
	sec, err := cf.Framework("Security")
	if err != nil {
		s.err = err
		return
	}
	libc, err := purego.Dlopen("/usr/lib/libSystem.B.dylib", purego.RTLD_NOW|purego.RTLD_GLOBAL)
	if err != nil {
		s.err = err
		return
	}
	core := cf.CoreFoundation()
	s.staticCreate, s.checkValidity = cf.Sym(sec, "SecStaticCodeCreateWithPath"), cf.Sym(sec, "SecStaticCodeCheckValidityWithErrors")
	s.copySelf, s.copyDR = cf.Sym(sec, "SecCodeCopySelf"), cf.Sym(sec, "SecCodeCopyDesignatedRequirement")
	s.reqCreate, s.reqCopyString = cf.Sym(sec, "SecRequirementCreateWithString"), cf.Sym(sec, "SecRequirementCopyString")
	s.copySigningInfo, s.errorMsg = cf.Sym(sec, "SecCodeCopySigningInformation"), cf.Sym(sec, "SecCopyErrorMessageString")
	s.infoFlags = cf.Const(sec, "kSecCodeInfoFlags")
	s.dictGet, s.numberGet, s.errorDesc = cf.Sym(core, "CFDictionaryGetValue"), cf.Sym(core, "CFNumberGetValue"), cf.Sym(core, "CFErrorCopyDescription")
	s.renamex, s.errnoLoc = cf.Sym(libc, "renamex_np"), cf.Sym(libc, "__error")
	s.pidPath, s.listPids = cf.Sym(libc, "proc_pidpath"), cf.Sym(libc, "proc_listallpids")
	for _, p := range []uintptr{s.staticCreate, s.checkValidity, s.copySelf, s.copyDR, s.reqCreate, s.reqCopyString,
		s.copySigningInfo, s.errorMsg, s.infoFlags, s.dictGet, s.numberGet, s.errorDesc, s.renamex, s.errnoLoc, s.pidPath, s.listPids} {
		if p == 0 {
			s.err = cf.ErrUnavailable
		}
	}
}

func ready() error {
	sys.once.Do(sys.load)
	if sys.err != nil {
		return fmt.Errorf("selfupdate: system frameworks: %w", sys.err)
	}
	return nil
}

// statusText is Security's message for an OSStatus, with the number.
func statusText(fr *ffi.Frame, st int32) string {
	s := fr.Reset().Arg32(uint32(st)).Arg(0).Call(sys.errorMsg).R0()
	if s == 0 {
		return fmt.Sprintf("OSStatus %d", st)
	}
	defer cf.CFRelease(fr, s)
	return fmt.Sprintf("%s (OSStatus %d)", cf.GoString(fr, s), st)
}

// cfErrorText describes a CFErrorRef and releases it.
func cfErrorText(fr *ffi.Frame, e uintptr) string {
	if e == 0 {
		return ""
	}
	defer cf.CFRelease(fr, e)
	d := fr.Call1(sys.errorDesc, e)
	if d == 0 {
		return ""
	}
	defer cf.CFRelease(fr, d)
	return cf.GoString(fr, d)
}

// selfCode returns the running process's SecCodeRef (owned).
func selfCode(fr *ffi.Frame) (uintptr, error) {
	out := cf.Out(fr)
	if st := int32(fr.Reset().Arg32(0).ArgPtr(out).Call(sys.copySelf).R0()); st != 0 {
		return 0, fmt.Errorf("selfupdate: SecCodeCopySelf: %s", statusText(fr, st))
	}
	return cf.Word(fr), nil
}

// selfRequirement is the running app's designated requirement, as text.
func selfRequirement() (string, error) {
	if err := ready(); err != nil {
		return "", err
	}
	fr := cf.Acquire()
	defer cf.Release(fr)
	code, err := selfCode(fr)
	if err != nil {
		return "", err
	}
	defer cf.CFRelease(fr, code)
	out := cf.Out(fr)
	if st := int32(fr.Reset().Arg(code).Arg32(0).ArgPtr(out).Call(sys.copyDR).R0()); st != 0 {
		if st == errSecCSUnsigned {
			return "", ErrNotSigned
		}
		return "", fmt.Errorf("selfupdate: the app's designated requirement: %s", statusText(fr, st))
	}
	req := cf.Word(fr)
	defer cf.CFRelease(fr, req)
	out = cf.Out(fr)
	if st := int32(fr.Reset().Arg(req).Arg32(0).ArgPtr(out).Call(sys.reqCopyString).R0()); st != 0 {
		return "", fmt.Errorf("selfupdate: the app's designated requirement: %s", statusText(fr, st))
	}
	s := cf.Word(fr)
	defer cf.CFRelease(fr, s)
	text := cf.GoString(fr, s)
	if text == "" {
		return "", errors.New("selfupdate: the app's designated requirement is empty")
	}
	return text, nil
}

// selfAdhoc reports whether the running code is signed ad hoc (the linker's
// signature included) or not at all.
func selfAdhoc() (bool, error) {
	if err := ready(); err != nil {
		return false, err
	}
	fr := cf.Acquire()
	defer cf.Release(fr)
	code, err := selfCode(fr)
	if err != nil {
		return false, err
	}
	defer cf.CFRelease(fr, code)
	out := cf.Out(fr)
	if st := int32(fr.Reset().Arg(code).Arg32(csSigningInformation).ArgPtr(out).Call(sys.copySigningInfo).R0()); st != 0 {
		return false, fmt.Errorf("selfupdate: the app's signature: %s", statusText(fr, st))
	}
	info := cf.Word(fr)
	defer cf.CFRelease(fr, info)
	num := fr.Call2(sys.dictGet, info, sys.infoFlags)
	if num == 0 {
		return true, nil // no signature
	}
	v := &fr.Scratch()[1]
	*v = 0
	if !fr.Reset().Arg(num).Arg(cfNumberSInt64).ArgPtr(unsafe.Pointer(v)).Call(sys.numberGet).Bool() {
		return false, errors.New("selfupdate: the app's signature flags are unreadable")
	}
	return *v&codeSignatureAdhoc != 0, nil
}

// verifyCode checks the bundle's code signature, strictly and with its nested
// code, against the requirement (csreq text). Failures match ErrRejected.
func verifyCode(bundle, requirement string) error {
	if err := ready(); err != nil {
		return err
	}
	fr := cf.Acquire()
	defer cf.Release(fr)
	url := cf.FileURL(fr, bundle, true)
	if url == 0 {
		return fmt.Errorf("%w: %q cannot be a URL", ErrRejected, bundle)
	}
	defer cf.CFRelease(fr, url)
	out := cf.Out(fr)
	if st := int32(fr.Reset().Arg(url).Arg32(0).ArgPtr(out).Call(sys.staticCreate).R0()); st != 0 {
		return fmt.Errorf("%w: %s: %s", ErrRejected, bundle, statusText(fr, st))
	}
	code := cf.Word(fr)
	defer cf.CFRelease(fr, code)

	rs := cf.String(fr, requirement)
	if rs == 0 {
		return errors.New("selfupdate: the requirement cannot be a string")
	}
	out = cf.Out(fr)
	st := int32(fr.Reset().Arg(rs).Arg32(0).ArgPtr(out).Call(sys.reqCreate).R0())
	cf.CFRelease(fr, rs)
	if st != 0 {
		return fmt.Errorf("selfupdate: the requirement %q does not compile: %s", requirement, statusText(fr, st))
	}
	req := cf.Word(fr)
	defer cf.CFRelease(fr, req)

	out = cf.Out(fr)
	st = int32(fr.Reset().Arg(code).Arg32(verifyFlags).Arg(req).ArgPtr(out).Call(sys.checkValidity).R0())
	if st == 0 {
		return nil
	}
	detail := cfErrorText(fr, cf.Word(fr))
	if detail == "" {
		detail = statusText(fr, st)
	}
	var why string
	switch st {
	case errSecCSUnsigned:
		why = "the update is not signed"
	case errSecCSReqFailed:
		why = "the update's signature is not the running app's publisher's"
	case errSecCSSignatureFailed, errSecCSBadResource, errSecCSResourcesNotSealed, errSecCSResourcesNotFound,
		errSecCSResourcesInvalid, errSecCSInfoPlistFailed:
		why = "the update's signature does not match its contents"
	default:
		why = "the update's signature is not valid"
	}
	return fmt.Errorf("%w: %s: %s", ErrRejected, why, detail)
}

// bundleInfo is what an Info.plist says.
type bundleInfo struct{ id, version, build, exe string }

// readInfo reads the bundle's Info.plist (XML or binary) with Foundation.
func readInfo(bundle string) (bundleInfo, error) {
	if err := ready(); err != nil {
		return bundleInfo{}, err
	}
	fr := cf.Acquire()
	defer cf.Release(fr)
	defer cf.Pool(fr)()
	p := bundle + "/Contents/Info.plist"
	if fi, err := os.Lstat(p); err != nil {
		return bundleInfo{}, err
	} else if !fi.Mode().IsRegular() {
		return bundleInfo{}, fmt.Errorf("%s is not a file", p)
	}
	url := cf.FileURL(fr, p, false)
	if url == 0 {
		return bundleInfo{}, fmt.Errorf("%q cannot be a URL", p)
	}
	defer cf.CFRelease(fr, url)
	d := cf.Msg(fr, cf.Cls(fr, "NSDictionary"), cf.Sel(fr, "dictionaryWithContentsOfURL:error:"), url, uintptr(cf.Out(fr)))
	if d == 0 {
		return bundleInfo{}, fmt.Errorf("%s is not a property list", p)
	}
	str, isKind, get := cf.Cls(fr, "NSString"), cf.Sel(fr, "isKindOfClass:"), cf.Sel(fr, "objectForKey:")
	value := func(key string) string {
		k := cf.String(fr, key)
		v := cf.Msg(fr, d, get, k)
		cf.CFRelease(fr, k)
		if v == 0 || !cf.MsgBool(fr, v, isKind, str) {
			return ""
		}
		return cf.GoString(fr, v)
	}
	return bundleInfo{
		id:      value("CFBundleIdentifier"),
		version: value("CFBundleShortVersionString"),
		build:   value("CFBundleVersion"),
		exe:     value("CFBundleExecutable"),
	}, nil
}

// renameSwap exchanges two paths in one step (renamex_np, RENAME_SWAP).
func renameSwap(a, b string) error {
	if err := ready(); err != nil {
		return err
	}
	fr := cf.Acquire()
	defer cf.Release(fr)
	pa, pb := cString(a), cString(b)
	// errno is the thread's: read it on the thread that made the call.
	runtime.LockOSThread()
	r := int32(fr.Reset().ArgPtr(unsafe.Pointer(&pa[0])).ArgPtr(unsafe.Pointer(&pb[0])).Arg32(renameSwapFlag).Call(sys.renamex).R0())
	var e syscall.Errno
	if r != 0 {
		e = syscall.Errno(*(*int32)(ffi.PtrFromC(fr.Call0(sys.errnoLoc))))
	}
	runtime.UnlockOSThread()
	runtime.KeepAlive(pa)
	runtime.KeepAlive(pb)
	if r == 0 {
		return nil
	}
	return &os.LinkError{Op: "renamex_np", Old: a, New: b, Err: e}
}

// processPath is the executable of process pid (proc_pidpath), "" when there
// is no such process.
func processPath(pid int) string {
	if pid <= 0 || ready() != nil {
		return ""
	}
	fr := cf.Acquire()
	defer cf.Release(fr)
	buf := make([]byte, 4096) // PROC_PIDPATHINFO_MAXSIZE
	n := int32(fr.Reset().Arg32(uint32(pid)).ArgPtr(unsafe.Pointer(&buf[0])).Arg32(uint32(len(buf))).Call(sys.pidPath).R0())
	runtime.KeepAlive(buf)
	if n <= 0 || int(n) > len(buf) {
		return ""
	}
	return string(buf[:n])
}

// processesOf lists the processes this user may signal whose executable is
// exe (proc_listallpids, then proc_pidpath for each).
func processesOf(exe string) []int {
	if ready() != nil {
		return nil
	}
	fr := cf.Acquire()
	defer cf.Release(fr)
	n := int32(fr.Reset().Arg(0).Arg32(0).Call(sys.listPids).R0())
	if n <= 0 {
		return nil
	}
	buf := make([]int32, int(n)+64)
	n = int32(fr.Reset().ArgPtr(unsafe.Pointer(&buf[0])).Arg32(uint32(4 * len(buf))).Call(sys.listPids).R0())
	runtime.KeepAlive(buf)
	if n <= 0 || int(n) > len(buf) {
		return nil
	}
	var out []int
	for _, pid := range buf[:n] {
		if pid > 0 && int(pid) != os.Getpid() && processPath(int(pid)) == exe && syscall.Kill(int(pid), 0) == nil {
			out = append(out, int(pid))
		}
	}
	return out
}

func cString(s string) []byte {
	b := make([]byte, len(s)+1)
	copy(b, s)
	return b
}

// ---- Launch Services ----

type launched struct {
	pid int
	err string
}

var ws struct {
	once sync.Once
	err  error
	kind *objc.BlockKind

	selPID, selDesc uintptr

	mu      sync.Mutex
	next    uintptr
	waiting map[uintptr]chan launched
}

func loadWorkspace() {
	if ws.err = ready(); ws.err != nil {
		return
	}
	if _, ws.err = cf.Framework("AppKit"); ws.err != nil {
		return
	}
	fr := cf.Acquire()
	defer cf.Release(fr)
	ws.selPID, ws.selDesc = cf.Sel(fr, "processIdentifier"), cf.Sel(fr, "localizedDescription")
	ws.waiting = map[uintptr]chan launched{}
	// void (^)(NSRunningApplication *app, NSError *error), on a system queue.
	ws.kind = objc.NewBlockKind("v24@?0@8@16", true, func(ctx uintptr, c *ffi.CB) {
		app, nserr := c.Int(1), c.Int(2)
		fr := c.Frame()
		var r launched
		if app != 0 {
			r.pid = int(int32(fr.Reset().Arg(app).Arg(ws.selPID).Call(cf.MsgAddr()).R0()))
		}
		if nserr != 0 {
			r.err = cf.GoString(fr, fr.Reset().Arg(nserr).Arg(ws.selDesc).Call(cf.MsgAddr()).R0())
		}
		if r.err == "" && (nserr != 0 || app == 0) {
			r.err = "Launch Services opened nothing and did not say why"
		}
		ws.mu.Lock()
		ch := ws.waiting[ctx]
		ws.mu.Unlock()
		if ch != nil {
			select {
			case ch <- r:
			default:
			}
		}
	}, nil)
}

// launchServices opens the bundle as a new instance, activated, with args:
// NSWorkspace openApplicationAtURL:configuration:completionHandler:, which
// starts it the way the Dock or the Finder would and reports its process id.
// The pid is 0 when Launch Services has not answered within 10 s.
func launchServices(bundle string, args []string) (int, error) {
	ws.once.Do(loadWorkspace)
	if ws.err != nil {
		return 0, fmt.Errorf("selfupdate: Launch Services: %w", ws.err)
	}
	if fi, err := os.Stat(bundle); err != nil || !fi.IsDir() {
		return 0, fmt.Errorf("selfupdate: there is no app at %s", bundle)
	}
	ch := make(chan launched, 1)
	ws.mu.Lock()
	ws.next++
	ctx := ws.next
	ws.waiting[ctx] = ch
	ws.mu.Unlock()
	defer func() {
		ws.mu.Lock()
		delete(ws.waiting, ctx)
		ws.mu.Unlock()
	}()

	err := func() error {
		fr := cf.Acquire()
		defer cf.Release(fr)
		defer cf.Pool(fr)()
		workspace := cf.Msg(fr, cf.Cls(fr, "NSWorkspace"), cf.Sel(fr, "sharedWorkspace"))
		cfg := cf.Msg(fr, cf.Cls(fr, "NSWorkspaceOpenConfiguration"), cf.Sel(fr, "configuration"))
		url := cf.FileURL(fr, bundle, true)
		if workspace == 0 || cfg == 0 || url == 0 {
			cf.CFRelease(fr, url)
			return cf.ErrUnavailable
		}
		defer cf.CFRelease(fr, url)
		cf.Msg(fr, cfg, cf.Sel(fr, "setCreatesNewApplicationInstance:"), 1)
		cf.Msg(fr, cfg, cf.Sel(fr, "setActivates:"), 1)
		cf.Msg(fr, cfg, cf.Sel(fr, "setAddsToRecentItems:"), 0)
		// No alert of the system's own: a refusal comes back here, and the
		// helper rolls back.
		cf.Msg(fr, cfg, cf.Sel(fr, "setPromptsUserIfNeeded:"), 0)
		if len(args) > 0 {
			strs := make([]uintptr, len(args)+1)
			for i, a := range args {
				if strs[i] = cf.String(fr, a); strs[i] == 0 {
					for _, s := range strs[:i] {
						cf.CFRelease(fr, s)
					}
					return fmt.Errorf("argument %q cannot be a string", a)
				}
			}
			// arrayWithObjects:count: retains the strings (a CFArray made
			// without callbacks would not, and the configuration keeps it).
			arr := cf.Msg(fr, cf.Cls(fr, "NSArray"), cf.Sel(fr, "arrayWithObjects:count:"), uintptr(unsafe.Pointer(&strs[0])), uintptr(len(args)))
			runtime.KeepAlive(strs)
			if arr != 0 {
				cf.Msg(fr, cfg, cf.Sel(fr, "setArguments:"), arr)
			}
			for _, s := range strs[:len(args)] {
				cf.CFRelease(fr, s)
			}
			if arr == 0 {
				return errors.New("the arguments cannot be an array")
			}
		}
		open := cf.Sel(fr, "openApplicationAtURL:configuration:completionHandler:") // before the frame is filled: Sel uses it
		req, err := ws.kind.New(ctx)
		if err != nil {
			return err
		}
		fr.Reset().Arg(workspace).Arg(open).Arg(url).Arg(cfg).Arg(uintptr(req.Block())).Call(cf.MsgAddr())
		req.Sent()
		return nil
	}()
	if err != nil {
		return 0, fmt.Errorf("selfupdate: Launch Services: %w", err)
	}
	select {
	case r := <-ch:
		if r.err != "" {
			return 0, fmt.Errorf("selfupdate: Launch Services could not open %s: %s", bundle, r.err)
		}
		if r.pid <= 0 {
			return 0, nil // opened, and already gone
		}
		return r.pid, nil
	case <-time.After(10 * time.Second):
		// Launch Services answers when the app checks in (NSApplication's
		// launch), or when a process that never does ends. The helper finds
		// the process by its executable instead.
		return 0, nil
	}
}
