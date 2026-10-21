//go:build darwin

package selfupdate

import (
	"bytes"
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"log/slog"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"syscall"
	"time"

	"gonativeui/app/paths"
)

const (
	mntReadOnly = 0x1 // MNT_RDONLY
	accessW     = 0x2 // W_OK
	accessX     = 0x1 // X_OK
)

// findSelf locates the running bundle from the executable's real path.
func findSelf() (self, error) {
	exe, err := os.Executable()
	if err != nil {
		return self{}, fmt.Errorf("%w: %v", ErrNotBundled, err)
	}
	if exe, err = filepath.EvalSymlinks(exe); err != nil {
		return self{}, fmt.Errorf("%w: %v", ErrNotBundled, err)
	}
	return selfAt(exe)
}

// selfAt is the bundle whose Contents/MacOS holds exe, which its Info.plist
// must name.
func selfAt(exe string) (self, error) {
	macos := filepath.Dir(exe)
	contents := filepath.Dir(macos)
	bundle := filepath.Dir(contents)
	if filepath.Base(macos) != "MacOS" || filepath.Base(contents) != "Contents" || !strings.HasSuffix(bundle, ".app") {
		return self{}, ErrNotBundled
	}
	for d := filepath.Dir(bundle); d != "/" && d != "."; d = filepath.Dir(d) {
		switch filepath.Ext(d) {
		case ".app", ".framework", ".bundle", ".appex", ".xpc", ".plugin":
			return self{}, fmt.Errorf("%w: %s is inside another bundle (%s)", ErrNotBundled, bundle, d)
		}
	}
	info, err := readInfo(bundle)
	if err != nil {
		return self{}, fmt.Errorf("%w: %v", ErrNotBundled, err)
	}
	if info.id == "" || info.exe != filepath.Base(exe) {
		return self{}, fmt.Errorf("%w: %s's Info.plist has no identifier or names another executable", ErrNotBundled, bundle)
	}
	return self{bundle: bundle, exe: exe, id: info.id, version: info.version, build: info.build}, nil
}

// Supported reports why this copy of the app cannot update itself, or nil
// when it can. The errors match ErrNotBundled, ErrTranslocated, ErrReadOnly,
// ErrNotWritable, ErrDevBundle or ErrNotSigned. It does a little file I/O
// and asks Security.framework about the app's own signature.
func Supported() error {
	s, err := findSelf()
	if err != nil {
		return err
	}
	return supportedAt(s, options().Requirement == "")
}

func supportedAt(s self, checkSignature bool) error {
	if strings.Contains(s.bundle, "/AppTranslocation/") {
		return fmt.Errorf("%w (%s)", ErrTranslocated, s.bundle)
	}
	var vfs syscall.Statfs_t
	if err := syscall.Statfs(s.bundle, &vfs); err != nil {
		return fmt.Errorf("selfupdate: %s: %w", s.bundle, err)
	}
	if vfs.Flags&mntReadOnly != 0 {
		return fmt.Errorf("%w (%s)", ErrReadOnly, s.bundle)
	}
	if strings.HasSuffix(s.id, ".dev") || strings.Contains(s.bundle, "/.gnui/dev/") {
		return fmt.Errorf("%w (%s)", ErrDevBundle, s.id)
	}
	var bst, pst syscall.Stat_t
	parent := filepath.Dir(s.bundle)
	if err := syscall.Lstat(s.bundle, &bst); err != nil {
		return fmt.Errorf("selfupdate: %s: %w", s.bundle, err)
	}
	if err := syscall.Lstat(parent, &pst); err != nil {
		return fmt.Errorf("selfupdate: %s: %w", parent, err)
	}
	if bst.Mode&syscall.S_IFMT != syscall.S_IFDIR {
		return fmt.Errorf("%w: %s is not a folder", ErrNotBundled, s.bundle)
	}
	for _, p := range []string{parent, s.bundle, filepath.Join(s.bundle, "Contents")} {
		if err := syscall.Access(p, accessW|accessX); err != nil {
			return fmt.Errorf("%w: %s is not writable (%v)", ErrNotWritable, p, err)
		}
	}
	if pst.Mode&syscall.S_ISVTX != 0 && int(bst.Uid) != os.Getuid() {
		return fmt.Errorf("%w: %s belongs to another user in a shared folder", ErrNotWritable, s.bundle)
	}
	if checkSignature {
		adhoc, err := selfAdhoc()
		if err != nil {
			return err
		}
		if adhoc {
			return ErrNotSigned
		}
	}
	return nil
}

