//go:build !windows

package prefs

import "os"

// moveFile renames src over dst; rename(2) replaces atomically.
func moveFile(src, dst string) error { return os.Rename(src, dst) }

// syncDir flushes the directory so a completed rename or creation survives a
// power loss. A file system that cannot fsync a directory is not an error.
func syncDir(dir string) error {
	d, err := os.Open(dir)
	if err != nil {
		return err
	}
	defer d.Close()
	if err := d.Sync(); err != nil && !fsyncUnsupported(err) {
		return err
	}
	return nil
}
