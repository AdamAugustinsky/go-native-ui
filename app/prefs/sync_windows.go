package prefs

import "os"

// syncDir does nothing: Windows has no directory flush, and renames made with
// MoveFileEx are written through by the file system's journal.
func syncDir(string) {}

func syncFile(f *os.File, _ bool) error { return f.Sync() }
