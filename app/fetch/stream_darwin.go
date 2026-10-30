//go:build darwin

package fetch

import (
	"runtime"
	"sync"
	"sync/atomic"
	"unsafe"

	"github.com/ebitengine/purego"

	"gonativeui/internal/cf"
	"gonativeui/internal/ffi"
	"gonativeui/internal/nsurl"
)

// Bound streams are written on their notification queue. A callback merely
// waking another thread can lose the next writable edge while CF is still
// dispatching the previous one. Submissions and space callbacks instead run
// the same nonblocking flush, serialized on one queue.
var uploadStreams struct {
	once                                           sync.Once
	err                                            error
	client, dispatch, queue, async, callback, work uintptr
	next                                           atomic.Uint64
	live                                           sync.Map // integer context token -> *uploadStream
}

type uploadStream struct {
	mu      sync.Mutex
	wake    chan struct{} // one submitted buffer drained, or the stream ended
	stream  uintptr
	token   uint64
	pending []byte // caller keeps this buffer unchanged until wake
	stopped bool
	ended   bool
	sent    atomic.Int64
}

func watchUpload(fr *ffi.Frame, stream uintptr) (*uploadStream, error) {
	uploadStreams.once.Do(func() {
		core := cf.CoreFoundation()
		uploadStreams.client = cf.Sym(core, "CFWriteStreamSetClient")
		uploadStreams.dispatch = cf.Sym(core, "CFWriteStreamSetDispatchQueue")
		lib, err := purego.Dlopen("/usr/lib/libSystem.B.dylib", purego.RTLD_NOW|purego.RTLD_GLOBAL)
		if err != nil {
			uploadStreams.err = err
			return
		}
		create := cf.Sym(lib, "dispatch_queue_create")
		uploadStreams.async = cf.Sym(lib, "dispatch_async_f")
		if uploadStreams.client == 0 || uploadStreams.dispatch == 0 || create == 0 || uploadStreams.async == 0 {
			uploadStreams.err = cf.ErrUnavailable
			return
		}
		name := "gonativeui.fetch.upload\x00"
		uploadStreams.queue = fr.Call2(create, uintptr(unsafe.Pointer(unsafe.StringData(name))), 0)
		if uploadStreams.queue == 0 {
			uploadStreams.err = cf.ErrUnavailable
			return
		}
		uploadStreams.callback = ffi.NewCallback(func(c *ffi.CB) {
			if v, ok := uploadStreams.live.Load(uint64(c.Int(2))); ok {
				u := v.(*uploadStream)
				u.flush(c.Frame(), c.Int(1)&(8|16) != 0)
			}
		})
		uploadStreams.work = ffi.NewCallback(func(c *ffi.CB) {
			if v, ok := uploadStreams.live.Load(uint64(c.Int(0))); ok {
				v.(*uploadStream).flush(c.Frame(), false)
			}
		})
	})
	if uploadStreams.err != nil {
		return nil, uploadStreams.err
	}
	u := &uploadStream{wake: make(chan struct{}, 1), stream: stream, token: uploadStreams.next.Add(1)}
	uploadStreams.live.Store(u.token, u)
	// CF copies the context. Its info is an integer, never a retained Go pointer.
	ctx := &[5]uintptr{0, uintptr(u.token), 0, 0, 0}
	ok := fr.Reset().Arg(stream).Arg(4 | 8 | 16).Arg(uploadStreams.callback).
		ArgPtr(unsafe.Pointer(ctx)).Call(uploadStreams.client).Bool()
	runtime.KeepAlive(ctx)
	if !ok {
		uploadStreams.live.Delete(u.token)
		return nil, cf.ErrUnavailable
	}
	fr.Call2(uploadStreams.dispatch, stream, uploadStreams.queue)
	return u, nil
}

func (u *uploadStream) submit(fr *ffi.Frame, p []byte) {
	u.mu.Lock()
	u.pending = p
	u.mu.Unlock()
	fr.Call3(uploadStreams.async, uploadStreams.queue, uintptr(u.token), uploadStreams.work)
}

// flush never waits for stream space. CF calls it again once the reader
// frees room; a submission schedules it even if the stream was writable all
// along and has no new edge to report. The queue serializes these callouts.
func (u *uploadStream) flush(fr *ffi.Frame, ended bool) {
	u.mu.Lock()
	defer u.mu.Unlock()
	if u.stopped {
		return
	}
	u.ended = u.ended || ended
	if len(u.pending) == 0 {
		return
	}
	for !u.ended && len(u.pending) > 0 && nsurl.CanWrite(fr, u.stream) {
		n := nsurl.WriteStream(fr, u.stream, u.pending)
		runtime.KeepAlive(u.pending)
		if n < 0 {
			u.ended = true
			break
		}
		if n == 0 {
			break
		}
		u.sent.Add(int64(n))
		u.pending = u.pending[n:]
	}
	if u.ended || len(u.pending) == 0 {
		select {
		case u.wake <- struct{}{}:
		default:
		}
	}
}

func (u *uploadStream) finished() bool {
	u.mu.Lock()
	defer u.mu.Unlock()
	return u.ended
}

func (u *uploadStream) close(fr *ffi.Frame) {
	u.mu.Lock()
	defer u.mu.Unlock()
	if u.stopped {
		return
	}
	u.stopped = true
	u.pending = nil
	fr.Call2(uploadStreams.dispatch, u.stream, 0)
	fr.Call4(uploadStreams.client, u.stream, 0, 0, 0)
	nsurl.CloseStream(fr, u.stream)
	uploadStreams.live.Delete(u.token)
}