// requirementFor is the requirement a staged bundle must satisfy.
func requirementFor(o Options) (string, error) {
	if o.Requirement != "" {
		return o.Requirement, nil
	}
	return selfRequirement()
}

// Prepare unpacks archive, a zip of the app's bundle (ditto -c -k
// --keepParent), into a new private folder on the running bundle's volume and
// verifies it (see the package comment). It fails with Supported's error when
// the app cannot update itself, and with an error matching ErrRejected when
// the archive or the bundle in it is refused; nothing is left behind then.
// It runs ditto and checks a code signature: call it from a task.
func Prepare(archive string) (*Staged, error) {
	o := options()
	s, err := findSelf()
	if err != nil {
		return nil, err
	}
	if err := supportedAt(s, o.Requirement == ""); err != nil {
		return nil, err
	}
	req, err := requirementFor(o)
	if err != nil {
		return nil, err
	}
	return prepare(s, req, o.AllowDowngrade, archive, o.Logger)
}

func prepare(s self, req string, anyVersion bool, archive string, lg *slog.Logger) (*Staged, error) {
	f, err := os.Open(archive)
	if err != nil {
		return nil, err
	}
	defer f.Close()
	fi, err := f.Stat()
	if err != nil {
		return nil, err
	}
	if !fi.Mode().IsRegular() {
		return nil, fmt.Errorf("%w: %s is not a file", ErrRejected, archive)
	}
	top, err := checkArchive(f, fi.Size())
	if err != nil {
		lg.Warn("selfupdate: archive refused", "archive", archive, "err", err)
		return nil, err
	}
	cleanStale(s, lg)
	dir, lock, err := makeStaging(s)
	if err != nil {
		return nil, fmt.Errorf("selfupdate: cannot make a staging folder on the volume of %s: %w", s.bundle, err)
	}
	ok := false
	defer func() {
		if !ok {
			removeStaging(dir)
			lock.Close()
		}
	}()
	newDir := filepath.Join(dir, "new")
	if err := os.Mkdir(newDir, 0o700); err != nil {
		return nil, err
	}
	if err := unpack(f, newDir); err != nil {
		lg.Warn("selfupdate: unpacking failed", "err", err)
		return nil, err
	}
	app, err := checkTree(newDir, top)
	if err != nil {
		lg.Warn("selfupdate: staged bundle refused", "err", err)
		return nil, err
	}
	info, err := verifyStaged(s.id, app, req)
	if err == nil && !anyVersion {
		err = checkNewer(info.version, info.build, s.version, s.build)
	}
	if err != nil {
		lg.Warn("selfupdate: staged bundle refused", "err", err)
		return nil, err
	}
	ok = true
	lg.Info("selfupdate: staged and verified the update", "version", info.version, "build", info.build, "path", app)
	return &Staged{dir: dir, app: app, version: info.version, build: info.build, self: s, lock: lock, anyVer: anyVersion}, nil
}

// verifyStaged checks the staged bundle's signature against req, then that it
// is the same app.
func verifyStaged(id, app, req string) (bundleInfo, error) {
	if err := verifyCode(app, req); err != nil {
		return bundleInfo{}, err
	}
	info, err := readInfo(app)
	if err != nil {
		return bundleInfo{}, fmt.Errorf("%w: %v", ErrRejected, err)
	}
	if info.id != id {
		return bundleInfo{}, fmt.Errorf("%w: the update is another app (identifier %q, not %q)", ErrRejected, info.id, id)
	}
	return info, nil
}

