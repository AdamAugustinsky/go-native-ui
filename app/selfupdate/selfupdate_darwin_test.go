//go:build darwin

package selfupdate

import (
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"syscall"
	"testing"
	"time"

	"gonativeui/internal/launchguard"
)

const (
	testID  = "dev.gonativeui.selfupdatetest"
	testReq = `identifier "dev.gonativeui.selfupdatetest"`
	testExe = "testapp.test" // a test binary's name: internal/launchguard
)

var quiet = slog.New(slog.NewTextHandler(io.Discard, nil))

func TestMain(m *testing.M) {
	if os.Getenv("SELFUPDATE_PRINT_DR") == "1" {
		r, err := selfRequirement()
		if err != nil {
			fmt.Fprintln(os.Stderr, err)
			os.Exit(1)
		}
		fmt.Print(r)
		os.Exit(0)
	}
	buildEnv = os.Environ() // before any test moves HOME, which the go command's caches follow
	code := m.Run()
	if testApp.dir != "" {
		os.RemoveAll(testApp.dir)
	}
	os.Exit(code)
}

var buildEnv []string

var testApp struct {
	once sync.Once
	dir  string
	path string
	err  error
}

// appBinary builds internal/testapp once per run.
func appBinary(t *testing.T) string {
	t.Helper()
	testApp.once.Do(func() {
		if testApp.dir, testApp.err = os.MkdirTemp("", "selfupdate-testapp-*"); testApp.err != nil {
			return
		}
		testApp.path = filepath.Join(testApp.dir, "testapp")
		cmd := exec.Command("go", "build", "-o", testApp.path, "./internal/testapp")
		cmd.Env = buildEnv
		out, err := cmd.CombinedOutput()
		if err != nil {
			testApp.err = fmt.Errorf("building the test app: %v\n%s", err, out)
		}
	})
	if testApp.err != nil {
		t.Fatal(testApp.err)
	}
	return testApp.path
}

func realTemp(t *testing.T) string {
	t.Helper()
	d, err := filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	return d
}

func run(t *testing.T, name string, args ...string) {
	t.Helper()
	if out, err := exec.Command(name, args...).CombinedOutput(); err != nil {
		t.Fatalf("%s %s: %v\n%s", name, strings.Join(args, " "), err, out)
	}
}

// makeBundle writes parent/SUTest.app around the test app, signed ad hoc
// when sign is set.
func makeBundle(t *testing.T, parent, id, version, behavior string, sign bool) string {
	t.Helper()
	return makeBundleOf(t, parent, bundleSpec{id: id, version: version, build: version, behavior: behavior, exe: testExe, sign: sign})
}

type bundleSpec struct {
	id, version, build, behavior, exe string
	sign                              bool
}

func makeBundleOf(t *testing.T, parent string, sp bundleSpec) string {
	t.Helper()
	id, version, behavior, sign := sp.id, sp.version, sp.behavior, sp.sign
	b := filepath.Join(parent, "SUTest.app")
	for _, d := range []string{"Contents/MacOS", "Contents/Resources"} {
		if err := os.MkdirAll(filepath.Join(b, d), 0o755); err != nil {
			t.Fatal(err)
		}
	}
	bin, err := os.ReadFile(appBinary(t))
	if err != nil {
		t.Fatal(err)
	}
	plist := `<?xml version="1.0" encoding="UTF-8"?>
<!DOCTYPE plist PUBLIC "-//Apple//DTD PLIST 1.0//EN" "http://www.apple.com/DTDs/PropertyList-1.0.dtd">
<plist version="1.0"><dict>
	<key>CFBundleExecutable</key><string>` + sp.exe + `</string>
	<key>CFBundleIdentifier</key><string>` + id + `</string>
	<key>CFBundleInfoDictionaryVersion</key><string>6.0</string>
	<key>CFBundleName</key><string>SUTest</string>
	<key>CFBundlePackageType</key><string>APPL</string>
	<key>CFBundleShortVersionString</key><string>` + version + `</string>
	<key>CFBundleVersion</key><string>` + sp.build + `</string>
</dict></plist>
`
	for name, data := range map[string]string{
		"Contents/MacOS/" + sp.exe:     string(bin),
		"Contents/Info.plist":          plist,
		"Contents/Resources/version":   version + "\n",
		"Contents/Resources/behavior":  behavior + "\n",
		"Contents/Resources/notes.txt": "release notes " + version + "\n",
	} {
		mode := os.FileMode(0o644)
		if strings.HasPrefix(name, "Contents/MacOS/") {
			mode = 0o755
		}
		if err := os.WriteFile(filepath.Join(b, name), []byte(data), mode); err != nil {
			t.Fatal(err)
		}
	}
	if sign {
		run(t, "/usr/bin/codesign", "-s", "-", "--force", b)
	}
	return b
}

