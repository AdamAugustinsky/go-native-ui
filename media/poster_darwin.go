//go:build darwin

package media

import (
	"errors"
	"runtime"
	"unsafe"

	"gonativeui/internal/ffi"
	"gonativeui/internal/objc"
)

// LoadVideoPoster starts reading the first frame of a local video, without a
// player or player item. Call it on the main thread. maxSide limits the longest
// side in pixels (default 1280); the track's preferred transform is applied.
// On success done owns tightly packed, premultiplied sRGB RGBA bytes. File and
// decoding errors arrive through done with nil pixels and zero dimensions.
// done runs once on a worker, not the main thread; dispatch UI work to the app.
// If this call returns an error (including a nil done), done is not called.
func LoadVideoPoster(path string, maxSide int, done func(pix []byte, w, h int, err error)) error {
	if done == nil {
		return ErrInvalid
	}
	if err := loadVideoAV(); err != nil {
		return err
	}
	go func() {
		runtime.LockOSThread()
		defer runtime.UnlockOSThread()
		s := new(loadScratch)
		f := &s.f
		pool := f.Call0(objc.PoolPush)
		asset := videoAsset(f, path)
		pix, w, h := videoPoster(s, asset, maxSide)
		f.Call1(objc.PoolPop, pool)
		s.send(asset, selRelease)
		var err error
		if pix == nil {
			err = errors.New("media: cannot read video poster")
		}
		done(pix, w, h, err)
	}()
	return nil
}

// videoAsset returns a retained asset, using only the caller's thread frame.
// The caller supplies an autorelease pool and releases the asset when done.
func videoAsset(f *ffi.Frame, path string) objc.ID {
	b := []byte(path)
	if len(b) == 0 {
		b = []byte{0}
	}
	str := f.Reset().Arg(uintptr(vav.clsString)).Arg(uintptr(selAlloc)).Call(objc.MsgSend).R0()
	str = f.Reset().Arg(str).Arg(uintptr(selStrInit)).Arg(uintptr(unsafe.Pointer(&b[0]))).
		Arg(uintptr(len(path))).Arg(4).Call(objc.MsgSend).R0() // NSUTF8StringEncoding
	runtime.KeepAlive(b)
	url := f.Reset().Arg(uintptr(vav.clsURL)).Arg(uintptr(selFileURL)).Arg(str).Call(objc.MsgSend).R0()
	f.Reset().Arg(str).Arg(uintptr(selRelease)).Call(objc.MsgSend)
	asset := f.Reset().Arg(uintptr(vav.clsAsset)).Arg(uintptr(selAssetWithURL)).Arg(url).Arg(0).Call(objc.MsgSend).R0()
	return objc.ID(f.Reset().Arg(asset).Arg(uintptr(selRetain)).Call(objc.MsgSend).R0())
}
