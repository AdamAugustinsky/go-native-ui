//go:build windows

package selfupdate

import (
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
	"sync"
	"syscall"
	"time"

	applog "gonativeui/app/log"
	"gonativeui/app/paths"
	"gonativeui/internal/winproc"
	"gonativeui/internal/winres"
)

// The Windows helper follows the macOS one (helper_darwin.go): the same
// record, phases and health marker. The differences: the app passes the
// helper two inherited handles, its own process (to wait on) and the
// staging folder's lock; the swap is renames (the old executable and the
// files beside it that the update replaces or the old version declared to
// staging\old, then the new files and the new executable into place, all
// undone together on a failure or a rollback); the new version is started
// with CreateProcess and stopped, on a rollback, with TerminateProcess once
// its image is checked to be the installed executable.

// helperFlag is the private argument that makes the app's executable the
// helper: os.Args is exactly {exe, helperFlag, job file}.
const helperFlag = "-gnui-selfupdate-helper"

const (
	phaseInstalling = "installing"
	phaseVerifying  = "verifying"
	phaseRolledBack = "rolledback"
	phaseFailed     = "failed"
)

type record struct {
	Phase   string    `json:"phase"`
	Bundle  string    `json:"bundle"`
	Dev     int64     `json:"dev"`
	OldIno  uint64    `json:"oldIno"`
	NewIno  uint64    `json:"newIno"`
	From    string    `json:"from"`
	To      string    `json:"to"`
	Reason  string    `json:"reason,omitempty"`
	Nonce   string    `json:"nonce"`
	Staging string    `json:"staging"`
	Time    time.Time `json:"time"`
}

func stateDir(id string) (string, error) {
	d, err := paths.Data(id)
	if err != nil {
		return "", err
	}
	return filepath.Join(d, "selfupdate"), nil
}

func readRecord(dir string) (record, bool) {
	b, err := readSmall(filepath.Join(dir, "state.json"))
	if err != nil {
		return record{}, false
	}
	var r record
	if json.Unmarshal(b, &r) != nil {
		return record{}, false
	}
	return r, true
}

func writeRecord(dir string, r record) error {
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return err
	}
	r.Time = time.Now().UTC()
	b, err := json.Marshal(r)
	if err != nil {
		return err
	}
	return writeAtomic(filepath.Join(dir, "state.json"), b)
}

func markerPath(dir, nonce string) string { return filepath.Join(dir, "healthy-"+nonce) }

// readSmall reads a regular file of at most 1 MiB, not through a link.
func readSmall(path string) ([]byte, error) {
	if fi, err := os.Lstat(path); err != nil || !fi.Mode().IsRegular() || fi.Size() > 1<<20 {
		if err == nil {
			err = fmt.Errorf("%s is not a small file", path)
		}
		return nil, err
	}
	f, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	defer f.Close()
	return io.ReadAll(io.LimitReader(f, 1<<20))
}

// ---- The app's side: LastResult and Started ----

var last struct {
	once    sync.Once
	r       Result
	rec     *record
	dir     string
	mu      sync.Mutex
	started bool
}

func loadLast() {
	s, err := findSelf()
	if err != nil {
		return
	}
	dir, err := stateDir(s.id)
	if err != nil {
		return
	}
	last.dir = dir
	last.r, last.rec = resultFor(s, dir, options().Logger)
}

func resultFor(s self, dir string, lg *slog.Logger) (Result, *record) {
	rec, ok := readRecord(dir)
	if !ok || !strings.EqualFold(rec.Bundle, s.exe) {
		return Result{}, nil
	}
	res := Result{From: rec.From, To: rec.To, Reason: rec.Reason, Time: rec.Time}
	switch rec.Phase {
	case phaseRolledBack, phaseFailed:
		res.Status = RolledBack
		if rec.Phase == phaseFailed {
			res.Status = Failed
		}
		os.Remove(filepath.Join(dir, "state.json"))
		cleanLater(rec.Staging)
		return res, nil
	case phaseInstalling, phaseVerifying:
		dev, ino, err := inode(s.exe)
		if err != nil || dev != rec.Dev {
			return Result{}, nil
		}
		switch {
		case ino == rec.NewIno:
			res.Status = Updated
			return res, &rec
		case ino == rec.OldIno && !stagingInUse(rec.Staging):
			removeStaging(rec.Staging)
			os.Remove(filepath.Join(dir, "state.json"))
			lg.Warn("selfupdate: the last update was interrupted; this is the old version", "to", rec.To)
			res.Status, res.Reason = Failed, "the update was interrupted before it finished"
			return res, nil
		}
	}
	return Result{}, nil
}

