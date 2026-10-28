//go:build windows

package install

import (
	"bytes"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"syscall"
	"testing"
	"time"
	"unicode/utf16"
	"unsafe"

	"gonativeui/internal/win32"
	"gonativeui/internal/winreg"
)

type report struct {
	Pid       int      `json:"pid"`
	Event     string   `json:"event"`
	Exe       string   `json:"exe"`
	Args      []string `json:"args"`
	Installed string   `json:"installed"`
	IsCopy    bool     `json:"isCopy"`
	Err       string   `json:"err"`
}

// waitReport waits for a report of event in dir and returns it.
func waitReport(t *testing.T, dir, event string, limit time.Duration) report {
	t.Helper()
	deadline := time.Now().Add(limit)
	for {
		m, _ := filepath.Glob(filepath.Join(dir, "*-"+event+".json"))
		if len(m) > 0 {
			b, err := os.ReadFile(m[0])
			if err != nil {
				t.Fatal(err)
			}
			var r report
			if err := json.Unmarshal(b, &r); err != nil {
				t.Fatal(err)
			}
			if r.Err != "" {
				t.Fatalf("%s: %s", event, r.Err)
			}
			return r
		}
		if time.Now().After(deadline) {
			all, _ := filepath.Glob(filepath.Join(dir, "*.json"))
			t.Fatalf("no %q report within %v (reports: %v)", event, limit, all)
		}
		time.Sleep(100 * time.Millisecond)
	}
}

func regString(t *testing.T, path, name string) string {
	t.Helper()
	v, err := winreg.GetString(path, name)
	if err != nil {
		t.Errorf(`HKCU\%s %q: %v`, path, name, err)
	}
	return v
}

// shellOpen opens url as the shell does for a link (ShellExecuteW "open").
func shellOpen(url string) error {
	sh := win32.LoadSystem("shell32.dll")
	p := win32.Proc(sh, "ShellExecuteW")
	verb, _ := syscall.UTF16PtrFromString("open")
	file, _ := syscall.UTF16PtrFromString(url)
	r, _, _ := syscall.SyscallN(p, 0, uintptr(unsafe.Pointer(verb)), uintptr(unsafe.Pointer(file)), 0, 0, 1)
	if r <= 32 {
		return syscall.Errno(r)
	}
	return nil
}