func zipApp(t *testing.T, bundle, zip string) string {
	t.Helper()
	run(t, "/usr/bin/ditto", "-c", "-k", "--keepParent", bundle, zip)
	return zip
}

// fakeSelf is a running app for the in-process tests: a real, signed bundle
// in root/Applications that this test process pretends to run from.
func fakeSelf(t *testing.T, root string) self {
	t.Helper()
	t.Setenv("HOME", filepath.Join(root, "home")) // the cache and data folders
	b := makeBundle(t, filepath.Join(root, "Applications"), testID, "1.0", "healthy", true)
	s, err := selfAt(filepath.Join(b, "Contents", "MacOS", testExe))
	if err != nil {
		t.Fatal(err)
	}
	return s
}

func TestPrepareStagesAndVerifies(t *testing.T) {
	root := realTemp(t)
	s := fakeSelf(t, root)
	zip := zipApp(t, makeBundle(t, filepath.Join(root, "build"), testID, "2.0", "healthy", true), filepath.Join(root, "u.zip"))
	st, err := prepare(s, testReq, false, zip, quiet)
	if err != nil {
		t.Fatal(err)
	}
	if st.Version() != "2.0" || st.Build() != "2.0" || filepath.Base(st.Path()) != "SUTest.app" {
		t.Errorf("staged %q %q at %s", st.Version(), st.Build(), st.Path())
	}
	var a, b syscall.Stat_t
	if syscall.Stat(st.Path(), &a) != nil || syscall.Stat(filepath.Dir(s.bundle), &b) != nil || a.Dev != b.Dev {
		t.Errorf("staged on device %d, the app is on %d", a.Dev, b.Dev)
	}
	if fi, err := os.Stat(st.dir); err != nil || fi.Mode().Perm() != 0o700 {
		t.Errorf("staging folder %s: %v %v", st.dir, fi.Mode(), err)
	}
	if got, _ := os.ReadFile(filepath.Join(st.Path(), "Contents/Resources/version")); string(got) != "2.0\n" {
		t.Errorf("staged version file %q", got)
	}
	if want := filepath.Join(root, "home/Library/Caches", testID, "selfupdate") + "/"; !strings.HasPrefix(st.dir, want) {
		t.Errorf("staged in %s, want the cache folder %s", st.dir, want)
	}

	// While a Staged holds its folder, another Prepare leaves it alone; once
	// nothing holds it (the app quit), the next Prepare removes it.
	st2, err := prepare(s, testReq, false, zip, quiet)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(st.dir); err != nil {
		t.Fatalf("a second Prepare removed a held staging folder: %v", err)
	}
	st.lock.Close()
	st3, err := prepare(s, testReq, false, zip, quiet)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(st.dir); !os.IsNotExist(err) {
		t.Errorf("a stale staging folder survived the next Prepare: %v", err)
	}
	for _, x := range []*Staged{st2, st3} {
		if err := x.Discard(); err != nil {
			t.Fatal(err)
		}
		if _, err := os.Stat(x.dir); !os.IsNotExist(err) {
			t.Errorf("Discard left %s: %v", x.dir, err)
		}
	}
}

