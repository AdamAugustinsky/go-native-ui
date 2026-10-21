//go:build linux

package inspectwire

import (
	"os"
	"os/exec"
	"path/filepath"
	"testing"
)

// A directory with a default POSIX ACL hands it to what is created in it: refused.
func TestListenRefusesADirectoryWithADefaultACL(t *testing.T) {
	d := tmpDir(t)
	if out, err := exec.Command("setfacl", "-d", "-m", "u:nobody:rwx", d).CombinedOutput(); err != nil {
		t.Skipf("cannot set a default ACL here: %v %s", err, out)
	}
	if has, err := dirHasACL(d); err != nil || !has {
		t.Skipf("the file system does not report it: %v %v", has, err)
	}
	if l, err := Listen(filepath.Join(d, "s")); err == nil {
		l.Close()
		t.Fatal("a directory with a default ACL was accepted")
	}
	if ents, _ := os.ReadDir(d); len(ents) != 0 {
		t.Errorf("left behind: %v", ents)
	}
}