// cleanLater removes the staging folder once its helper, which cannot
// delete its own executable, has exited (within a minute).
func cleanLater(dir string) {
	if !isStaging(dir) {
		return
	}
	go func() {
		for i := 0; i < 120; i++ {
			if !stagingInUse(dir) {
				removeStaging(dir)
				return
			}
			time.Sleep(500 * time.Millisecond)
		}
	}()
}

// LastResult describes the update that led to this run (see the package
// comment). It is read once per process.
func LastResult() Result {
	last.once.Do(loadLast)
	return last.r
}

// Started reports that this run started: the new version calls it after its
// first frame, and the helper waiting for it keeps the update.
func Started() {
	last.once.Do(loadLast)
	last.mu.Lock()
	defer last.mu.Unlock()
	if last.started || last.rec == nil {
		return
	}
	last.started = true
	rec, lg := last.rec, options().Logger
	if !stagingInUse(rec.Staging) {
		if cur, ok := readRecord(last.dir); ok && cur.Nonce == rec.Nonce {
			removeStaging(rec.Staging)
			os.Remove(filepath.Join(last.dir, "state.json"))
		}
		lg.Info("selfupdate: started; the helper had gone, so the update was finished here", "version", rec.To)
		return
	}
	if err := writeAtomic(markerPath(last.dir, rec.Nonce), []byte(strconv.Itoa(os.Getpid()))); err != nil {
		lg.Warn("selfupdate: cannot report that the update started", "err", err)
		return
	}
	lg.Info("selfupdate: reported that the new version started", "version", rec.To)
	cleanLater(rec.Staging)
}

// ---- The helper ----

// RunHelperIfAsked runs the helper and exits when this process was started
// as one (by InstallAndRelaunch); otherwise it returns at once. Call it first
// thing in main, after Configure.
func RunHelperIfAsked() {
	if len(os.Args) != 3 || os.Args[1] != helperFlag {
		return
	}
	os.Exit(runHelper(os.Args[2]))
}

func runHelper(jobPath string) int {
	j, err := loadJob(jobPath)
	if err != nil {
		fmt.Fprintln(os.Stderr, "selfupdate helper:", err)
		return 2
	}
	o := options()
	lg, err := applog.Setup(applog.Options{ID: j.ID, Name: "selfupdate", NoStderr: true, NoCrashCapture: true})
	if lg == nil {
		lg = slog.New(slog.NewTextHandler(os.Stderr, nil))
	}
	if err != nil {
		lg.Warn("selfupdate helper: log", "err", err)
	}
	defer applog.Close()
	dir, err := stateDir(j.ID)
	if err != nil {
		lg.Error("selfupdate helper: no state folder", "err", err)
		return 1
	}
	launch := o.Launcher
	if launch == nil {
		launch = func(exe string, args []string) (int, error) {
			p, err := winproc.Start(exe, args, filepath.Dir(exe))
			if err != nil {
				return 0, err
			}
			pid := p.Pid
			p.Release()
			return pid, nil
		}
	}
	h := &helper{j: j, o: o, lg: lg, dir: dir, launch: launch}
	h.run()
	return 0
}

// loadJob reads the job and checks that it describes this helper.
func loadJob(jobPath string) (job, error) {
	var j job
	b, err := readSmall(jobPath)
	if err != nil {
		return j, err
	}
	if err := json.Unmarshal(b, &j); err != nil {
		return j, err
	}
	exe, err := os.Executable()
	if err != nil {
		return j, err
	}
	exe = filepath.Clean(exe)
	eq := strings.EqualFold
	switch {
	case !eq(filepath.Dir(filepath.Dir(exe)), j.Staging) || !eq(filepath.Base(filepath.Dir(exe)), "helper") ||
		!eq(filepath.Dir(filepath.Clean(jobPath)), j.Staging) || !isStaging(j.Staging):
		return j, errors.New("the job is not in this helper's staging folder")
	case !eq(filepath.Dir(filepath.Dir(j.Staged)), j.Staging) || !eq(filepath.Base(filepath.Dir(j.Staged)), "new"):
		return j, errors.New("the staged executable is not in the staging folder")
	case !filepath.IsAbs(j.Bundle) || filepath.Clean(j.Bundle) != j.Bundle || !strings.HasSuffix(strings.ToLower(j.Bundle), ".exe"):
		return j, errors.New("the executable path is not usable")
	case j.Parent == 0 || j.Lock == 0 || j.ID == "" || j.Nonce == "" || strings.ContainsAny(j.Nonce, `/\.`):
		return j, errors.New("the job is incomplete")
	case !j.Signed && len(j.Digest) != 64:
		return j, errors.New("the job has no digest for an unsigned app")
	}
	for _, f := range j.Files {
		if winres.CheckFileName(f.Name) != nil || len(f.Digest) != 64 {
			return j, fmt.Errorf("the job names an unusable file %q", f.Name)
		}
	}
	j.HealthTimeout = clamp(j.HealthTimeout, time.Second, 10*time.Minute, 30*time.Second)
	j.QuitTimeout = clamp(j.QuitTimeout, time.Second, 10*time.Minute, 60*time.Second)
	return j, nil
}

