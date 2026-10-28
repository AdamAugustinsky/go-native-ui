//go:build windows

package selfupdate

import (
	"archive/zip"
	"bytes"
	"crypto/rand"
	"crypto/sha256"
	"encoding/binary"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"sort"
	"strings"
	"sync"
	"testing"
	"time"
	"unicode/utf16"

	"gonativeui/internal/winres"
)

// The D61 matrix on Windows (S4): a good update, a hang rolled back, a wrong
// digest, an older version, another machine, another app and another
// signer refused, and an unsigned app refused without Options.Unsigned. The
// executables are one build of internal/testappwin with a version resource,
// patched per variant (the file version, the behavior marker, the machine,
// the AppUserModelID); the signed cases sign them with throwaway
// certificates made by PowerShell and trusted for the test.

const testID = "dev.gonativeui.selfupdatetest"

var quiet = slog.New(slog.NewTextHandler(io.Discard, nil))

var testApp struct {
	once sync.Once
	b    []byte
	err  error
}

// appBytes builds internal/testappwin once, with the version resource
// linked in.
func appBytes(t *testing.T) []byte {
	t.Helper()
	testApp.once.Do(func() {
		v := winres.Version{File: [4]uint16{1, 0, 0, 1}, Product: [4]uint16{1, 0, 0, 1},
			Strings: []winres.KV{{Key: "ProductVersion", Value: "1.0.0"}, {Key: "AppUserModelID", Value: testID}}}
		obj, err := winres.COFF(winres.MachineFor(runtime.GOARCH), []winres.Resource{
			{Type: winres.TypeVersion, ID: 1, Lang: winres.LangEnUS, Data: v.Encode()}})
		if err != nil {
			testApp.err = err
			return
		}
		syso := filepath.Join("internal", "testappwin", "zz_gnui_rsrc_windows_"+runtime.GOARCH+".syso")
		if testApp.err = os.WriteFile(syso, obj, 0o644); testApp.err != nil {
			return
		}
		defer os.Remove(syso)
		dir, err := os.MkdirTemp("", "selfupdate-testapp-*")
		if err != nil {
			testApp.err = err
			return
		}
		defer os.RemoveAll(dir)
		out := filepath.Join(dir, "sutest.exe")
		if b, err := exec.Command("go", "build", "-o", out, "./internal/testappwin").CombinedOutput(); err != nil {
			testApp.err = fmt.Errorf("building the test app: %v\n%s", err, b)
			return
		}
		testApp.b, testApp.err = os.ReadFile(out)
	})
	if testApp.err != nil {
		t.Fatal(testApp.err)
	}
	return testApp.b
}

type variant struct {
	version  [4]uint16
	behavior string // "" is healthy
	machine  uint16 // 0 is the build's
	id       string // "" is testID; the same length
}

// makeExe writes the variant to path.
func makeExe(t *testing.T, path string, v variant) {
	t.Helper()
	b := bytes.Clone(appBytes(t))
	le := binary.LittleEndian
	// The fixed file info, found through the resource (the signature's bytes
	// also occur in winres's code on amd64).
	pf, err := winres.Read(bytes.NewReader(b), int64(len(b)))
	if err != nil {
		t.Fatal(err)
	}
	vr, ok := winres.Find(pf.Resources, winres.TypeVersion, 1)
	at := bytes.Index(b, vr.Data)
	sig := bytes.Index(vr.Data, []byte{0xBD, 0x04, 0xEF, 0xFE})
	if !ok || at < 0 || sig < 0 {
		t.Fatal("the version resource is not found")
	}
	i := at + sig
	q := v.version
	le.PutUint32(b[i+8:], uint32(q[0])<<16|uint32(q[1]))
	le.PutUint32(b[i+12:], uint32(q[2])<<16|uint32(q[3]))
	behavior := v.behavior
	if behavior == "" {
		behavior = "healthy"
	}
	m := []byte("SUTEST-BEHAVIOR=healthy                 ")
	j := bytes.Index(b, m)
	if j < 0 {
		t.Fatal("the behavior marker is not found")
	}
	repl := []byte(fmt.Sprintf("SUTEST-BEHAVIOR=%-24s", behavior))
	copy(b[j:], repl[:len(m)])
	if v.machine != 0 {
		le.PutUint16(b[le.Uint32(b[0x3C:])+4:], v.machine)
	}
	if v.id != "" {
		from, to := utf16le(testID), utf16le(v.id)
		if len(from) != len(to) {
			t.Fatal("the replacement id must have the same length")
		}
		k := bytes.Index(b, from)
		if k < 0 {
			t.Fatal("the AppUserModelID is not found")
		}
		copy(b[k:], to)
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, b, 0o755); err != nil {
		t.Fatal(err)
	}
}