func TestPrepareRejects(t *testing.T) {
	root := realTemp(t)
	s := fakeSelf(t, root)
	builds := filepath.Join(root, "builds")
	n := 0
	bundleZip := func(t *testing.T, id string, sign bool, after func(b string)) string {
		n++
		d := filepath.Join(builds, fmt.Sprint(n))
		b := makeBundle(t, d, id, "2.0", "healthy", sign)
		if after != nil {
			after(b)
		}
		return zipApp(t, b, filepath.Join(d, "u.zip"))
	}
	writeZip := func(t *testing.T, entries ...entry) string {
		n++
		p := filepath.Join(builds, fmt.Sprintf("%d.zip", n))
		r := mkzip(t, entries...)
		b := make([]byte, r.Size())
		r.ReadAt(b, 0)
		if err := os.WriteFile(p, b, 0o600); err != nil {
			t.Fatal(err)
		}
		return p
	}
	if err := os.MkdirAll(builds, 0o755); err != nil {
		t.Fatal(err)
	}
	selfReq, err := selfRequirement()
	if err != nil {
		t.Fatal(err)
	}
	for _, c := range []struct {
		name string
		zip  func(t *testing.T) string
		req  string
		want string
	}{
		{"tampered", func(t *testing.T) string {
			return bundleZip(t, testID, true, func(b string) {
				if err := os.WriteFile(filepath.Join(b, "Contents/Resources/notes.txt"), []byte("changed after signing\n"), 0o644); err != nil {
					t.Fatal(err)
				}
			})
		}, testReq, "does not match its contents"},
		{"unsigned", func(t *testing.T) string {
			return bundleZip(t, testID, false, func(b string) {
				run(t, "/usr/bin/codesign", "--remove-signature", filepath.Join(b, "Contents/MacOS/"+testExe))
			})
		}, testReq, "not signed"},
		{"another app", func(t *testing.T) string { return bundleZip(t, "dev.gonativeui.other", true, nil) },
			`identifier "dev.gonativeui.other" or identifier "` + testID + `"`, "another app"},
		{"another signer", func(t *testing.T) string { return bundleZip(t, testID, true, nil) },
			selfReq, "publisher"},
		{"dot-dot entry", func(t *testing.T) string {
			return writeZip(t, bundle(file("SUTest.app/../../escaped", "x"))...)
		}, testReq, `".."`},
		{"link outside", func(t *testing.T) string {
			return writeZip(t, bundle(link("SUTest.app/Contents/l", "../../../../.."), file("SUTest.app/Contents/l/escaped", "x"))...)
		}, testReq, "outside"},
	} {
		t.Run(c.name, func(t *testing.T) {
			zip := c.zip(t)
			st, err := prepare(s, c.req, false, zip, quiet)
			if err == nil {
				st.Discard()
				t.Fatal("Prepare accepted it")
			}
			if !errors.Is(err, ErrRejected) || !strings.Contains(err.Error(), c.want) {
				t.Fatalf("Prepare = %v, want ErrRejected mentioning %q", err, c.want)
			}
		})
	}
	for _, p := range []string{filepath.Join(root, "escaped"), filepath.Join(builds, "escaped"), "/escaped"} {
		if _, err := os.Lstat(p); err == nil {
			t.Errorf("%s was written", p)
		}
	}
}

func TestPrepareRejectsDowngrades(t *testing.T) {
	root := realTemp(t)
	s := fakeSelf(t, root) // 1.0, build 1.0
	for i, c := range []struct {
		version, build string
		allow          bool
		want           error // nil, ErrNotNewer, or ErrRejected alone
	}{
		{"0.9", "0.9", false, ErrNotNewer},
		{"1.0", "1.0", false, ErrNotNewer},
		{"1.0", "1.0.1", false, nil},
		{"1.0b1", "1", false, ErrRejected},
		{"0.9", "0.9", true, nil},
		{"1.0", "1.0", true, nil},
	} {
		d := filepath.Join(root, "builds", fmt.Sprint(i))
		b := makeBundleOf(t, d, bundleSpec{id: testID, version: c.version, build: c.build, behavior: "healthy", exe: testExe, sign: true})
		st, err := prepare(s, testReq, c.allow, zipApp(t, b, filepath.Join(d, "u.zip")), quiet)
		if st != nil {
			st.Discard()
		}
		switch {
		case c.want == nil && err != nil,
			c.want == ErrNotNewer && !errors.Is(err, ErrNotNewer),
			c.want == ErrRejected && (!errors.Is(err, ErrRejected) || errors.Is(err, ErrNotNewer)):
			t.Errorf("%s (%s) over 1.0 (1.0), AllowDowngrade %v: %v, want %v", c.version, c.build, c.allow, err, c.want)
		}
	}
}

// Options.Requirement works in a Go test binary and nowhere else.
func TestRequirementOnlyInATestBinary(t *testing.T) {
	var log strings.Builder
	t.Cleanup(func() {
		Configure(Options{})
		isTestBinary = launchguard.TestBinary
		requirementWarned.Store(false)
	})
	Configure(Options{Requirement: testReq, Logger: slog.New(slog.NewTextHandler(&log, nil))})
	if !launchguard.TestBinary() || options().Requirement != testReq {
		t.Fatal("the requirement is not honoured in this test binary")
	}
	isTestBinary = func() bool { return false }
	requirementWarned.Store(false)
	for range 3 {
		if o := options(); o.Requirement != "" {
			t.Fatalf("a shipped app keeps the requirement %q", o.Requirement)
		}
	}
	if n := strings.Count(log.String(), "ignored outside a Go test binary"); n != 1 {
		t.Errorf("%d warnings, want 1:\n%s", n, log.String())
	}
	if r, err := requirementFor(options()); err != nil || r == testReq {
		t.Errorf("requirementFor = %q, %v; want the app's own", r, err)
	}
}