func clamp(d, lo, hi, def time.Duration) time.Duration {
	switch {
	case d <= 0:
		return def
	case d < lo:
		return lo
	case d > hi:
		return hi
	}
	return d
}

type helper struct {
	j      job
	o      Options
	lg     *slog.Logger
	dir    string
	launch func(string, []string) (int, error)
	rec    record
	sw     swap // the renames that installed the update
}

// swap is a sequence of renames that can be undone together.
type swap struct{ done [][2]string }

// move renames from to to and remembers it.
func (s *swap) move(from, to string) error {
	if err := winproc.Rename(from, to); err != nil {
		return err
	}
	s.done = append(s.done, [2]string{from, to})
	return nil
}

// undo renames everything back, the last first; it stops at the first
// rename that fails.
func (s *swap) undo() error {
	for i := len(s.done) - 1; i >= 0; i-- {
		m := s.done[i]
		if err := winproc.Rename(m[1], m[0]); err != nil {
			s.done = s.done[:i+1]
			return err
		}
	}
	s.done = nil
	return nil
}

// takeLock adopts the inherited handle to the staging folder's lock, after
// checking that it is that file.
func takeLock(j job) (syscall.Handle, error) {
	h := syscall.Handle(j.Lock)
	var d syscall.ByHandleFileInformation
	if err := syscall.GetFileInformationByHandle(h, &d); err != nil {
		return 0, fmt.Errorf("no lock from the app: %w", err)
	}
	v, i, err := fileID(filepath.Join(j.Staging, "lock"))
	if err != nil {
		return 0, err
	}
	if d.VolumeSerialNumber != v || uint64(d.FileIndexHigh)<<32|uint64(d.FileIndexLow) != i {
		return 0, errors.New("the handle is not the staging folder's lock")
	}
	return h, nil
}