func utf16le(s string) []byte {
	u := utf16.Encode([]rune(s))
	b := make([]byte, 2*len(u))
	for i, c := range u {
		binary.LittleEndian.PutUint16(b[2*i:], c)
	}
	return b
}

func zipOf(t *testing.T, exe, zipPath string) {
	t.Helper()
	f, err := os.Create(zipPath)
	if err != nil {
		t.Fatal(err)
	}
	zw := zip.NewWriter(f)
	w, err := zw.Create(filepath.Base(exe))
	if err != nil {
		t.Fatal(err)
	}
	b, err := os.ReadFile(exe)
	if err != nil {
		t.Fatal(err)
	}
	w.Write(b)
	if err := zw.Close(); err != nil {
		t.Fatal(err)
	}
	f.Close()
}

func digest(t *testing.T, path string) [32]byte {
	t.Helper()
	b, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	return sha256.Sum256(b)
}

// world is one install: root\Programs\SUTest\sutest.exe, with the app's
// folders under root.
type world struct {
	root, reports, exe string
	env                []string
}

func newWorld(t *testing.T, health time.Duration, unsigned bool) *world {
	t.Helper()
	root := t.TempDir()
	w := &world{root: root, reports: filepath.Join(root, "reports"), exe: filepath.Join(root, "Programs", "SUTest", "sutest.exe")}
	os.MkdirAll(w.reports, 0o755)
	local, roaming := filepath.Join(root, "local"), filepath.Join(root, "roaming")
	t.Setenv("LOCALAPPDATA", local)
	t.Setenv("APPDATA", roaming)
	w.env = append(os.Environ(), "SUTEST_REPORT="+w.reports, "SUTEST_HEALTH_TIMEOUT="+health.String())
	if unsigned {
		w.env = append(w.env, "SUTEST_UNSIGNED=1")
	}
	return w
}

type report struct {
	Pid      int      `json:"pid"`
	Event    string   `json:"event"`
	Args     []string `json:"args"`
	Exe      string   `json:"exe"`
	Version  string   `json:"version"`
	Behavior string   `json:"behavior"`
	Status   string   `json:"status"`
	From     string   `json:"from"`
	To       string   `json:"to"`
	Reason   string   `json:"reason"`
	Err      string   `json:"err"`
}

func (w *world) all() []report {
	m, _ := filepath.Glob(filepath.Join(w.reports, "*.json"))
	var out []report
	for _, p := range m {
		b, err := os.ReadFile(p)
		if err != nil {
			continue
		}
		var r report
		if json.Unmarshal(b, &r) == nil {
			out = append(out, r)
		}
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Pid < out[j].Pid })
	return out
}

func (w *world) wait(t *testing.T, limit time.Duration, what string, ok func(report) bool) report {
	t.Helper()
	deadline := time.Now().Add(limit)
	for {
		for _, r := range w.all() {
			if ok(r) {
				return r
			}
		}
		if time.Now().After(deadline) {
			for _, r := range w.all() {
				t.Logf("report: %+v", r)
			}
			w.logs(t)
			t.Fatalf("no report of %s within %v", what, limit)
		}
		time.Sleep(100 * time.Millisecond)
	}
}

// logs prints the helper's log.
func (w *world) logs(t *testing.T) {
	m, _ := filepath.Glob(filepath.Join(w.root, "local", testID, "Logs", "*"))
	for _, p := range m {
		b, _ := os.ReadFile(p)
		t.Logf("%s:\n%s", p, b)
	}
}

// update runs the installed app with "update <file> [digest]".
func (w *world) update(t *testing.T, file string, sum *[32]byte) {
	t.Helper()
	args := []string{"update", file}
	if sum != nil {
		args = append(args, hex.EncodeToString(sum[:]))
	}
	cmd := exec.Command(w.exe, args...)
	cmd.Env = w.env
	if out, err := cmd.CombinedOutput(); err != nil {
		for _, r := range w.all() {
			t.Logf("report: %+v", r)
		}
		t.Fatalf("the update run: %v\n%s", err, out)
	}
}

