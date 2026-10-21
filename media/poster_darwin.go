//go:build darwin

package media

import (
	"errors"
	"runtime"

	"gonativeui/internal/objc"
	"gonativeui/internal/videoframe"
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
		asset := videoframe.Asset(f, path)
		pix, w, h := videoframe.Poster(asset, maxSide)
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