func (h *helper) run() {
	j := h.j
	lock, err := takeLock(j)
	if err != nil {
		h.lg.Error("selfupdate helper: cannot take the update", "err", err)
		return
	}
	defer syscall.CloseHandle(lock)
	h.rec = record{Bundle: j.Bundle, Dev: j.Dev, OldIno: j.Ino, From: j.From, To: j.To, Nonce: j.Nonce, Staging: j.Staging}

	h.lg.Info("selfupdate helper: waiting for the app to quit", "from", j.From, "to", j.To)
	parent := syscall.Handle(j.Parent)
	if !winproc.Wait(parent, j.QuitTimeout) {
		h.fail(fmt.Sprintf("the app did not quit within %v", j.QuitTimeout), false)
		return
	}
	syscall.CloseHandle(parent)
	h.lg.Info("selfupdate helper: the app quit")

	if dev, ino, err := inode(j.Bundle); err != nil || dev != j.Dev || ino != j.Ino {
		h.fail("the app was moved or replaced before the update could be installed", false)
		return
	}
	cur, err := selfAt(j.Bundle)
	if err != nil || cur.id != j.ID {
		h.fail("the executable to replace is not the app that staged the update", false)
		return
	}
	var pub *publisher
	if j.Signed {
		if pub, err = signerOf(j.Bundle); err != nil {
			h.fail("the running app's signature no longer verifies: "+err.Error(), true)
			return
		}
	} else {
		sum, err := sha256File(j.Staged)
		if err != nil || hex.EncodeToString(sum[:]) != j.Digest {
			h.fail("the staged update changed after it was verified", true)
			return
		}
	}
	newDir := filepath.Dir(j.Staged)
	names := make([]string, len(j.Files))
	for i, f := range j.Files {
		names[i] = f.Name
		sum, err := sha256File(filepath.Join(newDir, f.Name))
		if err != nil || hex.EncodeToString(sum[:]) != f.Digest {
			h.fail("the staged "+f.Name+" changed after it was verified", true)
			return
		}
	}
	info, err := verifyStaged(cur, j.Staged, pub, j.AnyVersion)
	if err == nil {
		err = checkFiles(info, newDir, names, pub)
	}
	if err != nil {
		h.fail("the staged update no longer verifies: "+err.Error(), true)
		return
	}
	old, err := readExePath(j.Bundle)
	if err != nil {
		h.fail("the executable to replace: "+err.Error(), true)
		return
	}
	_, newIno, err := inode(j.Staged)
	if err != nil {
		h.fail(err.Error(), true)
		return
	}
	h.rec.NewIno = newIno
	h.rec.Phase = phaseInstalling
	if err := writeRecord(h.dir, h.rec); err != nil {
		h.fail("cannot write the update record: "+err.Error(), true)
		return
	}
	h.lg.Info("selfupdate helper: verified the update again")

	backup := filepath.Join(j.Staging, "old")
	if err := h.install(old.files, names, backup); err != nil {
		if uerr := h.sw.undo(); uerr != nil {
			h.critical(fmt.Sprintf("the update could not be moved into place (%v), and the old version could not be put back (%v); it is in %s", err, uerr, backup))
			return
		}
		h.fail("the update could not be moved into place: "+err.Error(), true)
		return
	}
	h.lg.Info("selfupdate helper: swapped the new version into place", "backup", backup, "files", len(names))

	h.rec.Phase = phaseVerifying
	if err := writeRecord(h.dir, h.rec); err != nil {
		h.rollback(backup, "cannot write the update record: "+err.Error(), 0)
		return
	}
	deadline := time.Now().Add(j.HealthTimeout)
	pid, err := h.launch(j.Bundle, j.Args)
	if err != nil {
		h.rollback(backup, "the new version could not be started: "+err.Error(), 0)
		return
	}
	h.lg.Info("selfupdate helper: started the new version", "pid", pid)
	if why := h.waitHealthy(pid, deadline); why != "" {
		h.rollback(backup, why, pid)
		return
	}
	os.Remove(markerPath(h.dir, j.Nonce))
	os.Remove(filepath.Join(h.dir, "state.json"))
	h.tidy()
	h.lg.Info("selfupdate helper: the new version started; removed the old one", "version", j.To)
}

// install moves the old version into backup (the executable, then the files
// beside it that the old version declares or the update brings), and the
// new files and then the new executable into place, through h.sw.
func (h *helper) install(oldFiles, newFiles []string, backup string) error {
	j := h.j
	if err := os.Mkdir(backup, 0o700); err != nil {
		return err
	}
	appDir, newDir := filepath.Dir(j.Bundle), filepath.Dir(j.Staged)
	if err := h.sw.move(j.Bundle, filepath.Join(backup, filepath.Base(j.Bundle))); err != nil {
		return fmt.Errorf("the old version could not be moved aside: %w", err)
	}
	seen := map[string]bool{}
	for _, n := range append(append([]string(nil), oldFiles...), newFiles...) {
		k := strings.ToLower(n)
		if seen[k] {
			continue
		}
		seen[k] = true
		p := filepath.Join(appDir, n)
		if _, err := os.Lstat(p); err != nil {
			continue // not there: nothing to keep
		}
		if err := h.sw.move(p, filepath.Join(backup, n)); err != nil {
			return fmt.Errorf("%s could not be moved aside: %w", n, err)
		}
	}
	for _, n := range newFiles {
		if err := h.sw.move(filepath.Join(newDir, n), filepath.Join(appDir, n)); err != nil {
			return fmt.Errorf("%s could not be moved into place: %w", n, err)
		}
	}
	return h.sw.move(j.Staged, j.Bundle)
}

// waitHealthy waits until deadline for the marker Started writes, and says
// why the new version is not healthy, or "".
func (h *helper) waitHealthy(pid int, deadline time.Time) string {
	marker := markerPath(h.dir, h.j.Nonce)
	healthy := func() bool {
		b, err := readSmall(marker)
		if err != nil {
			return false
		}
		p, err := strconv.Atoi(strings.TrimSpace(string(b)))
		return err == nil && (pid <= 0 || p == pid)
	}
	var proc syscall.Handle
	if pid > 0 {
		proc, _ = winproc.Open(pid)
		if proc != 0 {
			defer syscall.CloseHandle(proc)
		}
	}
	for {
		if healthy() {
			return ""
		}
		if proc != 0 && !winproc.Running(proc) {
			if healthy() {
				return ""
			}
			return "the new version quit before it reported that it started"
		}
		if time.Now().After(deadline) {
			return fmt.Sprintf("the new version did not report that it started within %v", h.j.HealthTimeout)
		}
		time.Sleep(100 * time.Millisecond)
	}
}