func installedVersion(t *testing.T, exe string) string {
	t.Helper()
	info, err := readExePath(exe)
	if err != nil {
		t.Fatal(err)
	}
	return info.version + "." + info.build
}

func TestUpdateIsInstalledAndKept(t *testing.T) {
	w := newWorld(t, 20*time.Second, true)
	makeExe(t, w.exe, variant{version: [4]uint16{1, 0, 0, 1}})
	upd := filepath.Join(w.root, "download", "sutest.exe")
	makeExe(t, upd, variant{version: [4]uint16{1, 1, 0, 1}})
	z := filepath.Join(w.root, "download", "SUTest-1.1.0-windows.zip")
	zipOf(t, upd, z)
	sum := digest(t, z)
	w.update(t, z, &sum)
	r := w.wait(t, 40*time.Second, "the new version started", func(r report) bool { return r.Event == "started" })
	if r.Version != "1.1.0.1" || r.Status != "updated" || len(r.Args) != 1 || r.Args[0] != "relaunched" {
		t.Errorf("the new version: %+v", r)
	}
	if got := installedVersion(t, w.exe); got != "1.1.0.1" {
		t.Errorf("installed version %s after the update", got)
	}
}

func TestUpdateThatDoesNotStartIsRolledBack(t *testing.T) {
	w := newWorld(t, 3*time.Second, true)
	makeExe(t, w.exe, variant{version: [4]uint16{1, 0, 0, 1}})
	upd := filepath.Join(w.root, "download", "sutest.exe")
	makeExe(t, upd, variant{version: [4]uint16{2, 0, 0, 0}, behavior: "hang"})
	sum := digest(t, upd)
	w.update(t, upd, &sum)
	hung := w.wait(t, 30*time.Second, "the new version hanging", func(r report) bool { return r.Event == "hanging" })
	r := w.wait(t, 40*time.Second, "the old version back", func(r report) bool { return r.Event == "started" })
	if r.Version != "1.0.0.1" || r.Status != "rolled back" || !strings.Contains(r.Reason, "did not report") {
		t.Errorf("after the rollback: %+v", r)
	}
	if got := installedVersion(t, w.exe); got != "1.0.0.1" {
		t.Errorf("installed version %s after the rollback", got)
	}
	if p, err := os.FindProcess(hung.Pid); err == nil {
		// The hung version was terminated: waiting on it returns at once.
		done := make(chan struct{})
		go func() { p.Wait(); close(done) }()
		select {
		case <-done:
		case <-time.After(10 * time.Second):
			p.Kill()
			t.Errorf("the hung version %d still runs", hung.Pid)
		}
	}
}

