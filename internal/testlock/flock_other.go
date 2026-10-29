//go:build !unix && !windows

package testlock

import "os"

// Elsewhere tests are not serialized across processes.
func lock(*os.File) error   { return nil }
func unlock(*os.File) error { return nil }
