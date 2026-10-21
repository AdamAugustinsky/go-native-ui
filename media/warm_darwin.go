//go:build darwin

package media

import (
	"sync"
	"unsafe"

	"gonativeui/internal/cf"
)

var warmOnce sync.Once

// osWarm prepares a throwaway AVAudioPlayer of 10 ms of silence on a
// goroutine, through internal/cf (a frame of its own): the process's first
// player starts the audio stack (about 60 ms), and later ones cost about
// 8 ms.
func osWarm() {
	warmOnce.Do(func() {
		go func() {
			if cf.Init() != nil {
				return
			}
			if _, err := openAVFoundation(); err != nil {
				return
			}
			fr := cf.Acquire()
			defer cf.Release(fr)
			defer cf.Pool(fr)()
			f := Format{48000, 1, Int16}
			pcm := make([]byte, 480*f.FrameBytes())
			data := cf.Data(fr, append(appendWAVHeader(nil, f, len(pcm)), pcm...))
			out := make([]uintptr, 1) // heap: the NSError out parameter
			p := cf.Msg(fr, cf.Cls(fr, "AVAudioPlayer"), cf.Sel(fr, "alloc"))
			p = cf.Msg(fr, p, cf.Sel(fr, "initWithData:error:"), data, uintptr(unsafe.Pointer(&out[0])))
			cf.CFRelease(fr, data)
			if p != 0 {
				cf.MsgBool(fr, p, cf.Sel(fr, "prepareToPlay"))
				cf.Msg(fr, p, cf.Sel(fr, "release"))
			}
		}()
	})
}
