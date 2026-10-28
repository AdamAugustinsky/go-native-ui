//go:build windows

package login

import (
	"crypto/rand"
	"encoding/hex"
	"errors"
	"os"
	"syscall"
	"testing"

	"gonativeui/internal/winreg"
)

// TestRunValue enables a Run value, reads its command back, switches it off
// as Task Manager does, and checks that Status reports it and Enable leaves
// the user's choice alone; then disables it.
func TestRunValue(t *testing.T) {
	var rnd [4]byte
	rand.Read(rnd[:])
	name := "GnuiLoginTest" + hex.EncodeToString(rnd[:])
	t.Cleanup(func() {
		winreg.DeleteValue(runKey, name)
		winreg.DeleteValue(approvedKey, name)
	})
	item := RunValue(name, StartHiddenArg, "a b")
	if st, err := item.Status(); err != nil || st != NotRegistered {
		t.Fatalf("Status before Enable = %v, %v", st, err)
	}
	if st, err := item.Enable(); err != nil || st != Enabled {
		t.Fatalf("Enable = %v, %v", st, err)
	}
	exe, _ := os.Executable()
	want := syscall.EscapeArg(exe) + ` --start-hidden "a b"`
	if got, err := winreg.GetString(runKey, name); err != nil || got != want {
		t.Errorf("the Run value = %q, %v; want %q", got, err, want)
	}

	// Task Manager's "Disabled": 03 00 00 00 and a FILETIME.
	off := []byte{3, 0, 0, 0, 1, 2, 3, 4, 5, 6, 7, 8}
	k, err := winreg.Create(approvedKey)
	if err != nil {
		t.Fatal(err)
	}
	err = k.SetBinary(name, off)
	k.Close()
	if err != nil {
		t.Fatal(err)
	}
	if st, err := item.Status(); err != nil || st != RequiresApproval {
		t.Errorf("Status when switched off = %v, %v", st, err)
	}
	if st, err := item.Enable(); err != nil || st != RequiresApproval {
		t.Errorf("Enable when switched off = %v, %v", st, err)
	}
	k, _ = winreg.Open(approvedKey)
	b, _ := k.Binary(name)
	k.Close()
	if string(b) != string(off) {
		t.Errorf("Enable changed the user's switch: % x", b)
	}

	if st, err := item.Disable(); err != nil || st != NotRegistered {
		t.Errorf("Disable = %v, %v", st, err)
	}
	if _, err := winreg.GetString(runKey, name); !errors.Is(err, winreg.ErrNotExist) {
		t.Errorf("the Run value after Disable: %v", err)
	}
	if st, err := item.Disable(); err != nil || st != NotRegistered {
		t.Errorf("Disable again = %v, %v", st, err)
	}
}

func TestWindowsItems(t *testing.T) {
	if _, err := Agent("hidden").Status(); !errors.Is(err, ErrNotFound) {
		t.Errorf("Agent on Windows = %v, want ErrNotFound", err)
	}
	if st, err := App().Status(); err != nil || st != NotRegistered {
		t.Errorf("App().Status() of a test binary = %v, %v", st, err)
	}
	for _, bad := range []string{"", `a\b`} {
		if _, err := RunValue(bad).Status(); !errors.Is(err, ErrInvalid) {
			t.Errorf("RunValue(%q) = %v, want ErrInvalid", bad, err)
		}
	}
}
