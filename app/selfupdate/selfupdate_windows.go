//go:build windows

package selfupdate

import (
	"archive/zip"
	"bytes"
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"syscall"
	"time"

	"gonativeui/app/paths"
	"gonativeui/internal/launchguard"
	"gonativeui/internal/winproc"
	"gonativeui/internal/winres"
)

// Windows (D76). The app is one executable and the files it declares beside
// it (winres.FilesKey: the DLLs of gnui.json's nativeLibs and their
// licences). The update is a newer build: the .exe alone (for an app that
// declares no files), or the zip gnui package writes, which holds the .exe
// and exactly the files the new build declares, all at its top. It is
// staged in a private folder on the executable's volume and swapped in, all
// files together, by renames once the app has exited.

// maxExe bounds the size of a staged executable.
const maxExe = 1 << 30

// findSelf describes the running executable from its version resource.
func findSelf() (self, error) {
	exe, err := os.Executable()
	if err != nil {
		return self{}, fmt.Errorf("%w: %v", ErrNotBundled, err)
	}
	return selfAt(filepath.Clean(exe))
}

// exeInfo is what a Windows executable says about itself.
type exeInfo struct {
	id, version, build, display string
	machine                     uint16
	signed                      bool     // it has a certificate table
	files                       []string // the files it declares beside it (winres.FilesKey)
}

// readExe reads the machine type, the certificate table's presence and the
// version resource of the executable f.
func readExe(f *os.File) (exeInfo, error) {
	st, err := f.Stat()
	if err != nil {
		return exeInfo{}, err
	}
	pf, err := winres.Read(f, st.Size())
	if err != nil {
		return exeInfo{}, err
	}
	info := exeInfo{machine: pf.Machine, signed: pf.Signed}
	r, ok := winres.Find(pf.Resources, winres.TypeVersion, 1)
	if !ok {
		return info, errors.New("the executable has no version resource")
	}
	v, err := winres.ParseVersion(r.Data)
	if err != nil {
		return info, err
	}
	q := v.File
	info.version = fmt.Sprintf("%d.%d.%d", q[0], q[1], q[2])
	info.build = strconv.Itoa(int(q[3]))
	info.id = v.String("AppUserModelID")
	info.display = v.String("ProductVersion")
	if info.display == "" {
		info.display = info.version
	}
	if info.files, err = winres.Files(v); err != nil {
		return info, err
	}
	return info, nil
}

func readExePath(path string) (exeInfo, error) {
	f, err := os.Open(path)
	if err != nil {
		return exeInfo{}, err
	}
	defer f.Close()
	return readExe(f)
}

func selfAt(exe string) (self, error) {
	info, err := readExePath(exe)
	if err != nil {
		return self{}, fmt.Errorf("%w: %s: %v (gnui package writes the version resource)", ErrNotBundled, exe, err)
	}
	if info.id == "" {
		return self{}, fmt.Errorf("%w: %s's version resource has no AppUserModelID (gnui package writes it)", ErrNotBundled, exe)
	}
	return self{bundle: exe, exe: exe, id: info.id, version: info.version, build: info.build}, nil
}

// Supported reports why this copy of the app cannot update itself, or nil
// when it can: on Windows ErrNotBundled (no version resource naming the
// app), ErrDevBundle (a go run or go test binary), ErrNotWritable
// (installed for all users, or its folder not writable) or ErrNotSigned (no
// valid Authenticode signature, and Options.Unsigned is not set).
func Supported() error {
	s, err := findSelf()
	if err != nil {
		return err
	}
	_, err = supportedAt(s, options())
	return err
}