// The same bundle, its executable not named like a test binary, is a shipped
// app: the injected requirement is ignored, so its ad-hoc signature cannot be
// updated.
func TestRequirementIgnoredInAShippedApp(t *testing.T) {
	w := newWorld(t, 3*time.Second)
	app := makeBundleOf(t, filepath.Join(w.root, "Shipped"), bundleSpec{id: testID, version: "1.0", build: "1.0", behavior: "healthy", exe: "testapp", sign: true})
	zip := zipApp(t, makeBundle(t, filepath.Join(w.root, "build"), testID, "2.0", "healthy", true), filepath.Join(w.root, "u.zip"))
	cmd := exec.Command(filepath.Join(app, "Contents/MacOS/testapp"), "update", zip)
	cmd.Env = w.env
	out, err := cmd.CombinedOutput()
	if err == nil {
		t.Fatalf("the shipped app updated itself:\n%s", out)
	}
	r := w.wait(t, 5*time.Second, "the refusal", func(r report) bool { return r.Event == "failed" })
	if !strings.Contains(r.Err, ErrNotSigned.Error()) {
		t.Errorf("the shipped app reported %q, want ErrNotSigned", r.Err)
	}
	if !strings.Contains(string(out), "ignored outside a Go test binary") {
		t.Errorf("no warning in the app's log:\n%s", out)
	}
	if v, _ := os.ReadFile(filepath.Join(app, "Contents/Resources/version")); string(v) != "1.0\n" {
		t.Errorf("the shipped app's version is now %q", v)
	}
}

func TestSupported(t *testing.T) {
	root := realTemp(t)
	s := fakeSelf(t, root)
	if err := supportedAt(s, false); err != nil {
		t.Fatalf("a writable signed bundle: %v", err)
	}
	// The test binary is signed by the linker, ad hoc: no publisher.
	if err := supportedAt(s, true); !errors.Is(err, ErrNotSigned) {
		t.Errorf("ad-hoc running code: %v, want ErrNotSigned", err)
	}
	dev := s
	dev.id = testID + ".dev"
	if err := supportedAt(dev, false); !errors.Is(err, ErrDevBundle) {
		t.Errorf("a .dev identifier: %v, want ErrDevBundle", err)
	}
	tr := filepath.Join(root, "AppTranslocation", "6B1C", "d")
	trs, err := selfAt(filepath.Join(makeBundle(t, tr, testID, "1.0", "healthy", false), "Contents/MacOS/"+testExe))
	if err != nil {
		t.Fatal(err)
	}
	if err := supportedAt(trs, false); !errors.Is(err, ErrTranslocated) {
		t.Errorf("translocated: %v, want ErrTranslocated", err)
	}
	if _, err := selfAt("/usr/bin/true"); !errors.Is(err, ErrNotBundled) {
		t.Errorf("/usr/bin/true: %v, want ErrNotBundled", err)
	}
	nested := makeBundle(t, filepath.Join(root, "Outer.app", "Contents", "Helpers"), testID, "1.0", "healthy", false)
	if _, err := selfAt(filepath.Join(nested, "Contents/MacOS/"+testExe)); !errors.Is(err, ErrNotBundled) {
		t.Errorf("a bundle inside a bundle: %v, want ErrNotBundled", err)
	}

	if os.Getuid() == 0 {
		t.Skip("root writes anywhere")
	}
	apps := filepath.Dir(s.bundle)
	if err := os.Chmod(apps, 0o555); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { os.Chmod(apps, 0o755) })
	if err := supportedAt(s, false); !errors.Is(err, ErrNotWritable) {
		t.Errorf("an unwritable folder: %v, want ErrNotWritable", err)
	}
	if _, err := Prepare(filepath.Join(root, "none.zip")); err == nil {
		t.Error("Prepare from the test binary worked")
	}
}