// stagingRoot is where updates are staged when it is on the bundle's
// volume: "selfupdate" in the app's cache folder (app/paths.Cache), private
// to the user.
func stagingRoot(id string) (string, error) {
	d, err := paths.Cache(id)
	if err != nil {
		return "", err
	}
	return filepath.Join(d, "selfupdate"), nil
}

// stagingPrefix names the hidden staging folders next to the bundle, the
// fallback when the cache folder is on another volume.
func stagingPrefix(bundle string) string {
	return "." + strings.TrimSuffix(filepath.Base(bundle), ".app") + ".selfupdate-"
}

// makeStaging makes the private folder an update is staged in, on the
// bundle's volume so that the swaps are renames: update-* in stagingRoot, or
// else a hidden folder next to the bundle. The folder holds a "lock" file,
// which marks it as ours; the returned file holds its lock (flock), which
// passes to the helper, so that no other process takes the folder for a
// stale one while it is in use.
func makeStaging(s self) (string, *os.File, error) {
	parent := filepath.Dir(s.bundle)
	var want syscall.Stat_t
	if err := syscall.Stat(parent, &want); err != nil {
		return "", nil, err
	}
	dir := ""
	if root, err := stagingRoot(s.id); err == nil && os.MkdirAll(root, 0o700) == nil {
		var st syscall.Stat_t
		if syscall.Lstat(root, &st) == nil && st.Dev == want.Dev && st.Mode&syscall.S_IFMT == syscall.S_IFDIR && int(st.Uid) == os.Getuid() {
			if r, err := filepath.EvalSymlinks(root); err == nil {
				if d, err := os.MkdirTemp(r, "update-*"); err == nil {
					dir = d
				}
			}
		}
	}
	if dir == "" {
		d, err := os.MkdirTemp(parent, stagingPrefix(s.bundle)+"*")
		if err != nil {
			return "", nil, err
		}
		dir = d
	}
	lock, err := os.OpenFile(filepath.Join(dir, "lock"), os.O_CREATE|os.O_EXCL|os.O_RDWR, 0o600)
	if err == nil {
		if err = syscall.Flock(int(lock.Fd()), syscall.LOCK_EX|syscall.LOCK_NB); err != nil {
			lock.Close()
		}
	}
	if err != nil {
		os.RemoveAll(dir)
		return "", nil, err
	}
	return dir, lock, nil
}

// isStaging reports whether dir is a staging folder of this user's: a real
// folder, owned by us, closed to others, with the lock file.
func isStaging(dir string) bool {
	if dir == "" || !filepath.IsAbs(dir) || filepath.Clean(dir) != dir {
		return false
	}
	var st, lk syscall.Stat_t
	if syscall.Lstat(dir, &st) != nil || st.Mode&syscall.S_IFMT != syscall.S_IFDIR || int(st.Uid) != os.Getuid() || st.Mode&0o077 != 0 {
		return false
	}
	return syscall.Lstat(filepath.Join(dir, "lock"), &lk) == nil && lk.Mode&syscall.S_IFMT == syscall.S_IFREG
}

// stagingInUse reports whether a process (a Prepare, a Staged not yet
// handed over, or the helper) holds the staging folder's lock.
func stagingInUse(dir string) bool {
	f, err := os.OpenFile(filepath.Join(dir, "lock"), os.O_RDONLY|syscall.O_NOFOLLOW, 0)
	if err != nil {
		return false
	}
	defer f.Close()
	if err := syscall.Flock(int(f.Fd()), syscall.LOCK_EX|syscall.LOCK_NB); err != nil {
		return errors.Is(err, syscall.EWOULDBLOCK)
	}
	syscall.Flock(int(f.Fd()), syscall.LOCK_UN)
	return false
}