// TestPrepareRejects stages updates for an installed executable in this
// process (prepare with the installed copy as self).
func TestPrepareRejects(t *testing.T) {
	w := newWorld(t, 0, true)
	makeExe(t, w.exe, variant{version: [4]uint16{1, 2, 0, 5}})
	s, err := selfAt(w.exe)
	if err != nil {
		t.Fatal(err)
	}
	dl := filepath.Join(w.root, "download")
	good := filepath.Join(dl, "good.exe")
	makeExe(t, good, variant{version: [4]uint16{1, 3, 0, 0}})
	goodSum := digest(t, good)

	st, err := prepare(s, nil, &goodSum, false, good, quiet)
	if err != nil {
		t.Fatalf("a good update: %v", err)
	}
	if st.Build() != "0" { // the file version's fourth part
		t.Errorf("staged build %q", st.Build())
	}
	if err := st.Discard(); err != nil {
		t.Errorf("Discard: %v", err)
	}
	if _, err := os.Stat(st.dir); !os.IsNotExist(err) {
		t.Errorf("the staging folder is still there: %v", err)
	}

	other := map[uint16]uint16{winres.MachineAMD64: winres.MachineARM64, winres.MachineARM64: winres.MachineAMD64}
	cur, _ := readExePath(w.exe)
	cases := []struct {
		name string
		v    variant
		want error
	}{
		{"older", variant{version: [4]uint16{1, 1, 9, 9}}, ErrNotNewer},
		{"same", variant{version: [4]uint16{1, 2, 0, 5}}, ErrNotNewer},
		{"another machine", variant{version: [4]uint16{2, 0, 0, 0}, machine: other[cur.machine]}, ErrRejected},
		{"another app", variant{version: [4]uint16{2, 0, 0, 0}, id: "dev.gonativeui.selfupdatetesX"}, ErrRejected},
	}
	for _, c := range cases {
		p := filepath.Join(dl, strings.ReplaceAll(c.name, " ", "-")+".exe")
		makeExe(t, p, c.v)
		sum := digest(t, p)
		if _, err := prepare(s, nil, &sum, false, p, quiet); !errors.Is(err, c.want) {
			t.Errorf("%s: %v, want %v", c.name, err, c.want)
		}
	}
	// A downgrade the app allows.
	older := filepath.Join(dl, "older.exe")
	olderSum := digest(t, older)
	if st, err := prepare(s, nil, &olderSum, true, older, quiet); err != nil {
		t.Errorf("an allowed downgrade: %v", err)
	} else {
		st.Discard()
	}
	// The wrong digest.
	var wrong [32]byte
	if _, err := prepare(s, nil, &wrong, false, good, quiet); !errors.Is(err, ErrRejected) {
		t.Errorf("a wrong digest: %v", err)
	}
	// Unsigned without a digest: Prepare's way.
	if _, err := prepare(s, nil, nil, false, good, quiet); !errors.Is(err, ErrNotSigned) {
		t.Errorf("unsigned without a digest: %v", err)
	}
	// Not an executable; a zip of two.
	junk := filepath.Join(dl, "junk.exe")
	os.WriteFile(junk, []byte("not a PE file at all"), 0o644)
	junkSum := digest(t, junk)
	if _, err := prepare(s, nil, &junkSum, false, junk, quiet); !errors.Is(err, ErrRejected) {
		t.Errorf("junk: %v", err)
	}
	z := filepath.Join(dl, "two.zip")
	f, _ := os.Create(z)
	zw := zip.NewWriter(f)
	for _, n := range []string{"a.exe", "b.exe"} {
		ww, _ := zw.Create(n)
		ww.Write([]byte("x"))
	}
	zw.Close()
	f.Close()
	zSum := digest(t, z)
	if _, err := prepare(s, nil, &zSum, false, z, quiet); !errors.Is(err, ErrRejected) {
		t.Errorf("a zip of two executables: %v", err)
	}
	// Nothing is left staged.
	root, _ := stagingRoot(testID)
	if ents, _ := os.ReadDir(root); len(ents) != 0 {
		t.Errorf("staging folders left: %v", ents)
	}
}

func TestSupportedRefusesUnsignedWithoutTheOption(t *testing.T) {
	w := newWorld(t, 0, false)
	makeExe(t, w.exe, variant{version: [4]uint16{1, 0, 0, 1}})
	cmd := exec.Command(w.exe, "supported")
	cmd.Env = w.env
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("%v\n%s", err, out)
	}
	r := w.wait(t, 10*time.Second, "Supported", func(r report) bool { return r.Event == "supported" })
	if !strings.Contains(r.Err, ErrNotSigned.Error()) {
		t.Errorf("Supported of an unsigned app = %q, want ErrNotSigned", r.Err)
	}
	cmd = exec.Command(w.exe, "supported")
	cmd.Env = append(w.env, "SUTEST_UNSIGNED=1")
	os.RemoveAll(w.reports)
	os.MkdirAll(w.reports, 0o755)
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("%v\n%s", err, out)
	}
	if r := w.wait(t, 10*time.Second, "Supported", func(r report) bool { return r.Event == "supported" }); r.Err != "" {
		t.Errorf("Supported with Options.Unsigned = %q", r.Err)
	}
}

func TestSupportedRefusesATestBinary(t *testing.T) {
	if err := Supported(); err == nil {
		t.Error("Supported() in a test binary = nil")
	}
	if LastResult().Status != None {
		t.Error("LastResult in a test binary is not None")
	}
	Started() // does nothing
}

// ---- Signed updates ----