func TestRenameSwap(t *testing.T) {
	root := realTemp(t)
	a, b := filepath.Join(root, "a"), filepath.Join(root, "b")
	for _, d := range []string{a, b} {
		os.Mkdir(d, 0o755)
		os.WriteFile(filepath.Join(d, "name"), []byte(filepath.Base(d)), 0o644)
	}
	if err := renameSwap(a, b); err != nil {
		t.Fatal(err)
	}
	if got, _ := os.ReadFile(filepath.Join(a, "name")); string(got) != "b" {
		t.Errorf("after the swap a holds %q", got)
	}
	err := renameSwap(a, filepath.Join(root, "missing"))
	if !errors.Is(err, syscall.ENOENT) {
		t.Errorf("swap with a missing path: %v, want ENOENT", err)
	}
}

func TestSwapWithoutRenameSwap(t *testing.T) {
	renameSwapFn = func(string, string) error { return &os.LinkError{Op: "renamex_np", Err: syscall.ENOTSUP} }
	t.Cleanup(func() { renameSwapFn = renameSwap })
	root := realTemp(t)
	staging := filepath.Join(root, "staging")
	bundle, staged := filepath.Join(root, "X.app"), filepath.Join(staging, "new", "X.app")
	for _, d := range []struct{ path, name string }{{bundle, "old"}, {staged, "new"}} {
		os.MkdirAll(d.path, 0o755)
		os.WriteFile(filepath.Join(d.path, "v"), []byte(d.name), 0o644)
	}
	read := func(p string) string { b, _ := os.ReadFile(filepath.Join(p, "v")); return string(b) }

	backup, err := swapIn(bundle, staged, staging)
	if err != nil || backup != filepath.Join(staging, "old.app") || read(bundle) != "new" || read(backup) != "old" {
		t.Fatalf("swapIn = %q, %v; bundle %q backup %q", backup, err, read(bundle), read(backup))
	}
	if err := swapBack(bundle, backup, staging); err != nil || read(bundle) != "old" || read(filepath.Join(staging, "failed.app")) != "new" {
		t.Fatalf("swapBack = %v; bundle %q", err, read(bundle))
	}
	// The second rename fails (nothing staged): the old bundle goes back.
	if _, err := swapIn(bundle, filepath.Join(staging, "new", "gone.app"), filepath.Join(root, "s2")); err == nil {
		t.Fatal("swapIn of a missing bundle worked")
	}
	if read(bundle) != "old" {
		t.Fatalf("after a failed swap the bundle holds %q", read(bundle))
	}
}