// removeStaging removes a staging folder, and nothing that is not one. It
// removes the folder's contents and then the folder, so it never needs to
// read the folder above (Go's RemoveAll opens it).
func removeStaging(dir string) error {
	if !isStaging(dir) {
		return fmt.Errorf("selfupdate: %q is not a staging folder", dir)
	}
	ents, err := os.ReadDir(dir)
	if err != nil {
		return err
	}
	for _, e := range ents {
		if err := os.RemoveAll(filepath.Join(dir, e.Name())); err != nil {
			return err
		}
	}
	return os.Remove(dir)
}

// cleanStale removes staging folders that nothing holds any more: left by an
// app that quit without installing or discarding its update, or by a helper
// that was killed.
func cleanStale(s self, lg *slog.Logger) {
	var dirs []string
	if root, err := stagingRoot(s.id); err == nil {
		if r, err := filepath.EvalSymlinks(root); err == nil {
			ents, _ := os.ReadDir(r)
			for _, e := range ents {
				if strings.HasPrefix(e.Name(), "update-") {
					dirs = append(dirs, filepath.Join(r, e.Name()))
				}
			}
		}
	}
	parent, prefix := filepath.Dir(s.bundle), stagingPrefix(s.bundle)
	ents, _ := os.ReadDir(parent)
	for _, e := range ents {
		if strings.HasPrefix(e.Name(), prefix) {
			dirs = append(dirs, filepath.Join(parent, e.Name()))
		}
	}
	for _, d := range dirs {
		if isStaging(d) && !stagingInUse(d) {
			if err := removeStaging(d); err == nil {
				lg.Info("selfupdate: removed an update staged earlier", "path", d)
			}
		}
	}
}

// unpack runs ditto on the open archive (the bytes checkArchive read, not
// whatever the path names by now).
func unpack(f *os.File, dst string) error {
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Minute)
	defer cancel()
	cmd := exec.CommandContext(ctx, "/usr/bin/ditto", "-x", "-k", "--noqtn", "--noacl", "/dev/fd/3", dst)
	cmd.ExtraFiles = []*os.File{f}
	cmd.Env = []string{"PATH=/usr/bin:/bin", "LC_ALL=C"}
	var out bytes.Buffer
	cmd.Stdout, cmd.Stderr = &out, &out
	if err := cmd.Run(); err != nil {
		msg := strings.TrimSpace(out.String())
		if len(msg) > 2000 {
			msg = msg[:2000]
		}
		return fmt.Errorf("%w: ditto could not unpack the archive: %v: %s", ErrRejected, err, msg)
	}
	return nil
}