// supportedAt checks s and returns its publisher (nil when it is unsigned
// and the options allow that).
func supportedAt(s self, o Options) (*publisher, error) {
	l := strings.ToLower(s.exe)
	tmp := strings.ToLower(filepath.Clean(os.TempDir())) + `\`
	if launchguard.TestBinary() || strings.HasPrefix(l, tmp) && strings.Contains(l, `\go-build`) {
		return nil, fmt.Errorf("%w (%s)", ErrDevBundle, s.exe)
	}
	for _, v := range []string{"ProgramFiles", "ProgramFiles(x86)", "ProgramW6432", "SystemRoot"} {
		if d := os.Getenv(v); d != "" && strings.HasPrefix(l, strings.ToLower(filepath.Clean(d))+`\`) {
			return nil, fmt.Errorf("%w: %s is installed for all users (%s)", ErrNotWritable, s.exe, d)
		}
	}
	probe, err := os.CreateTemp(filepath.Dir(s.exe), ".selfupdate-probe-*")
	if err != nil {
		return nil, fmt.Errorf("%w: %s (%v)", ErrNotWritable, filepath.Dir(s.exe), err)
	}
	probe.Close()
	os.Remove(probe.Name())
	pub, err := signerOf(s.exe)
	if err != nil {
		if o.Unsigned {
			return nil, nil
		}
		return nil, fmt.Errorf("%w (%v)", ErrNotSigned, err)
	}
	return pub, nil
}

// Prepare stages path, the new executable or a zip holding it, for a signed
// app (see the package comment). An unsigned app uses PrepareSHA256.
func Prepare(archive string) (*Staged, error) {
	return prepareWin(archive, nil)
}

// PrepareSHA256 is Prepare for an update whose SHA-256 the app took from a
// manifest it trusts: the file must have that digest (checked on the bytes
// that are staged). It is how an unsigned app (Options.Unsigned) updates.
func PrepareSHA256(archive string, sum [32]byte) (*Staged, error) {
	return prepareWin(archive, &sum)
}

func prepareWin(archive string, sum *[32]byte) (*Staged, error) {
	o := options()
	s, err := findSelf()
	if err != nil {
		return nil, err
	}
	pub, err := supportedAt(s, o)
	if err != nil {
		return nil, err
	}
	return prepare(s, pub, sum, o.AllowDowngrade, archive, o.Logger)
}

// prepare stages archive for the running executable s, whose publisher is
// pub (nil: unsigned, and sum must be given).
func prepare(s self, pub *publisher, sum *[32]byte, anyVersion bool, archive string, lg *slog.Logger) (*Staged, error) {
	if pub == nil && sum == nil {
		return nil, fmt.Errorf("%w: an unsigned app updates only with Options.Unsigned and a digest it verified (PrepareSHA256)", ErrNotSigned)
	}
	cleanStale(s, lg)
	dir, lock, err := makeStaging(s)
	if err != nil {
		return nil, fmt.Errorf("selfupdate: cannot make a staging folder on the volume of %s: %w", s.exe, err)
	}
	ok := false
	defer func() {
		if !ok {
			syscall.CloseHandle(lock)
			removeStaging(dir)
		}
	}()
	staged, files, digest, err := stageUpdate(archive, dir, filepath.Base(s.exe))
	if err != nil {
		lg.Warn("selfupdate: archive refused", "archive", archive, "err", err)
		return nil, err
	}
	if sum != nil && digest != *sum {
		err := fmt.Errorf("%w: the file's SHA-256 is %x, not the one the app expects", ErrRejected, digest)
		lg.Warn("selfupdate: staged update refused", "err", err)
		return nil, err
	}
	info, err := verifyStaged(s, staged, pub, anyVersion)
	if err == nil {
		err = checkFiles(info, filepath.Dir(staged), files, pub)
	}
	if err != nil {
		lg.Warn("selfupdate: staged update refused", "err", err)
		return nil, err
	}
	exeSum, err := sha256File(staged)
	if err != nil {
		return nil, err
	}
	sums := make([]jobFile, len(files))
	for i, n := range files {
		d, err := sha256File(filepath.Join(filepath.Dir(staged), n))
		if err != nil {
			return nil, err
		}
		sums[i] = jobFile{Name: n, Digest: hex.EncodeToString(d[:])}
	}
	ok = true
	lg.Info("selfupdate: staged and verified the update", "version", info.display, "path", staged, "files", len(files))
	st := &Staged{dir: dir, app: staged, version: info.display, build: info.build, self: s, anyVer: anyVersion}
	st.win = winStaged{lock: lock, exeSum: exeSum, pub: pub, files: sums}
	return st, nil
}

// checkFiles checks the files staged beside the new executable in dir:
// exactly the ones its version resource declares (info.files, compared
// without case), each DLL for the executable's machine and, for a signed
// app (pub), signed by the same publisher as the app.
func checkFiles(info exeInfo, dir string, staged []string, pub *publisher) error {
	want := make(map[string]bool, len(info.files))
	for _, n := range info.files {
		want[strings.ToLower(n)] = true
	}
	got := make(map[string]bool, len(staged))
	for _, n := range staged {
		if !want[strings.ToLower(n)] {
			return fmt.Errorf("%w: the update holds %s, which the new version does not declare", ErrRejected, n)
		}
		got[strings.ToLower(n)] = true
	}
	for _, n := range info.files {
		if !got[strings.ToLower(n)] {
			return fmt.Errorf("%w: the new version needs %s beside it, and the update does not hold it", ErrRejected, n)
		}
	}
	for _, n := range staged {
		if !strings.HasSuffix(strings.ToLower(n), ".dll") {
			continue // a licence: text, never loaded
		}
		p := filepath.Join(dir, n)
		m, err := machineOf(p)
		if err != nil {
			return fmt.Errorf("%w: %s: %v", ErrRejected, n, err)
		}
		if m != info.machine {
			return fmt.Errorf("%w: %s is for machine type %#x, and the new version %#x", ErrRejected, n, m, info.machine)
		}
		if pub != nil {
			sig, err := signerOf(p)
			if err != nil {
				return fmt.Errorf("%w: the signature of %s: %v", ErrRejected, n, err)
			}
			if !sig.same(pub) {
				return fmt.Errorf("%w: %s is signed by %q (root %q), not by this app's publisher %q (root %q)",
					ErrRejected, n, sig.subject, sig.root, pub.subject, pub.root)
			}
		}
	}
	return nil
}

// machineOf is the machine type of the PE file at path.
func machineOf(path string) (uint16, error) {
	f, err := os.Open(path)
	if err != nil {
		return 0, err
	}
	defer f.Close()
	st, err := f.Stat()
	if err != nil {
		return 0, err
	}
	pf, err := winres.Read(f, st.Size())
	if err != nil {
		return 0, err
	}
	return pf.Machine, nil
}

// verifyStaged checks that the executable at path is a build of the same
// app, for this machine, newer (unless anyVersion), and signed by pub (or,
// with pub nil, whatever its signature).
func verifyStaged(s self, path string, pub *publisher, anyVersion bool) (exeInfo, error) {
	info, err := readExePath(path)
	if err != nil {
		return info, fmt.Errorf("%w: %v", ErrRejected, err)
	}
	cur, err := readExePath(s.exe)
	if err != nil {
		return info, err
	}
	if info.machine != cur.machine {
		return info, fmt.Errorf("%w: the update is for machine type %#x, and this app %#x", ErrRejected, info.machine, cur.machine)
	}
	if info.id != s.id {
		return info, fmt.Errorf("%w: the update is another app (AppUserModelID %q, not %q)", ErrRejected, info.id, s.id)
	}
	if !anyVersion {
		if err := checkNewer(info.version, info.build, s.version, s.build); err != nil {
			return info, err
		}
	}
	if pub != nil {
		got, err := signerOf(path)
		if err != nil {
			return info, fmt.Errorf("%w: the update's signature: %v", ErrRejected, err)
		}
		if !got.same(pub) {
			return info, fmt.Errorf("%w: the update is signed by %q (root %q), not by this app's publisher %q (root %q)",
				ErrRejected, got.subject, got.root, pub.subject, pub.root)
		}
	}
	return info, nil
}

// stageUpdate copies the executable (or the one .exe in the zip at archive)
// to dir\new\<name>, and the zip's other files (DLLs and .txt licences, all
// at its top) beside it, hashing the bytes it reads from archive; it returns
// the staged executable and the names of the files staged beside it. The
// copies carry no Mark of the Web (they are new files; their streams are not
// copied), so SmartScreen does not prompt when the update starts.
func stageUpdate(archive, dir, name string) (string, []string, [32]byte, error) {
	var sum [32]byte
	in, err := os.Open(archive)
	if err != nil {
		return "", nil, sum, err
	}
	defer in.Close()
	fi, err := in.Stat()
	if err != nil {
		return "", nil, sum, err
	}
	if !fi.Mode().IsRegular() || fi.Size() > maxExe {
		return "", nil, sum, fmt.Errorf("%w: %s is not a file of at most 1 GiB", ErrRejected, archive)
	}
	h := sha256.New()
	src := io.TeeReader(in, h)
	newDir := filepath.Join(dir, "new")
	if err := os.Mkdir(newDir, 0o700); err != nil {
		return "", nil, sum, err
	}
	out := filepath.Join(newDir, name)
	var head [4]byte
	if _, err := io.ReadFull(in, head[:]); err != nil {
		return "", nil, sum, fmt.Errorf("%w: %v", ErrRejected, err)
	}
	if _, err := in.Seek(0, io.SeekStart); err != nil {
		return "", nil, sum, err
	}
	if string(head[:]) != "PK\x03\x04" {
		if err := writeFrom(out, src, maxExe); err != nil {
			return "", nil, sum, err
		}
		copy(sum[:], h.Sum(nil))
		return out, nil, sum, nil
	}
	// A zip: copied into the folder with the digest, then read from there.
	zpath := filepath.Join(dir, "update.zip")
	if err := writeFrom(zpath, src, maxExe); err != nil {
		return "", nil, sum, err
	}
	copy(sum[:], h.Sum(nil))
	zr, err := zip.OpenReader(zpath)
	if err != nil {
		return "", nil, sum, fmt.Errorf("%w: %v", ErrRejected, err)
	}
	defer zr.Close()
	var exe *zip.File
	var others []*zip.File
	seen := map[string]bool{}
	var total uint64
	for _, f := range zr.File {
		if f.FileInfo().IsDir() {
			continue
		}
		n, k := f.Name, strings.ToLower(f.Name)
		total += f.UncompressedSize64
		switch {
		case strings.ContainsAny(n, `/\:`) || f.UncompressedSize64 > maxExe || total > maxExe || seen[k]:
			return "", nil, sum, fmt.Errorf("%w: the zip must hold one executable and the files beside it, each once, at its top (%q)", ErrRejected, n)
		case strings.HasSuffix(k, ".exe"):
			if exe != nil {
				return "", nil, sum, fmt.Errorf("%w: the zip must hold exactly one executable at its top (%q)", ErrRejected, n)
			}
			exe = f
		case winres.CheckFileName(n) == nil:
			others = append(others, f)
		default:
			return "", nil, sum, fmt.Errorf("%w: the zip holds %q: only one .exe and the .dll and .txt files beside it", ErrRejected, n)
		}
		seen[k] = true
	}
	if exe == nil {
		return "", nil, sum, fmt.Errorf("%w: the zip holds no executable", ErrRejected)
	}
	unzip := func(f *zip.File, dst string) error {
		rc, err := f.Open()
		if err != nil {
			return fmt.Errorf("%w: %v", ErrRejected, err)
		}
		defer rc.Close()
		if err := writeFrom(dst, rc, maxExe); err != nil {
			return fmt.Errorf("%w: %v", ErrRejected, err)
		}
		return nil
	}
	if err := unzip(exe, out); err != nil {
		return "", nil, sum, err
	}
	names := make([]string, len(others))
	for i, f := range others {
		if err := unzip(f, filepath.Join(newDir, f.Name)); err != nil {
			return "", nil, sum, err
		}
		names[i] = f.Name
	}
	return out, names, sum, nil
}

func writeFrom(path string, r io.Reader, limit int64) error {
	f, err := os.OpenFile(path, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0o700)
	if err != nil {
		return err
	}
	n, err := io.Copy(f, io.LimitReader(r, limit+1))
	if cerr := f.Close(); err == nil {
		err = cerr
	}
	if err == nil && n > limit {
		err = fmt.Errorf("%w: larger than %d bytes", ErrRejected, limit)
	}
	if err != nil {
		os.Remove(path)
	}
	return err
}

// ---- Staging folders ----

// stagingRoot is "selfupdate" in the app's cache folder.
func stagingRoot(id string) (string, error) {
	d, err := paths.Cache(id)
	if err != nil {
		return "", err
	}
	return filepath.Join(d, "selfupdate"), nil
}

// stagingPrefix names the hidden staging folders next to the executable,
// the fallback when the cache folder is on another volume.
func stagingPrefix(exe string) string {
	return "." + strings.TrimSuffix(filepath.Base(exe), filepath.Ext(exe)) + ".selfupdate-"
}

// makeStaging makes the folder an update is staged in, on the executable's
// volume so that the swap is renames, with its "lock" file open with no
// sharing: while a process holds it, nobody takes the folder for a stale one.
func makeStaging(s self) (string, syscall.Handle, error) {
	dir := ""
	if root, err := stagingRoot(s.id); err == nil && strings.EqualFold(filepath.VolumeName(root), filepath.VolumeName(s.exe)) {
		if os.MkdirAll(root, 0o700) == nil {
			if d, err := os.MkdirTemp(root, "update-*"); err == nil {
				dir = d
			}
		}
	}
	if dir == "" {
		d, err := os.MkdirTemp(filepath.Dir(s.exe), stagingPrefix(s.exe)+"*")
		if err != nil {
			return "", 0, err
		}
		dir = d
	}
	lock, err := openLock(filepath.Join(dir, "lock"), true)
	if err != nil {
		os.RemoveAll(dir)
		return "", 0, err
	}
	return dir, lock, nil
}

// openLock opens (or creates) the lock file with no sharing.
func openLock(path string, create bool) (syscall.Handle, error) {
	p, err := syscall.UTF16PtrFromString(path)
	if err != nil {
		return 0, err
	}
	disp := uint32(syscall.OPEN_EXISTING)
	if create {
		disp = syscall.CREATE_NEW
	}
	return syscall.CreateFile(p, syscall.GENERIC_READ|syscall.GENERIC_WRITE, 0, nil, disp,
		syscall.FILE_ATTRIBUTE_NORMAL|0x00200000, 0) // FILE_FLAG_OPEN_REPARSE_POINT: never through a link
}

// isStaging reports whether dir is a staging folder: an absolute, real
// folder (not a link) with the lock file.
func isStaging(dir string) bool {
	if dir == "" || !filepath.IsAbs(dir) || filepath.Clean(dir) != dir {
		return false
	}
	st, err := os.Lstat(dir)
	if err != nil || !st.IsDir() || st.Mode()&(os.ModeSymlink|os.ModeIrregular) != 0 {
		return false
	}
	lk, err := os.Lstat(filepath.Join(dir, "lock"))
	return err == nil && lk.Mode().IsRegular()
}

// stagingInUse reports whether a process holds the folder's lock.
func stagingInUse(dir string) bool {
	h, err := openLock(filepath.Join(dir, "lock"), false)
	if err != nil {
		return errors.Is(err, syscall.Errno(32)) // ERROR_SHARING_VIOLATION
	}
	syscall.CloseHandle(h)
	return false
}

// removeStaging removes a staging folder, and nothing that is not one.
func removeStaging(dir string) error {
	if !isStaging(dir) {
		return fmt.Errorf("selfupdate: %q is not a staging folder", dir)
	}
	return winproc.RemoveAll(dir)
}

// cleanStale removes staging folders nothing holds any more.
func cleanStale(s self, lg *slog.Logger) {
	var dirs []string
	if root, err := stagingRoot(s.id); err == nil {
		ents, _ := os.ReadDir(root)
		for _, e := range ents {
			if strings.HasPrefix(e.Name(), "update-") {
				dirs = append(dirs, filepath.Join(root, e.Name()))
			}
		}
	}
	parent, prefix := filepath.Dir(s.exe), stagingPrefix(s.exe)
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

// fileID is the volume serial number and file index of path itself: what
// stays the same across renames on a volume, as an inode does.
func fileID(path string) (uint32, uint64, error) {
	p, err := syscall.UTF16PtrFromString(path)
	if err != nil {
		return 0, 0, err
	}
	h, err := syscall.CreateFile(p, 0, syscall.FILE_SHARE_READ|syscall.FILE_SHARE_WRITE|syscall.FILE_SHARE_DELETE, nil,
		syscall.OPEN_EXISTING, syscall.FILE_FLAG_BACKUP_SEMANTICS|0x00200000, 0)
	if err != nil {
		return 0, 0, err
	}
	defer syscall.CloseHandle(h)
	var d syscall.ByHandleFileInformation
	if err := syscall.GetFileInformationByHandle(h, &d); err != nil {
		return 0, 0, err
	}
	return d.VolumeSerialNumber, uint64(d.FileIndexHigh)<<32 | uint64(d.FileIndexLow), nil
}

// inode is fileID with the volume as an int64, the shape the record has.
func inode(path string) (int64, uint64, error) {
	v, i, err := fileID(path)
	return int64(v), i, err
}

// ---- Handing over to the helper ----

// winStaged is what a Windows Staged holds besides the shared fields.
type winStaged struct {
	lock   syscall.Handle
	exeSum [32]byte   // the staged executable's SHA-256, which the helper checks again
	pub    *publisher // nil for an unsigned app
	files  []jobFile  // the files staged beside it, with their SHA-256
}

// jobFile is a file staged beside the new executable (in staging\new), with
// its SHA-256 in hex, which the helper checks again.
type jobFile struct {
	Name   string `json:"name"`
	Digest string `json:"digest"`
}

// job is what the app hands the helper, in the staging folder.
type job struct {
	Parent        uint64        `json:"parent"` // an inherited handle to the app's process
	Lock          uint64        `json:"lock"`   // an inherited handle to the staging folder's lock
	Bundle        string        `json:"bundle"` // the executable to replace
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
	AnyVersion    bool          `json:"anyVersion"`
	Digest        string        `json:"digest"` // hex SHA-256 of the staged executable, for an unsigned app
	Signed        bool          `json:"signed"`
	Files         []jobFile     `json:"files"` // the files staged beside the new executable
}

var pSetHandleInfo = syscall.NewLazyDLL("kernel32.dll").NewProc("SetHandleInformation")

func inheritable(h syscall.Handle) error {
	r, _, err := pSetHandleInfo.Call(uintptr(h), 1, 1) // HANDLE_FLAG_INHERIT
	if r == 0 {
		return err
	}
	return nil
}

// InstallAndRelaunch hands the staged update to the helper and returns; the
// app must then quit. The helper swaps the update in once the app has
// exited, starts it with args, and rolls back if it does not call Started
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
	if !strings.EqualFold(cur.exe, s.self.exe) {
		return fmt.Errorf("selfupdate: the app moved since the update was staged (%s)", cur.exe)
	}
	if _, err := supportedAt(cur, o); err != nil {
		return err
	}
	if fi, err := os.Lstat(s.app); err != nil || !fi.Mode().IsRegular() {
		return fmt.Errorf("selfupdate: the staged update is gone (%s)", s.app)
	}
	dev, ino, err := inode(cur.exe)
	if err != nil {
		return err
	}
	helperDir := filepath.Join(s.dir, "helper")
	if err := os.Mkdir(helperDir, 0o700); err != nil {
		return fmt.Errorf("selfupdate: cannot copy the helper: %w", err)
	}
	helper := filepath.Join(helperDir, filepath.Base(cur.exe))
	if err := copyFile(cur.exe, helper); err != nil {
		os.RemoveAll(helperDir)
		return fmt.Errorf("selfupdate: cannot copy the helper: %w", err)
	}
	parent, err := winproc.SelfHandle()
	if err != nil {
		os.RemoveAll(helperDir)
		return err
	}
	defer syscall.CloseHandle(parent)
	if err := inheritable(s.win.lock); err != nil {
		os.RemoveAll(helperDir)
		return err
	}
	nonce := make([]byte, 16)
	if _, err := rand.Read(nonce); err != nil {
		return err
	}
	j := job{
		Parent: uint64(parent), Lock: uint64(s.win.lock), Bundle: cur.exe, Dev: dev, Ino: ino,
		Staging: s.dir, Staged: s.app, ID: cur.id, From: displayVersion(cur.exe, cur.version), To: s.version,
		Args: append([]string(nil), args...), HealthTimeout: o.HealthTimeout, QuitTimeout: o.QuitTimeout,
		Nonce: hex.EncodeToString(nonce), AnyVersion: s.anyVer, Digest: hex.EncodeToString(s.win.exeSum[:]), Signed: s.win.pub != nil,
		Files: s.win.files,
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
	p, err := winproc.Start(helper, []string{helperFlag, jobPath}, s.dir, parent, s.win.lock)
	if err != nil {
		os.RemoveAll(helperDir)
		os.Remove(jobPath)
		return fmt.Errorf("selfupdate: cannot start the helper: %w", err)
	}
	pid := p.Pid
	p.Release()
	syscall.CloseHandle(s.win.lock) // the helper holds its own copy now
	s.win.lock, s.used = 0, true
	o.Logger.Info("selfupdate: the helper installs the update when the app quits", "helper", pid, "from", j.From, "to", s.version)
	return nil
}

// displayVersion is the executable's ProductVersion, or version.
func displayVersion(exe, version string) string {
	if info, err := readExePath(exe); err == nil && info.display != "" {
		return info.display
	}
	return version
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
	syscall.CloseHandle(s.win.lock)
	s.win.lock = 0
	return removeStaging(s.dir)
}

func copyFile(src, dst string) error {
	in, err := os.Open(src)
	if err != nil {
		return err
	}
	defer in.Close()
	return writeFrom(dst, in, maxExe)
}

// writeNew writes a file that must not exist yet.
func writeNew(path string, b []byte) error { return writeFrom(path, bytes.NewReader(b), 1<<20) }

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

// sha256File is the digest of the file at path.
func sha256File(path string) ([32]byte, error) {
	var sum [32]byte
	f, err := os.Open(path)
	if err != nil {
		return sum, err
	}
	defer f.Close()
	h := sha256.New()
	if _, err := io.Copy(h, f); err != nil {
		return sum, err
	}
	copy(sum[:], h.Sum(nil))
	return sum, nil
}