// certs makes two throwaway code-signing certificates in the user's store,
// trusts them as roots for the machine (the runner's user is an
// administrator), and returns a function that signs a file with one.
func certs(t *testing.T) func(t *testing.T, path string, which int) {
	t.Helper()
	if os.Getenv("GITHUB_ACTIONS") != "true" && os.Getenv("GNUI_SELFUPDATE_SIGNING_TEST") != "1" {
		t.Skip("trusts throwaway roots for the machine: CI only, or set GNUI_SELFUPDATE_SIGNING_TEST=1")
	}
	var rnd [4]byte
	rand.Read(rnd[:])
	tag := hex.EncodeToString(rnd[:])
	dir := t.TempDir()
	script := fmt.Sprintf(`$ErrorActionPreference = 'Stop'
foreach ($n in 'A','B') {
  $c = New-SelfSignedCertificate -Type CodeSigningCert -Subject "CN=GNUI selfupdate test $n %s" -CertStoreLocation Cert:\CurrentUser\My -NotAfter (Get-Date).AddDays(1)
  Export-Certificate -Cert $c -FilePath "%s\$n.cer" | Out-Null
  Import-Certificate -FilePath "%s\$n.cer" -CertStoreLocation Cert:\LocalMachine\Root | Out-Null
  Write-Output $c.Thumbprint
}`, tag, dir, dir)
	out, err := exec.Command("powershell.exe", "-NoProfile", "-NonInteractive", "-Command", script).CombinedOutput()
	thumbs := strings.Fields(string(out))
	t.Cleanup(func() {
		for _, th := range thumbs {
			if len(th) == 40 {
				exec.Command("powershell.exe", "-NoProfile", "-NonInteractive", "-Command",
					fmt.Sprintf(`Remove-Item Cert:\LocalMachine\Root\%s, Cert:\CurrentUser\My\%s -ErrorAction SilentlyContinue`, th, th)).Run()
			}
		}
	})
	if err != nil || len(thumbs) != 2 {
		t.Fatalf("cannot make and trust test certificates: %v\n%s", err, out)
	}
	return func(t *testing.T, path string, which int) {
		t.Helper()
		s := fmt.Sprintf(`$ErrorActionPreference = 'Stop'
$r = Set-AuthenticodeSignature -FilePath '%s' -Certificate (Get-Item Cert:\CurrentUser\My\%s) -HashAlgorithm SHA256
if ($r.Status -ne 'Valid') { throw "signing: $($r.Status) $($r.StatusMessage)" }`, path, thumbs[which])
		if out, err := exec.Command("powershell.exe", "-NoProfile", "-NonInteractive", "-Command", s).CombinedOutput(); err != nil {
			t.Fatalf("signing %s: %v\n%s", path, err, out)
		}
	}
}

func TestSignedUpdates(t *testing.T) {
	sign := certs(t)
	w := newWorld(t, 20*time.Second, false)
	makeExe(t, w.exe, variant{version: [4]uint16{1, 0, 0, 1}})
	sign(t, w.exe, 0)
	dl := filepath.Join(w.root, "download")
	sameSigner := filepath.Join(dl, "a", "sutest.exe")
	otherSigner := filepath.Join(dl, "b", "sutest.exe")
	unsigned := filepath.Join(dl, "u", "sutest.exe")
	for _, p := range []string{sameSigner, otherSigner, unsigned} {
		makeExe(t, p, variant{version: [4]uint16{1, 1, 0, 0}})
	}
	sign(t, sameSigner, 0)
	sign(t, otherSigner, 1)

	pa, err := signerOf(w.exe)
	if err != nil {
		t.Fatalf("the signed app does not verify: %v", err)
	}
	t.Logf("publisher %q, root %q", pa.subject, pa.root)
	if pb, err := signerOf(otherSigner); err != nil || pb.same(pa) {
		t.Errorf("the other signer: %+v, %v", pb, err)
	}
	if _, err := signerOf(unsigned); err == nil {
		t.Error("an unsigned file verified")
	}

	s, err := selfAt(w.exe)
	if err != nil {
		t.Fatal(err)
	}
	for name, p := range map[string]string{"another signer": otherSigner, "unsigned": unsigned} {
		if _, err := prepare(s, pa, nil, false, p, quiet); !errors.Is(err, ErrRejected) {
			t.Errorf("%s: %v, want ErrRejected", name, err)
		}
	}

	// The good update, live, through Prepare.
	w.update(t, sameSigner, nil)
	r := w.wait(t, 40*time.Second, "the new version started", func(r report) bool { return r.Event == "started" })
	if r.Version != "1.1.0.0" || r.Status != "updated" {
		t.Errorf("the new version: %+v", r)
	}
}