// The helper runs from a copy of the whole bundle, which must validate as the
// bundle does: a Developer ID signature binds the Info.plist, and the system
// kills the executable copied out of its bundle at exec. The copy carries no
// quarantine.
func TestHelperCopyOfTheBundle(t *testing.T) {
	root := realTemp(t)
	b := makeBundle(t, filepath.Join(root, "Applications"), testID, "1.0", "healthy", true)
	run(t, "/usr/bin/xattr", "-w", "com.apple.quarantine", "0081;00000000;Safari;", b)
	cp := filepath.Join(root, "staging", "helper", "SUTest.app")
	if err := os.MkdirAll(filepath.Dir(cp), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := copyBundle(b, cp); err != nil {
		t.Fatal(err)
	}
	if err := verifyCode(cp, testReq); err != nil {
		t.Fatalf("the copy does not validate: %v", err)
	}
	if out, err := exec.Command("/usr/bin/xattr", "-p", "com.apple.quarantine", cp).CombinedOutput(); err == nil {
		t.Errorf("the copy is quarantined: %s", out)
	}
	if err := copyBundle(b, cp); err == nil {
		t.Error("copying over an existing copy worked")
	}
}

func TestResultFor(t *testing.T) {
	root := realTemp(t)
	bundle := filepath.Join(root, "X.app")
	os.Mkdir(bundle, 0o755)
	dev, ino, err := inode(bundle)
	if err != nil {
		t.Fatal(err)
	}
	s := self{bundle: bundle}
	dir := filepath.Join(root, "state")
	staging := func() string {
		d, err := os.MkdirTemp(root, "staging-")
		if err != nil {
			t.Fatal(err)
		}
		os.WriteFile(filepath.Join(d, "lock"), nil, 0o600)
		return d
	}
	base := record{Bundle: bundle, Dev: dev, From: "1.0", To: "2.0", Nonce: "n"}
	consumed := func() bool { _, ok := readRecord(dir); return !ok }

	r := base
	r.Phase, r.Reason = phaseRolledBack, "it hung"
	writeRecord(dir, r)
	if res, rec := resultFor(s, dir, quiet); res.Status != RolledBack || res.Reason != "it hung" || rec != nil || !consumed() {
		t.Errorf("rolled back: %+v %v consumed=%v", res, rec, consumed())
	}
	r.Phase = phaseFailed
	writeRecord(dir, r)
	if res, _ := resultFor(s, dir, quiet); res.Status != Failed || !consumed() {
		t.Errorf("failed: %+v", res)
	}

	r = base
	r.Phase, r.NewIno, r.OldIno, r.Staging = phaseVerifying, ino, ino+1, staging()
	writeRecord(dir, r)
	if res, rec := resultFor(s, dir, quiet); res.Status != Updated || rec == nil || consumed() {
		t.Errorf("the new version: %+v %v", res, rec)
	}

	// The old version, with its helper still running: not ours to decide.
	r.NewIno, r.OldIno = ino+1, ino
	writeRecord(dir, r)
	lock, err := os.Open(filepath.Join(r.Staging, "lock"))
	if err != nil {
		t.Fatal(err)
	}
	syscall.Flock(int(lock.Fd()), syscall.LOCK_EX)
	if res, _ := resultFor(s, dir, quiet); res.Status != None || consumed() {
		t.Errorf("the old version while the helper runs: %+v", res)
	}
	lock.Close()
	// Its helper gone: interrupted.
	if res, _ := resultFor(s, dir, quiet); res.Status != Failed || !consumed() {
		t.Errorf("the old version after the helper died: %+v", res)
	}
	if _, err := os.Stat(r.Staging); !os.IsNotExist(err) {
		t.Errorf("the interrupted update's staging folder is still there: %v", err)
	}

	r.Bundle = filepath.Join(root, "Other.app")
	writeRecord(dir, r)
	if res, _ := resultFor(s, dir, quiet); res.Status != None || consumed() {
		t.Errorf("another bundle's record: %+v", res)
	}
}

// ---- The whole update, through the helper ----

type report struct {
	Pid      int      `json:"pid"`
	Event    string   `json:"event"`
	Args     []string `json:"args"`
	Version  string   `json:"version"`
	Behavior string   `json:"behavior"`
	Status   string   `json:"status"`
	From     string   `json:"from"`
	To       string   `json:"to"`
	Reason   string   `json:"reason"`
	Staged   string   `json:"staged"`
	Err      string   `json:"err"`
}

type world struct {
	root, home, reports, app string
	env                      []string
}

func newWorld(t *testing.T, health time.Duration) *world {
	root := realTemp(t)
	w := &world{root: root, home: filepath.Join(root, "home"), reports: filepath.Join(root, "reports")}
	for _, d := range []string{w.home, w.reports} {
		os.MkdirAll(d, 0o755)
	}
	w.app = makeBundle(t, filepath.Join(root, "Applications"), testID, "1.0", "healthy", true)
	w.env = append(os.Environ(), "HOME="+w.home, "SUTEST_REPORT="+w.reports, "SUTEST_HEALTH_TIMEOUT="+health.String())
	t.Cleanup(func() { w.cleanup(t) })
	return w
}

func (w *world) all() []report {
	ents, _ := os.ReadDir(w.reports)
	var out []report
	for _, e := range ents {
		if !strings.HasSuffix(e.Name(), ".json") {
			continue
		}
		b, err := os.ReadFile(filepath.Join(w.reports, e.Name()))
		var r report
		if err == nil && json.Unmarshal(b, &r) == nil {
			out = append(out, r)
		}
	}
	return out
}

func (w *world) wait(t *testing.T, limit time.Duration, what string, ok func(report) bool) report {
	t.Helper()
	for deadline := time.Now().Add(limit); time.Now().Before(deadline); time.Sleep(50 * time.Millisecond) {
		for _, r := range w.all() {
			if ok(r) {
				return r
			}
		}
	}
	w.dump(t)
	t.Fatalf("no report of %s within %v", what, limit)
	return report{}
}

func (w *world) dump(t *testing.T) {
	t.Helper()
	for _, r := range w.all() {
		t.Logf("report %+v", r)
	}
	if b, err := os.ReadFile(filepath.Join(w.home, "Library/Logs", testID, "selfupdate.log")); err == nil {
		t.Logf("selfupdate.log:\n%s", b)
	}
}

// update runs the installed app with "update zip" and waits for it to hand
// over to the helper and exit; it returns the helper's pid and the staging
// folder.
func (w *world) update(t *testing.T, zip string) (helperPid int, staging string) {
	t.Helper()
	cmd := exec.Command(filepath.Join(w.app, "Contents/MacOS/"+testExe), "update", zip)
	cmd.Env = w.env
	out, err := cmd.CombinedOutput()
	if err != nil {
		w.dump(t)
		t.Fatalf("the app's update run: %v\n%s", err, out)
	}
	u := w.wait(t, 5*time.Second, "the hand-over", func(r report) bool { return r.Event == "updating" })
	h := w.wait(t, 5*time.Second, "the helper", func(r report) bool { return len(r.Args) > 0 && r.Args[0] == helperFlag })
	return h.Pid, filepath.Dir(filepath.Dir(u.Staged))
}

func (w *world) installed(t *testing.T) string {
	b, _ := os.ReadFile(filepath.Join(w.app, "Contents/Resources/version"))
	return strings.TrimSpace(string(b))
}

func waitGone(t *testing.T, pid int, limit time.Duration) {
	t.Helper()
	for deadline := time.Now().Add(limit); time.Now().Before(deadline); time.Sleep(50 * time.Millisecond) {
		if !alive(pid) {
			return
		}
	}
	t.Fatalf("process %d is still running after %v", pid, limit)
}

// cleanup kills, by pid, every process of the test still running from its
// folders, and removes staging folders the helper left.
func (w *world) cleanup(t *testing.T) {
	var prefixes []string
	prefixes = append(prefixes, w.root+"/")
	for _, r := range w.all() {
		if r.Staged != "" {
			staging := filepath.Dir(filepath.Dir(r.Staged))
			prefixes = append(prefixes, staging+"/")
			defer removeStaging(staging)
		}
	}
	for _, r := range w.all() {
		p := processPath(r.Pid)
		for _, pre := range prefixes {
			if p != "" && strings.HasPrefix(p, pre) {
				t.Logf("killing leftover process %d (%s)", r.Pid, p)
				syscall.Kill(r.Pid, syscall.SIGKILL)
				break
			}
		}
	}
}

// The production launcher, for real: Launch Services opens a test bundle with
// arguments and reports its process id. Off by default (it registers the
// bundle with Launch Services, and unregisters it at the end):
//
//	GNUI_LIVE_SELFUPDATE=1 go test -run Live ./app/selfupdate
func TestLiveLaunchServices(t *testing.T) {
	if os.Getenv("GNUI_LIVE_SELFUPDATE") == "" {
		t.Skip("set GNUI_LIVE_SELFUPDATE=1 to open a test bundle through Launch Services")
	}
	root := realTemp(t)
	b := makeBundle(t, filepath.Join(root, "Applications"), testID+".live", "1.0", "healthy", true)
	t.Cleanup(func() {
		exec.Command("/System/Library/Frameworks/CoreServices.framework/Frameworks/LaunchServices.framework/Support/lsregister", "-u", b).Run()
	})
	// Only a signed bundle that exists is opened (a missing one is refused
	// before Launch Services), and with no system alerts: nothing is left on
	// the screen.
	t.Setenv(launchguard.Env, "1")
	reports := filepath.Join(root, "reports with spaces")
	os.Mkdir(reports, 0o755)
	t0 := time.Now()
	pid, err := launchServices(b, []string{"report-to", reports})
	if err != nil {
		t.Fatal(err)
	}
	w := &world{root: root, reports: reports}
	t.Cleanup(func() { w.cleanup(t) })
	r := w.wait(t, 20*time.Second, "the opened bundle", func(r report) bool { return r.Event == "opened" })
	// A process that does not check in as an app (this one has no
	// NSApplication) has no pid in Launch Services' answer; the helper then
	// finds it by its executable.
	if (pid != 0 && pid != r.Pid) || len(r.Args) != 2 || r.Args[1] != reports {
		t.Errorf("Launch Services said pid %d; the bundle reported %+v", pid, r)
	}
	if d := time.Since(t0); d > 15*time.Second {
		t.Errorf("the launch took %v", d)
	}
	if ps := processesOf(filepath.Join(b, "Contents/MacOS/"+testExe)); len(ps) != 1 || ps[0] != r.Pid {
		t.Errorf("processesOf found %v, want [%d]", ps, r.Pid)
	}
	if _, err := launchServices(filepath.Join(root, "Missing.app"), nil); err == nil {
		t.Error("opening a missing bundle worked")
	}

}

func TestUpdateIsInstalledAndKept(t *testing.T) {
	w := newWorld(t, 20*time.Second)
	zip := zipApp(t, makeBundle(t, filepath.Join(w.root, "build"), testID, "2.0", "healthy", true), filepath.Join(w.root, "u.zip"))
	helper, staging := w.update(t, zip)

	r := w.wait(t, 20*time.Second, "the new version starting", func(r report) bool { return r.Version == "2.0" && r.Event == "started" })
	if r.Status != "updated" || r.From != "1.0" || r.To != "2.0" || len(r.Args) != 1 || r.Args[0] != "relaunched" {
		t.Errorf("the new version saw %+v", r)
	}
	waitGone(t, helper, 20*time.Second)
	if v := w.installed(t); v != "2.0" {
		w.dump(t)
		t.Fatalf("installed version %q, want 2.0", v)
	}
	if _, err := os.Lstat(staging); !os.IsNotExist(err) {
		t.Errorf("the backup and staging folder %s are still there (%v)", staging, err)
	}
	for _, d := range []string{"Library/Application Support", "Library/Caches"} {
		if ents, _ := os.ReadDir(filepath.Join(w.home, d, testID, "selfupdate")); len(ents) != 0 {
			t.Errorf("%s still holds %v", d, ents)
		}
	}
	log, _ := os.ReadFile(filepath.Join(w.home, "Library/Logs", testID, "selfupdate.log"))
	for _, step := range []string{"the app quit", "verified the update again", "swapped the new version", "launched the new version", "removed the old one"} {
		if !strings.Contains(string(log), step) {
			t.Errorf("selfupdate.log has no %q line:\n%s", step, log)
		}
	}
}

// An app that allows a downgrade passes the choice to its helper, which
// checks the versions again before the swap.
func TestDowngradeAllowedByTheApp(t *testing.T) {
	w := newWorld(t, 20*time.Second)
	zip := zipApp(t, makeBundle(t, filepath.Join(w.root, "build"), testID, "0.9", "healthy", true), filepath.Join(w.root, "u.zip"))
	w.env = append(w.env, "SUTEST_ALLOW_DOWNGRADE=1")
	helper, _ := w.update(t, zip)
	r := w.wait(t, 20*time.Second, "the older version starting", func(r report) bool { return r.Version == "0.9" && r.Event == "started" })
	if r.Status != "updated" || r.From != "1.0" || r.To != "0.9" {
		t.Errorf("the older version saw %+v", r)
	}
	waitGone(t, helper, 20*time.Second)
	if v := w.installed(t); v != "0.9" {
		w.dump(t)
		t.Fatalf("installed version %q, want 0.9", v)
	}
}

func TestUpdateThatDoesNotStartIsRolledBack(t *testing.T) {
	for _, c := range []struct{ behavior, event, reason string }{
		{"hang", "hanging", "did not report that it started within 3s"},
		{"exit", "exiting", "quit before it reported"},
	} {
		t.Run(c.behavior, func(t *testing.T) {
			w := newWorld(t, 3*time.Second)
			zip := zipApp(t, makeBundle(t, filepath.Join(w.root, "build"), testID, "2.0", c.behavior, true), filepath.Join(w.root, "u.zip"))
			helper, staging := w.update(t, zip)

			nw := w.wait(t, 20*time.Second, "the new version running", func(r report) bool { return r.Version == "2.0" && r.Event == c.event })
			old := w.wait(t, 30*time.Second, "the old version again", func(r report) bool {
				return r.Version == "1.0" && r.Event == "started" && len(r.Args) == 1 && r.Args[0] == "relaunched"
			})
			if old.Status != "rolled back" || !strings.Contains(old.Reason, c.reason) || old.To != "2.0" {
				t.Errorf("the old version saw %+v, want rolled back: %q", old, c.reason)
			}
			waitGone(t, helper, 20*time.Second)
			waitGone(t, nw.Pid, 5*time.Second)
			if v := w.installed(t); v != "1.0" {
				w.dump(t)
				t.Fatalf("installed version %q after the rollback, want 1.0", v)
			}
			if _, err := os.Lstat(staging); !os.IsNotExist(err) {
				t.Errorf("the staging folder %s is still there (%v)", staging, err)
			}
			if _, ok := readRecord(filepath.Join(w.home, "Library/Application Support", testID, "selfupdate")); ok {
				t.Error("the old version did not consume the record")
			}
		})
	}
}