// checkTree checks what ditto made in dir: exactly the one bundle top, of
// folders, files and links only, all ours; no hard links; every link
// resolving, for real, inside the bundle. It clears setuid, setgid, sticky
// and group or other write bits, and gives the owner read and write (and
// search on folders), so that a later update can remove this one.
func checkTree(dir, top string) (string, error) {
	ents, err := os.ReadDir(dir)
	if err != nil {
		return "", err
	}
	if len(ents) != 1 || ents[0].Name() != top || !ents[0].IsDir() {
		return "", fmt.Errorf("%w: the archive did not unpack to exactly %q", ErrRejected, top)
	}
	app := filepath.Join(dir, top)
	root, err := filepath.EvalSymlinks(app)
	if err != nil {
		return "", err
	}
	uid := os.Getuid()
	err = filepath.WalkDir(app, func(p string, _ fs.DirEntry, err error) error {
		if err != nil {
			return fmt.Errorf("%w: %v", ErrRejected, err)
		}
		var st syscall.Stat_t
		if err := syscall.Lstat(p, &st); err != nil {
			return fmt.Errorf("%w: %v", ErrRejected, err)
		}
		rel := strings.TrimPrefix(p, dir+"/")
		if int(st.Uid) != uid {
			return fmt.Errorf("%w: %q belongs to another user", ErrRejected, rel)
		}
		switch st.Mode & syscall.S_IFMT {
		case syscall.S_IFDIR, syscall.S_IFREG:
			isDir := st.Mode&syscall.S_IFMT == syscall.S_IFDIR
			if !isDir && st.Nlink > 1 {
				return fmt.Errorf("%w: %q is a hard link", ErrRejected, rel)
			}
			perm := uint32(st.Mode) & 0o7777
			clean := perm &^ (syscall.S_ISUID | syscall.S_ISGID | syscall.S_ISVTX | 0o022)
			if isDir {
				clean |= 0o700
			} else {
				clean |= 0o600
			}
			if clean != perm {
				if err := os.Chmod(p, fs.FileMode(clean)); err != nil {
					return fmt.Errorf("%w: %v", ErrRejected, err)
				}
			}
		case syscall.S_IFLNK:
			target, err := os.Readlink(p)
			if err != nil {
				return fmt.Errorf("%w: %v", ErrRejected, err)
			}
			if filepath.IsAbs(target) {
				return fmt.Errorf("%w: link %q points to the absolute path %q", ErrRejected, rel, target)
			}
			real, err := filepath.EvalSymlinks(p)
			if err != nil {
				return fmt.Errorf("%w: link %q does not resolve: %v", ErrRejected, rel, err)
			}
			if real != root && !strings.HasPrefix(real, root+"/") {
				return fmt.Errorf("%w: link %q points outside the bundle", ErrRejected, rel)
			}
		default:
			return fmt.Errorf("%w: %q is not a file, folder or link", ErrRejected, rel)
		}
		return nil
	})
	if err != nil {
		return "", err
	}
	return app, nil
}

// inode is the device and inode of path itself (not what a link names).
func inode(path string) (dev int64, ino uint64, err error) {
	var st syscall.Stat_t
	if err := syscall.Lstat(path, &st); err != nil {
		return 0, 0, err
	}
	return int64(st.Dev), st.Ino, nil
}

// job is what the app hands the helper, in the staging folder.
type job struct {
	Parent        int           `json:"parent"`
	Bundle        string        `json:"bundle"`
	Dev           int64         `json:"dev"`
	Ino           uint64        `json:"ino"`
	Staging       string        `json:"staging"`
	Staged        string        `json:"staged"`
	ID            string        `json:"id"`
	From          string        `json:"from"`
	To            string        `json:"to"`
	Args          []string      `json:"args"`
	HealthTimeout time.Duration `json:"healthTimeout"`
	QuitTimeout   time.Duration `json:"quitTimeout"`
	Nonce         string        `json:"nonce"`
	AnyVersion    bool          `json:"anyVersion"` // Options.AllowDowngrade, as Prepare used it
}

