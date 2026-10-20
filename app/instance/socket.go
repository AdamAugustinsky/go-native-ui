//go:build unix

package instance

import (
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"strconv"
)

// maxSocketPath is a safe length for a Unix-domain socket path: the limit is
// 104 bytes on macOS and the BSDs and 108 on Linux (sun_path), including the
// terminator.
const maxSocketPath = 100

// socketPath is where the primary listens for the application directory dir:
// instance.sock inside it, unless that would be too long, in which case a
// short directory of the user's own in the runtime or temporary directory is
// used (named after dir, so every copy of the application computes the same).
func socketPath(dir string) (string, error) {
	p := filepath.Join(dir, "instance.sock")
	if len(p) <= maxSocketPath {
		return p, nil
	}
	base := os.TempDir()
	if r := os.Getenv("XDG_RUNTIME_DIR"); runtime.GOOS != "darwin" && filepath.IsAbs(r) {
		base = r
	}
	sum := sha256.Sum256([]byte(dir))
	short := filepath.Join(base, "gnui-"+strconv.Itoa(os.Getuid())+"-"+hex.EncodeToString(sum[:6]))
	if err := os.MkdirAll(short, 0o700); err != nil {
		return "", err
	}
	short, err := checkPrivate(short)
	if err != nil {
		return "", err
	}
	p = filepath.Join(short, "s")
	if len(p) > maxSocketPath {
		return "", fmt.Errorf("instance: no short enough path for the socket (%s)", base)
	}
	return p, nil
}
