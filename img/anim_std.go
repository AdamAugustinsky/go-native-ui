//go:build !darwin

package img

// Which formats beyond GIF and APNG the system can animate: none off macOS.
const (
	osAnimWebP = false
	osAnimHEIF = false
)

// openSystemDecoder opens a GIF or an APNG with the standard library.
func (w *worker) openSystemDecoder(data []byte) (animDecoder, error) {
	return openGoDecoder(data, true)
}

// systemAutoplay: animations play (Windows' and Linux's equivalent of
// Reduce Motion reaches the cache through Present.ReduceMotion).
func systemAutoplay(*cframe) bool { return true }

// watchAutoplay and unwatchAutoplay register a cache for the setting's change
// notification: there is none to observe here.
func watchAutoplay(*Cache)   {}
func unwatchAutoplay(*Cache) {}
