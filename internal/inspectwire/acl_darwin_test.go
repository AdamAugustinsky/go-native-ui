//go:build darwin

package inspectwire

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

// aclDir makes a 0755 directory that carries an inheritable ACL granting
// everyone write access: what an administrator's or an installer's setup can
// leave. Its children inherit the entry.
func aclDir(t *testing.T) string {
	t.Helper()
	d := tmpDir(t)
	if err := os.Chmod(d, 0o755); err != nil {
		t.Fatal(err)
	}
	if out, err := exec.Command("/bin/chmod", "+a", "everyone allow write,file_inherit,directory_inherit", d).CombinedOutput(); err != nil {
		t.Skipf("cannot set an ACL here: %v %s", err, out)
	}
	return d
}

func TestACLReadAndStrip(t *testing.T) {
	d := aclDir(t)
	if has, err := hasACL(d); err != nil || !has {
		t.Fatalf("the directory has an ACL: %v %v", has, err)
	}
	child := filepath.Join(d, "child")
	if err := os.Mkdir(child, 0o700); err != nil {
		t.Fatal(err)
	}
	if has, err := hasACL(child); err != nil || !has {
		t.Fatalf("the child did not inherit it (the premise): %v %v", has, err)
	}
	if err := os.Chmod(child, 0o700); err != nil {
		t.Fatal(err)
	}
	if has, _ := hasACL(child); !has {
		t.Fatal("chmod removed the ACL: the premise of the staging directory is wrong")
	}
	if err := stripACL(child); err != nil {
		t.Fatal(err)
	}
	if has, err := hasACL(child); err != nil || has {
		t.Errorf("the ACL is still there: %v %v", has, err)
	}
	plain := tmpDir(t)
	if has, err := hasACL(plain); err != nil || has {
		t.Errorf("a plain directory: %v %v", has, err)
	}
	if err := stripACL(plain); err != nil {
		t.Errorf("stripping nothing: %v", err)
	}
	if _, err := hasACL(filepath.Join(plain, "missing")); err == nil {
		t.Error("a missing path read as 'no ACL'")
	}
}

// A directory with an ACL is refused as the socket's directory.
func TestListenRefusesADirectoryWithAnACL(t *testing.T) {
	d := aclDir(t)
	l, err := Listen(filepath.Join(d, "s"))
	if err == nil {
		l.Close()
		t.Fatal("a directory with an inheritable ACL was accepted")
	}
	if !strings.Contains(err.Error(), "access control list") {
		t.Errorf("the error does not say why: %v", err)
	}
	if ents, _ := os.ReadDir(d); len(ents) != 0 {
		t.Errorf("left behind: %v", ents)
	}
}

// Even where a directory with an ACL gets past the check, the socket is made in
// a staging directory stripped of it: the published socket has no ACL, the
// staging directory is gone, and the socket answers where it was renamed to.
func TestPublishStripsTheInheritedACL(t *testing.T) {
	d := aclDir(t)
	final := filepath.Join(d, "s")
	l, err := publish(d, final)
	if err != nil {
		t.Fatal(err)
	}
	defer l.Close()
	if has, err := hasACL(final); err != nil || has {
		t.Errorf("the published socket has an ACL: %v %v", has, err)
	}
	fi, _ := os.Lstat(final)
	if fi.Mode().Perm() != 0o600 {
		t.Errorf("mode %v", fi.Mode().Perm())
	}
	if ents, _ := os.ReadDir(d); len(ents) != 1 {
		t.Errorf("the staging directory was left: %v", ents)
	}
}
