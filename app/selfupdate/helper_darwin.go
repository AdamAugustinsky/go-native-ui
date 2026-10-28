//go:build darwin

package selfupdate

import (
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
)

// helperFlag is the private argument that makes the app's executable the
// helper: os.Args is exactly {exe, helperFlag, job file}.
const helperFlag = "-gnui-selfupdate-helper"

// The phases of the record (state.json in the state folder).
const (
	phaseInstalling = "installing" // the helper is about to swap
	phaseVerifying  = "verifying"  // swapped; waiting for the new version's Started
	phaseRolledBack = "rolledback" // the old version is back
	phaseFailed     = "failed"     // nothing changed, or the rollback could not finish
)

// record is what the helper leaves for the app.
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

// stateDir is where the record and the health marker live: "selfupdate" in
// the app's data folder.
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
	f, err := os.OpenFile(path, os.O_RDONLY|syscall.O_NOFOLLOW, 0)
	if err != nil {
		return nil, err
	}
	defer f.Close()
	fi, err := f.Stat()
	if err != nil {
		return nil, err
	}
	if !fi.Mode().IsRegular() || fi.Size() > 1<<20 {
		return nil, fmt.Errorf("%s is not a small file", path)
	}
	return io.ReadAll(io.LimitReader(f, 1<<20))
}

// ---- The app's side: LastResult and Started ----

