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

// addACL puts an ACL entry on path with chmod(1).
func addACL(t *testing.T, path, entry string) {
	t.Helper()
	if out, err := exec.Command("/bin/chmod", "+a", entry, path).CombinedOutput(); err != nil {
		t.Skipf("cannot set an ACL here: %v %s", err, out)
	}
}

// Above the socket's directory an ACL matters when it allows someone to change
// what is in the directory (delete or rename its children) though the mode bits
// are closed; one that only denies, or only allows reading, does not.
func TestCheckDirReadsTheACLsOfAncestors(t *testing.T) {
	root := tmpDir(t)
	chain := func(name, entry string) string {
		a := filepath.Join(root, name)
		if err := os.Mkdir(a, 0o755); err != nil {
			t.Fatal(err)
		}
		addACL(t, a, entry)
		c := filepath.Join(a, "c")
		if err := os.Mkdir(c, 0o700); err != nil {
			t.Fatal(err)
		}
		return c
	}
	for _, entry := range []string{
		"everyone allow delete_child",
		"everyone allow delete",
		"everyone allow write",
		"everyone allow add_file",
		"everyone allow append",
		"everyone allow add_subdirectory",
		"everyone allow writesecurity",
		"everyone allow chown",
		"everyone allow writeattr",
		"everyone allow writeextattr",
		"everyone allow list,search,delete_child",
	} {
		c := chain(strings.ReplaceAll(entry, " ", "_"), entry)
		if _, err := CheckDir(c); err == nil || !strings.Contains(err.Error(), "ACL") {
			t.Errorf("an ancestor with %q: %v", entry, err)
		}
		if bad, err := aclGrantsMutation(filepath.Dir(c)); err != nil || !bad {
			t.Errorf("%q: grants mutation %v %v", entry, bad, err)
		}
	}
	for _, entry := range []string{
		"everyone deny delete",
		"everyone deny delete_child,write,chown",
		"everyone allow list,search,readattr,readextattr,readsecurity",
	} {
		c := chain(strings.ReplaceAll(entry, " ", "_"), entry)
		if _, err := CheckDir(c); err != nil {
			t.Errorf("an ancestor with %q refused: %v", entry, err)
		}
		if bad, err := aclGrantsMutation(filepath.Dir(c)); err != nil || bad {
			t.Errorf("%q: grants mutation %v %v", entry, bad, err)
		}
	}
	// a deny first and an allow after it: the allow is what counts
	c := chain("mixed", "everyone deny delete")
	addACL(t, filepath.Dir(c), "everyone allow delete_child")
	if _, err := CheckDir(c); err == nil {
		t.Error("an ancestor with a deny and an allow of delete_child was accepted")
	}
}

// What the real directories of this Mac carry, and that they pass: the chain
// under $TMPDIR has no ACL anywhere; the home folder and Library and Caches carry
// "everyone deny delete", which only denies.
func TestCheckDirAcceptsTheRealTempAndCachesChains(t *testing.T) {
	show := func(p string) {
		for ; ; p = filepath.Dir(p) {
			out, _ := exec.Command("/bin/ls", "-led", p).CombinedOutput()
			t.Logf("%s", strings.ReplaceAll(strings.TrimSpace(string(out)), "\n", " | "))
			if p == "/" {
				return
			}
		}
	}
	tmp, err := os.MkdirTemp("", "gi")
	if err != nil {
		t.Fatal(err)
	}
	defer os.RemoveAll(tmp)
	t.Log("the chain of $TMPDIR:")
	show(tmp)
	if _, err := CheckDir(tmp); err != nil {
		t.Errorf("a directory in $TMPDIR refused: %v", err)
	}
	home, err := os.UserHomeDir()
	if err != nil {
		t.Skip(err)
	}
	caches := filepath.Join(home, "Library", "Caches")
	if _, err := os.Stat(caches); err != nil {
		t.Skip(err)
	}
	sub, err := os.MkdirTemp(caches, "gi")
	if err != nil {
		t.Skip(err)
	}
	defer os.RemoveAll(sub)
	t.Log("the chain of ~/Library/Caches:")
	show(sub)
	if _, err := CheckDir(sub); err != nil {
		t.Errorf("a directory in ~/Library/Caches refused: %v", err)
	}
	// Caches itself carries an ACL, so it is not accepted as the socket's own directory
	if _, err := CheckDir(caches); err == nil {
		t.Log("~/Library/Caches has no ACL here, so it passes as a socket directory too")
	}
}
