//go:build !darwin

package img

// Which formats beyond GIF and APNG can animate off macOS: WebP, through
// x/image/webp (anim_webp.go); HEIF cannot.
const (
	osAnimWebP = true
	osAnimHEIF = false
)

// openSystemDecoder opens a GIF or an APNG with the standard library, an
// animated WebP with x/image/webp.
func (w *worker) openSystemDecoder(data []byte) (animDecoder, error) {
	if sniff(data) == "webp" {
		a, err := openWebP(data)
		if err != nil {
			return nil, err
		}
		return stdAnim{a}, nil
	}
	return openGoDecoder(data, true)
}

// systemAutoplay: animations play (Windows' and Linux's equivalent of
// Reduce Motion reaches the cache through Present.ReduceMotion).
func systemAutoplay(*cframe) bool { return true }

// watchAutoplay and unwatchAutoplay register a cache for the setting's change
// notification: there is none to observe here.
func watchAutoplay(*Cache)   {}
func unwatchAutoplay(*Cache) {}