// TestInstallRoundTrip runs a built app from a download folder: it installs
// itself under a temporary %LOCALAPPDATA% and relaunches from there. The
// test reads back the installed file, the shortcut and its AUMID, the
// uninstall entry, the scheme's command and the AUMID key; opens a link of
// the scheme, which starts the installed copy; then runs the uninstall
// entry's command and checks that everything is gone.
func TestInstallRoundTrip(t *testing.T) {
	root := t.TempDir()
	download := filepath.Join(root, "download")
	os.MkdirAll(download, 0o755)
	exe := filepath.Join(download, "testinst.exe")
	if out, err := exec.Command("go", "build", "-o", exe, "./internal/testinst").CombinedOutput(); err != nil {
		t.Fatalf("building the test app: %v\n%s", err, out)
	}

	var rnd [4]byte
	rand.Read(rnd[:])
	suffix := hex.EncodeToString(rnd[:])
	id, name, scheme := "dev.gonativeui.installtest"+suffix, "GnuiInstallTest"+suffix, "gnui-insttest-"+suffix
	reports := filepath.Join(root, "reports")
	os.MkdirAll(reports, 0o755)
	local := filepath.Join(root, "local")
	t.Setenv("LOCALAPPDATA", local) // after the build: the go command's cache lives there
	t.Setenv("INSTTEST_REPORT", reports)
	t.Setenv("INSTTEST_ID", id)
	t.Setenv("INSTTEST_NAME", name)
	t.Setenv("INSTTEST_SCHEME", scheme)
	dir := filepath.Join(local, "Programs", name)
	installed := filepath.Join(dir, "testinst.exe")
	lnk := shortcutPath(name)
	t.Cleanup(func() { // whatever the outcome, leave the runner's registry clean
		os.Remove(lnk)
		winreg.DeleteTree(classesRoot + scheme)
		winreg.DeleteTree(aumidRoot + id)
		winreg.DeleteTree(uninstallRoot + id)
	})

	if out, err := exec.Command(exe).CombinedOutput(); err != nil {
		t.Fatalf("the downloaded app: %v\n%s", err, out)
	}
	r := waitReport(t, reports, "installed", 10*time.Second)
	if r.IsCopy {
		t.Errorf("the downloaded copy took itself for the installed one: %+v", r)
	}
	run := waitReport(t, reports, "running", 15*time.Second)
	if !run.IsCopy || !samePath(run.Exe, installed) || !samePath(run.Installed, installed) {
		t.Errorf("the relaunched copy: %+v, want it at %s and Installed true", run, installed)
	}

	if _, err := os.Stat(installed); err != nil {
		t.Fatalf("the installed executable: %v", err)
	}
	b, err := os.ReadFile(lnk)
	if err != nil {
		t.Fatalf("the shortcut: %v", err)
	}
	u := utf16.Encode([]rune(id))
	idBytes := make([]byte, 2*len(u))
	for i, c := range u {
		idBytes[2*i], idBytes[2*i+1] = byte(c), byte(c>>8)
	}
	if !bytes.Contains(b, idBytes) {
		t.Errorf("the shortcut does not carry the AUMID %s", id)
	}
	if got := regString(t, aumidRoot+id, "DisplayName"); got != name {
		t.Errorf("AUMID DisplayName = %q", got)
	}
	un := uninstallRoot + id
	if got := regString(t, un, "UninstallString"); got != `"`+installed+`" --uninstall` {
		t.Errorf("UninstallString = %q", got)
	}
	for k, want := range map[string]string{"DisplayName": name, "Publisher": "Go Native UI tests", "DisplayVersion": "1.0.0", "InstallLocation": dir} {
		if got := regString(t, un, k); got != want {
			t.Errorf("uninstall %s = %q, want %q", k, got, want)
		}
	}
	if got := regString(t, classesRoot+scheme+`\shell\open\command`, ""); got != `"`+installed+`" "%1"` {
		t.Errorf("the scheme's command = %q", got)
	}
	if _, err := winreg.GetString(classesRoot+scheme, "URL Protocol"); err != nil {
		t.Errorf("the scheme has no URL Protocol value: %v", err)
	}

	url := scheme + ":hello"
	if err := shellOpen(url); err != nil {
		t.Fatalf("ShellExecuteW(%s): %v", url, err)
	}
	op := waitReport(t, reports, "opened", 15*time.Second)
	if !samePath(op.Exe, installed) || len(op.Args) != 1 || op.Args[0] != url {
		t.Errorf("the link started %+v", op)
	}

	cmd := regString(t, un, "UninstallString")
	if out, err := exec.Command(installed, strings.TrimPrefix(cmd, `"`+installed+`" `)).CombinedOutput(); err != nil {
		t.Fatalf("the uninstall command: %v\n%s", err, out)
	}
	waitReport(t, reports, "uninstalled", 10*time.Second)
	deadline := time.Now().Add(20 * time.Second)
	for {
		if _, err := os.Stat(dir); os.IsNotExist(err) {
			break
		}
		if time.Now().After(deadline) {
			t.Fatalf("the install folder %s is still there", dir)
		}
		time.Sleep(200 * time.Millisecond)
	}
	if _, err := os.Stat(lnk); !os.IsNotExist(err) {
		t.Errorf("the shortcut is still there: %v", err)
	}
	for _, k := range []string{un, aumidRoot + id, classesRoot + scheme} {
		if winreg.Exists(k) {
			t.Errorf(`HKCU\%s is still there`, k)
		}
	}
}

func TestInstalledIsFalseForATestBinary(t *testing.T) {
	if p, ok := Installed(); ok {
		t.Errorf("Installed() = %s, true in a test binary", p)
	}
	if err := Supported(); err != nil {
		t.Errorf("Supported() = %v", err)
	}
}
