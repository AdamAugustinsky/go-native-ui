//go:build darwin

package perm

import (
	"sync"

	"gonativeui/app/shell"
	"gonativeui/internal/cf"
	"gonativeui/internal/ffi"
	"gonativeui/internal/objc"
)

// macOS: AVCaptureDevice. Everything is called through internal/cf (a frame
// of the caller's own), so any goroutine may ask. The completion handler is a
// per-request block (objc.BlockKind) whose context is the ticket; the system
// calls it once, on a queue of its own, and it only reads the state and
// completes the ticket.

var av struct {
	once sync.Once
	err  error

	device       uintptr // AVCaptureDevice
	audio, video uintptr // AVMediaTypeAudio, AVMediaTypeVideo
	bundle       uintptr // NSBundle

	selStatus, selRequest, selMain, selInfo uintptr

	blocks [Camera + 1]*objc.BlockKind
}

func load() error {
	av.once.Do(func() {
		if cf.Init() != nil {
			av.err = ErrUnavailable
			return
		}
		lib, err := cf.Framework("AVFoundation")
		if err != nil {
			av.err = ErrUnavailable
			return
		}
		fr := cf.Acquire()
		defer cf.Release(fr)
		av.device, av.bundle = cf.Cls(fr, "AVCaptureDevice"), cf.Cls(fr, "NSBundle")
		av.audio, av.video = cf.Const(lib, "AVMediaTypeAudio"), cf.Const(lib, "AVMediaTypeVideo")
		av.selStatus = cf.Sel(fr, "authorizationStatusForMediaType:")
		av.selRequest = cf.Sel(fr, "requestAccessForMediaType:completionHandler:")
		av.selMain, av.selInfo = cf.Sel(fr, "mainBundle"), cf.Sel(fr, "objectForInfoDictionaryKey:")
		if av.device == 0 || av.audio == 0 || av.video == 0 {
			av.err = ErrUnavailable
			return
		}
		for _, k := range [...]Kind{Microphone, Camera} {
			k := k
			// void (^)(BOOL granted)
			av.blocks[k] = objc.NewBlockKind("v12@?0B8", true, func(ctx uintptr, c *ffi.CB) {
				answered(c.Frame(), Ticket(ctx), k, byte(c.Int(1)) != 0)
			}, nil)
		}
	})
	return av.err
}

func mediaType(k Kind) uintptr {
	if k == Camera {
		return av.video
	}
	return av.audio
}

func status(fr *ffi.Frame, k Kind) State {
	return State(fr.Reset().Arg(av.device).Arg(av.selStatus).Arg(mediaType(k)).Call(cf.MsgAddr()).R0())
}

// answered is the completion handler: the system's thread, the callback's
// frame, no allocation.
func answered(fr *ffi.Frame, tk Ticket, k Kind, granted bool) {
	a, b := encode(k, granted, status(fr, k))
	requests().Complete(tk, a, b, nil)
}

func osStatus(k Kind) (State, error) {
	if err := load(); err != nil {
		return NotDetermined, err
	}
	fr := cf.Acquire()
	defer cf.Release(fr)
	return status(fr, k), nil
}

// usageKey is the Info.plist key macOS requires before it asks for k.
func usageKey(k Kind) string {
	if k == Camera {
		return "NSCameraUsageDescription"
	}
	return "NSMicrophoneUsageDescription"
}

// hasUsage is whether the main bundle's Info.plist has k's usage string
// (tests replace it).
var hasUsage = func(k Kind) bool {
	fr := cf.Acquire()
	defer cf.Release(fr)
	defer cf.Pool(fr)()
	key := cf.String(fr, usageKey(k))
	defer cf.CFRelease(fr, key)
	b := cf.Msg(fr, av.bundle, av.selMain)
	return b != 0 && cf.Msg(fr, b, av.selInfo, key) != 0
}

func osPrepare(k Kind) error {
	if err := load(); err != nil {
		return err
	}
	if !hasUsage(k) {
		return ErrNoUsageDescription
	}
	return nil
}

func osRequest(tk Ticket, k Kind) error {
	fr := cf.Acquire()
	defer cf.Release(fr)
	defer cf.Pool(fr)()
	blk, err := av.blocks[k].New(uintptr(tk))
	if err != nil {
		return ErrBusy
	}
	fr.Reset().Arg(av.device).Arg(av.selRequest).Arg(mediaType(k)).Arg(uintptr(blk.Block())).Call(cf.MsgAddr())
	blk.Sent()
	return nil
}

func osOpenSettings(k Kind) error {
	page := "Privacy_Microphone"
	if k == Camera {
		page = "Privacy_Camera"
	}
	return shell.OpenURLWith("x-apple.systempreferences:com.apple.preference.security?"+page,
		shell.URLOptions{Schemes: []string{"x-apple.systempreferences"}})
}