// stop ends the new version's process, when it still runs the installed
// executable.
func (h *helper) stop(pid int) {
	p, err := winproc.Open(pid)
	if err != nil {
		return
	}
	defer syscall.CloseHandle(p)
	if !winproc.Running(p) || !sameFile(winproc.Image(p), h.j.Bundle) {
		return
	}
	h.lg.Info("selfupdate helper: stopping the new version", "pid", pid)
	winproc.Terminate(p, 1)
	winproc.Wait(p, 5*time.Second)
}

// rollback stops the new version, puts the old one back (the executable
// and the files beside it, by undoing the swap) and starts it.
func (h *helper) rollback(backup, why string, pid int) {
	j := h.j
	h.lg.Warn("selfupdate helper: rolling back", "reason", why)
	if pid > 0 {
		h.stop(pid)
	}
	if dev, ino, err := inode(j.Bundle); err != nil || dev != j.Dev || ino != h.rec.NewIno {
		h.critical(fmt.Sprintf("%s, and the app was replaced while it ran; the old version is in %s", why, backup))
		return
	}
	if err := h.sw.undo(); err != nil {
		h.critical(fmt.Sprintf("%s, and the old version could not be put back (%v); it is in %s", why, err, backup))
		return
	}
	h.lg.Info("selfupdate helper: put the old version back")
	h.rec.Phase, h.rec.Reason = phaseRolledBack, why
	if err := writeRecord(h.dir, h.rec); err != nil {
		h.lg.Warn("selfupdate helper: cannot write the update record", "err", err)
	}
	os.Remove(markerPath(h.dir, j.Nonce))
	if pid, err := h.launch(j.Bundle, j.Args); err != nil {
		h.lg.Error("selfupdate helper: cannot start the old version", "err", err)
	} else {
		h.lg.Info("selfupdate helper: started the old version", "pid", pid)
	}
	h.tidy()
}

// fail records that the update did not happen and, when the app has quit,
// starts it again.
func (h *helper) fail(why string, relaunch bool) {
	h.lg.Error("selfupdate helper: the update failed; nothing was changed", "reason", why)
	h.rec.Phase, h.rec.Reason = phaseFailed, why
	if err := writeRecord(h.dir, h.rec); err != nil {
		h.lg.Warn("selfupdate helper: cannot write the update record", "err", err)
	}
	if relaunch {
		if pid, err := h.launch(h.j.Bundle, h.j.Args); err != nil {
			h.lg.Error("selfupdate helper: cannot start the app again", "err", err)
		} else {
			h.lg.Info("selfupdate helper: started the app again", "pid", pid)
		}
	}
	h.tidy()
}

// critical records a failure that left the app in an unknown state; the
// staging folder, which holds the other version, is kept.
func (h *helper) critical(why string) {
	h.lg.Error("selfupdate helper: the update failed and could not be undone", "reason", why)
	h.rec.Phase, h.rec.Reason = phaseFailed, why
	if err := writeRecord(h.dir, h.rec); err != nil {
		h.lg.Warn("selfupdate helper: cannot write the update record", "err", err)
	}
}

// tidy removes what the staging folder holds besides the helper's own
// executable and the lock, which the app removes later (cleanLater,
// cleanStale): a running image cannot be deleted.
func (h *helper) tidy() {
	ents, err := os.ReadDir(h.j.Staging)
	if err != nil {
		return
	}
	for _, e := range ents {
		switch e.Name() {
		case "helper", "lock":
			continue
		}
		if err := winproc.RemoveAll(filepath.Join(h.j.Staging, e.Name())); err != nil {
			h.lg.Warn("selfupdate helper: could not remove", "path", e.Name(), "err", err)
		}
	}
}

// sameFile reports whether the paths a and b name the same file (8.3 names
// and case aside).
func sameFile(a, b string) bool {
	if a == "" || b == "" {
		return false
	}
	va, ia, err1 := fileID(a)
	vb, ib, err2 := fileID(b)
	return err1 == nil && err2 == nil && va == vb && ia == ib
}
