//go:build !darwin && !windows && !linux

package img

// iconDecoder has no state here.
type iconDecoder struct{}

// decodeIcon: the system has no icons this package can read.
func (w *worker) decodeIcon(c *Cache, j *job) result { return result{buf: -1, err: errUnsupported} }