// InstallAndRelaunch hands the staged update to the helper and returns; the
// app must then quit. The helper swaps the update in once the app has
// exited, launches it with args, and rolls back if it does not call Started
// in time (see the package comment). A Staged is installed once.
func (s *Staged) InstallAndRelaunch(args []string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.used {
		return errors.New("selfupdate: this update was already installed or discarded")
	}
	o := options()
	cur, err := findSelf()
	if err != nil {
		return err
	}
	if cur.bundle != s.self.bundle || cur.exe != s.self.exe {
		return fmt.Errorf("selfupdate: the app moved since the update was staged (%s)", cur.bundle)
	}
	if err := supportedAt(cur, o.Requirement == ""); err != nil {
		return err
	}
	if fi, err := os.Lstat(s.app); err != nil || !fi.IsDir() {
		return fmt.Errorf("selfupdate: the staged update is gone (%s)", s.app)
	}
	dev, ino, err := inode(cur.bundle)
	if err != nil {
		return err
	}
	// The copy keeps the executable's name (what Activity Monitor shows, and
	// what tells a test binary's copy for one, internal/launchguard).
	helperDir := filepath.Join(s.dir, "helper")
	if err := os.Mkdir(helperDir, 0o700); err != nil {
		return fmt.Errorf("selfupdate: cannot copy the helper: %w", err)
	}
	helper := filepath.Join(helperDir, filepath.Base(cur.exe))
	if err := copyExecutable(cur.exe, helper); err != nil {
		os.Remove(helperDir)
		return fmt.Errorf("selfupdate: cannot copy the helper: %w", err)
	}
	nonce := make([]byte, 16)
	if _, err := rand.Read(nonce); err != nil {
		return err
	}
	j := job{
		Parent: os.Getpid(), Bundle: cur.bundle, Dev: dev, Ino: ino,
		Staging: s.dir, Staged: s.app, ID: cur.id, From: cur.version, To: s.version,
		Args: append([]string(nil), args...), HealthTimeout: o.HealthTimeout, QuitTimeout: o.QuitTimeout,
		Nonce: hex.EncodeToString(nonce), AnyVersion: s.anyVer,
	}
	jobPath := filepath.Join(s.dir, "job.json")
	b, err := json.Marshal(j)
	if err != nil {
		return err
	}
	if err := writeNew(jobPath, b); err != nil {
		os.RemoveAll(helperDir)
		return err
	}
	cmd := exec.Command(helper, helperFlag, jobPath)
	cmd.Dir = "/"
	cmd.ExtraFiles = []*os.File{s.lock}                  // fd 3: the staging folder's lock, held from Prepare to the helper's end
	cmd.SysProcAttr = &syscall.SysProcAttr{Setsid: true} // outlives the app's terminal session, if any
	if err := cmd.Start(); err != nil {
		os.RemoveAll(helperDir)
		os.Remove(jobPath)
		return fmt.Errorf("selfupdate: cannot start the helper: %w", err)
	}
	go cmd.Wait() // reaps it should the app outlive it
	s.lock.Close()
	s.lock, s.used = nil, true
	o.Logger.Info("selfupdate: the helper installs the update when the app quits",
		"helper", cmd.Process.Pid, "from", cur.version, "to", s.version)
	return nil
}

// Discard removes the staged update. It does nothing once
// InstallAndRelaunch has handed the update to the helper.
func (s *Staged) Discard() error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.used {
		return nil
	}
	s.used = true
	err := removeStaging(s.dir)
	s.lock.Close()
	s.lock = nil
	return err
}

// copyExecutable copies the running executable (its embedded signature
// included) to a new file only the user can run.
func copyExecutable(src, dst string) error {
	in, err := os.Open(src)
	if err != nil {
		return err
	}
	defer in.Close()
	out, err := os.OpenFile(dst, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0o700)
	if err != nil {
		return err
	}
	if _, err := io.Copy(out, in); err != nil {
		out.Close()
		os.Remove(dst)
		return err
	}
	if err := out.Close(); err != nil {
		os.Remove(dst)
		return err
	}
	return nil
}

// writeNew writes a file that must not exist yet, readable by the user only.
func writeNew(path string, b []byte) error {
	f, err := os.OpenFile(path, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0o600)
	if err != nil {
		return err
	}
	if _, err := f.Write(b); err != nil {
		f.Close()
		os.Remove(path)
		return err
	}
	return f.Close()
}

// writeAtomic replaces path with b through a new file and a rename.
func writeAtomic(path string, b []byte) error {
	tmp := fmt.Sprintf("%s.%d.tmp", path, os.Getpid())
	os.Remove(tmp)
	if err := writeNew(tmp, b); err != nil {
		return err
	}
	if err := os.Rename(tmp, path); err != nil {
		os.Remove(tmp)
		return err
	}
	return nil
}