var last struct {
	once    sync.Once
	r       Result
	rec     *record // the record of the update this run completes, if it is the new version
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

// resultFor reads the record for the running bundle s. A finished record
// (rolled back, failed) is consumed. An unfinished one whose helper is gone
// is finished here: by Started when this run is the new version, and here,
// as Failed, when it is the old one.
func resultFor(s self, dir string, lg *slog.Logger) (Result, *record) {
	rec, ok := readRecord(dir)
	if !ok || rec.Bundle != s.bundle {
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
		return res, nil
	case phaseInstalling, phaseVerifying:
		dev, ino, err := inode(s.bundle)
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

// LastResult describes the update that led to this run (see the package
// comment). It is read once per process, by the first call to LastResult or
// Started; later calls return the same.
func LastResult() Result {
	last.once.Do(loadLast)
	return last.r
}

// Started reports that this run started: the new version calls it after its
// first frame, and the helper waiting for it keeps the update. When no update
// waits for it, it does nothing. Calls after the first do nothing.
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
		// The helper is gone (killed, or the Mac restarted): nobody can roll
		// back any more, so keep the update and clean up after it.
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
		launch = launchServices
	}
	h := &helper{j: j, o: o, lg: lg, dir: dir, launch: launch}
	h.run()
	return 0
}

// loadJob reads the job and checks that it describes this helper: the job
// file is in the folder the helper runs from, which is a staging folder, and
// the staged bundle is in it.
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
	if exe, err = filepath.EvalSymlinks(exe); err != nil {
		return j, err
	}
	jdir, err := filepath.EvalSymlinks(filepath.Dir(jobPath))
	if err != nil {
		return j, err
	}
	// exe is staging/helper/X.app/Contents/MacOS/x: the copy of the bundle
	// InstallAndRelaunch made.
	helperApp := filepath.Dir(filepath.Dir(filepath.Dir(exe)))
	switch {
	case filepath.Dir(filepath.Dir(helperApp)) != j.Staging || filepath.Base(filepath.Dir(helperApp)) != "helper" ||
		!strings.HasSuffix(helperApp, ".app") || filepath.Base(filepath.Dir(exe)) != "MacOS" ||
		jdir != j.Staging || !isStaging(j.Staging):
		return j, errors.New("the job is not in this helper's staging folder")
	case filepath.Dir(filepath.Dir(j.Staged)) != j.Staging || filepath.Base(filepath.Dir(j.Staged)) != "new" ||
		!strings.HasSuffix(j.Staged, ".app"):
		return j, errors.New("the staged bundle is not in the staging folder")
	case !filepath.IsAbs(j.Bundle) || filepath.Clean(j.Bundle) != j.Bundle || !strings.HasSuffix(j.Bundle, ".app") ||
		strings.HasPrefix(j.Bundle, j.Staging+"/") || strings.HasPrefix(j.Staging, j.Bundle+"/"):
		return j, errors.New("the bundle path is not usable")
	case j.Parent <= 1 || j.ID == "" || j.Nonce == "" || strings.ContainsAny(j.Nonce, `/\.`):
		return j, errors.New("the job is incomplete")
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
	dir    string // the state folder
	launch func(string, []string) (int, error)
	rec    record
	exe    string // the bundle's executable, once the staged copy is verified
}

// takeLock adopts the staging folder's lock, which the app passed as fd 3
// with its flock held: it must be that folder's lock file, and no process the
// helper starts may inherit it.
func takeLock(staging string) (*os.File, error) {
	var fd, lk syscall.Stat_t
	if err := syscall.Fstat(3, &fd); err != nil {
		return nil, fmt.Errorf("no lock from the app: %w", err)
	}
	if err := syscall.Lstat(filepath.Join(staging, "lock"), &lk); err != nil {
		return nil, err
	}
	if fd.Dev != lk.Dev || fd.Ino != lk.Ino {
		return nil, errors.New("fd 3 is not the staging folder's lock")
	}
	syscall.CloseOnExec(3)
	// Locking again through the same open file is a no-op for its holder and
	// fails for anyone else.
	if err := syscall.Flock(3, syscall.LOCK_EX|syscall.LOCK_NB); err != nil {
		return nil, fmt.Errorf("the staging folder is locked by another process: %w", err)
	}
	return os.NewFile(3, "lock"), nil
}

// signalReady tells InstallAndRelaunch that the helper took the job: one
// byte on readyFD, which it then closes, so that no process it starts
// inherits it. A descriptor that is not a pipe is left alone.
func signalReady() {
	var st syscall.Stat_t
	if syscall.Fstat(readyFD, &st) != nil || st.Mode&syscall.S_IFMT != syscall.S_IFIFO {
		return
	}
	syscall.Write(readyFD, []byte{1})
	syscall.Close(readyFD)
}

func (h *helper) run() {
	j := h.j
	lock, err := takeLock(j.Staging)
	if err != nil {
		h.lg.Error("selfupdate helper: cannot take the update", "err", err)
		return
	}
	defer lock.Close()
	signalReady()
	h.rec = record{Bundle: j.Bundle, Dev: j.Dev, OldIno: j.Ino, From: j.From, To: j.To, Nonce: j.Nonce, Staging: j.Staging}

	h.lg.Info("selfupdate helper: waiting for the app to quit", "pid", j.Parent, "from", j.From, "to", j.To)
	if !waitExit(j.Parent, j.QuitTimeout) {
		h.fail(fmt.Sprintf("the app did not quit within %v", j.QuitTimeout), false)
		return
	}
	h.lg.Info("selfupdate helper: the app quit")

	if dev, ino, err := inode(j.Bundle); err != nil || dev != j.Dev || ino != j.Ino {
		h.fail("the app was moved or replaced before the update could be installed", false)
		return
	}
	cur, err := readInfo(j.Bundle)
	if err != nil || cur.id != j.ID {
		h.fail("the bundle to replace is not the app that staged the update", false)
		return
	}
	req, err := requirementFor(h.o)
	var info bundleInfo
	if err == nil {
		info, err = verifyStaged(j.ID, j.Staged, req)
	}
	if err == nil && (info.exe == "" || info.exe == "." || info.exe == ".." || strings.ContainsRune(info.exe, '/')) {
		err = fmt.Errorf("%w: CFBundleExecutable %q", ErrRejected, info.exe)
	}
	if err == nil && !j.AnyVersion {
		err = checkNewer(info.version, info.build, cur.version, cur.build)
	}
	if err != nil {
		h.fail("the staged update no longer verifies: "+err.Error(), true)
		return
	}
	h.exe = filepath.Join(j.Bundle, "Contents", "MacOS", info.exe)
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

	backup, err := swapIn(j.Bundle, j.Staged, j.Staging)
	if err != nil {
		if errors.Is(err, errOldMissing) {
			h.critical(err.Error())
			return
		}
		h.fail("the update could not be moved into place: "+err.Error(), true)
		return
	}
	h.lg.Info("selfupdate helper: swapped the new version into place", "backup", backup)

	h.rec.Phase = phaseVerifying
	if err := writeRecord(h.dir, h.rec); err != nil {
		h.rollback(backup, "cannot write the update record: "+err.Error(), 0)
		return
	}
	deadline := time.Now().Add(j.HealthTimeout)
	pid, err := h.launch(j.Bundle, j.Args)
	if err != nil {
		h.rollback(backup, "the new version could not be launched: "+err.Error(), 0)
		return
	}
	h.lg.Info("selfupdate helper: launched the new version", "pid", pid)

	pid, why := h.waitHealthy(pid, deadline)
	if why != "" {
		h.rollback(backup, why, pid)
		return
	}
	os.Remove(markerPath(h.dir, j.Nonce))
	os.Remove(filepath.Join(h.dir, "state.json"))
	if err := removeStaging(j.Staging); err != nil {
		h.lg.Warn("selfupdate helper: the old version could not be removed", "err", err)
	}
	h.lg.Info("selfupdate helper: the new version started; removed the old one", "version", j.To)
}

// waitExit waits for process pid, the helper's parent, to end. Being
// reparented is the exact sign; a helper that is not its child waits for the
// pid to go away.
func waitExit(pid int, timeout time.Duration) bool {
	deadline := time.Now().Add(timeout)
	child := os.Getppid() == pid
	for {
		if child && os.Getppid() != pid {
			return true
		}
		if !child && !alive(pid) {
			return true
		}
		if time.Now().After(deadline) {
			return false
		}
		time.Sleep(50 * time.Millisecond)
	}
}

func alive(pid int) bool {
	err := syscall.Kill(pid, 0)
	return err == nil || errors.Is(err, syscall.EPERM)
}

// waitHealthy waits until deadline for the marker that Started writes. It
// returns the new version's pid (found by its executable when the launcher
// could not say) and why it is not healthy, or "".
func (h *helper) waitHealthy(pid int, deadline time.Time) (int, string) {
	marker := markerPath(h.dir, h.j.Nonce)
	healthy := func() bool {
		b, err := readSmall(marker)
		if err != nil {
			return false
		}
		p, err := strconv.Atoi(strings.TrimSpace(string(b)))
		return err == nil && (pid <= 0 || p == pid)
	}
	for i := 0; ; i++ {
		if healthy() {
			return pid, ""
		}
		if pid <= 0 && i%5 == 0 {
			if ps := processesOf(h.exe); len(ps) == 1 {
				pid = ps[0]
				h.lg.Info("selfupdate helper: found the new version's process", "pid", pid)
			}
		}
		if pid > 0 && !alive(pid) {
			if healthy() { // it reported, then quit
				return pid, ""
			}
			return pid, "the new version quit before it reported that it started"
		}
		if time.Now().After(deadline) {
			return pid, fmt.Sprintf("the new version did not report that it started within %v", h.j.HealthTimeout)
		}
		time.Sleep(100 * time.Millisecond)
	}
}

// rollback stops the new version, puts the old one back and launches it.
func (h *helper) rollback(backup, why string, pid int) {
	j := h.j
	h.lg.Warn("selfupdate helper: rolling back", "reason", why)
	if pid > 0 {
		h.stop(pid)
	}
	if h.exe != "" {
		for _, p := range processesOf(h.exe) {
			h.stop(p)
		}
	}
	if dev, ino, err := inode(j.Bundle); err != nil || dev != j.Dev || ino != h.rec.NewIno {
		h.critical(fmt.Sprintf("%s, and the app was replaced while it ran; the old version is at %s", why, backup))
		return
	}
	if err := swapBack(j.Bundle, backup, j.Staging); err != nil {
		h.critical(fmt.Sprintf("%s, and the old version could not be put back (%v); it is at %s", why, err, backup))
		return
	}
	h.lg.Info("selfupdate helper: put the old version back")
	h.rec.Phase, h.rec.Reason = phaseRolledBack, why
	if err := writeRecord(h.dir, h.rec); err != nil {
		h.lg.Warn("selfupdate helper: cannot write the update record", "err", err)
	}
	os.Remove(markerPath(h.dir, j.Nonce))
	if pid, err := h.launch(j.Bundle, j.Args); err != nil {
		h.lg.Error("selfupdate helper: cannot launch the old version", "err", err)
	} else {
		h.lg.Info("selfupdate helper: launched the old version", "pid", pid)
	}
	if err := removeStaging(j.Staging); err != nil {
		h.lg.Warn("selfupdate helper: the new version could not be removed", "err", err)
	}
}

// stop ends a process of the new version: SIGTERM, then SIGKILL after 5 s.
// It signals the pid only while that process runs the bundle's executable.
func (h *helper) stop(pid int) {
	ours := func() bool { return h.exe != "" && processPath(pid) == h.exe }
	for _, step := range []struct {
		sig  syscall.Signal
		wait time.Duration
	}{{syscall.SIGTERM, 5 * time.Second}, {syscall.SIGKILL, 2 * time.Second}} {
		if !ours() {
			return
		}
		h.lg.Info("selfupdate helper: stopping the new version", "pid", pid, "signal", step.sig.String())
		syscall.Kill(pid, step.sig)
		for deadline := time.Now().Add(step.wait); time.Now().Before(deadline); time.Sleep(50 * time.Millisecond) {
			if !alive(pid) {
				return
			}
		}
	}
}

// fail records that the update did not happen and, when the app has quit,
// launches it again.
func (h *helper) fail(why string, relaunch bool) {
	h.lg.Error("selfupdate helper: the update failed; nothing was changed", "reason", why)
	h.rec.Phase, h.rec.Reason = phaseFailed, why
	if err := writeRecord(h.dir, h.rec); err != nil {
		h.lg.Warn("selfupdate helper: cannot write the update record", "err", err)
	}
	if relaunch {
		if pid, err := h.launch(h.j.Bundle, h.j.Args); err != nil {
			h.lg.Error("selfupdate helper: cannot launch the app again", "err", err)
		} else {
			h.lg.Info("selfupdate helper: launched the app again", "pid", pid)
		}
	}
	if err := removeStaging(h.j.Staging); err != nil {
		h.lg.Warn("selfupdate helper: the staged update could not be removed", "err", err)
	}
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

// errOldMissing: the two-rename swap moved the old bundle aside and could
// not put it back.
var errOldMissing = errors.New("the old version could not be put back")

// renameSwapFn is renameSwap; the fallback tests replace it.
var renameSwapFn = renameSwap

func swapUnsupported(err error) bool {
	return errors.Is(err, syscall.ENOTSUP) || errors.Is(err, syscall.EINVAL)
}

// swapIn puts staged at bundle and returns where the old bundle went:
// staged's path after an atomic swap, or staging/old.app after two renames.
func swapIn(bundle, staged, staging string) (string, error) {
	err := renameSwapFn(staged, bundle)
	if err == nil {
		return staged, nil
	}
	if !swapUnsupported(err) {
		return "", err
	}
	old := filepath.Join(staging, "old.app")
	if err := os.Rename(bundle, old); err != nil {
		return "", err
	}
	if err := os.Rename(staged, bundle); err != nil {
		if rerr := os.Rename(old, bundle); rerr != nil {
			return "", fmt.Errorf("%w: it is at %s (%v; %v)", errOldMissing, old, err, rerr)
		}
		return "", err
	}
	return old, nil
}

// swapBack puts backup at bundle again, the new version going to backup's
// path (atomic swap) or to staging/failed.app (two renames).
func swapBack(bundle, backup, staging string) error {
	err := renameSwapFn(backup, bundle)
	if err == nil {
		return nil
	}
	if !swapUnsupported(err) {
		return err
	}
	failed := filepath.Join(staging, "failed.app")
	if err := os.Rename(bundle, failed); err != nil {
		return err
	}
	if err := os.Rename(backup, bundle); err != nil {
		if rerr := os.Rename(failed, bundle); rerr != nil {
			return fmt.Errorf("%v; and the new version could not be put back either: %v", err, rerr)
		}
		return err
	}
	return nil
}
