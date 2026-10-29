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
	"runtime"
	"strings"
	"syscall"
	"testing"
	"time"
	"unicode/utf16"
	"unsafe"

	"gonativeui/internal/win32"
	"gonativeui/internal/winreg"
	"gonativeui/internal/winres"
)

// libName is the stand-in for a nativeLibs DLL that the test app declares in
// its version resource (winres.FilesKey), as gnui package writes it.
const libName = "testinst-lib.dll"

// buildTestApp builds internal/testinst at exe with a version resource that
// declares libName as a file beside it.
func buildTestApp(t *testing.T, exe string) {
	t.Helper()
	v := winres.Version{File: [4]uint16{1, 0, 0, 0}, Product: [4]uint16{1, 0, 0, 0},
		Strings: []winres.KV{{Key: winres.FilesKey, Value: libName}}}
	obj, err := winres.COFF(winres.MachineFor(runtime.GOARCH), []winres.Resource{
		{Type: winres.TypeVersion, ID: 1, Lang: winres.LangEnUS, Data: v.Encode()}})
	if err != nil {
		t.Fatal(err)
	}
	syso := filepath.Join("internal", "testinst", "zz_gnui_rsrc_windows_"+runtime.GOARCH+".syso")
	if err := os.WriteFile(syso, obj, 0o644); err != nil {
		t.Fatal(err)
	}
	defer os.Remove(syso)
	if out, err := exec.Command("go", "build", "-o", exe, "./internal/testinst").CombinedOutput(); err != nil {
		t.Fatalf("building the test app: %v\n%s", err, out)
	}
}

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
// itself under a temporary %LOCALAPPDATA% and relaunches from there. The app
// declares a DLL beside it (a nativeLibs file): without the DLL the install
// is refused; with it, it is installed beside the executable. The test reads
// back the installed files, the shortcut and its AUMID, the uninstall entry,
// the scheme's command and the AUMID key; opens a link of the scheme, which
// starts the installed copy; then runs the uninstall entry's command and
// checks that everything is gone, the DLL with it.
func TestInstallRoundTrip(t *testing.T) {
	root := t.TempDir()
	download := filepath.Join(root, "download")
	os.MkdirAll(download, 0o755)
	exe := filepath.Join(download, "testinst.exe")
	buildTestApp(t, exe)

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

	// Without the DLL it declares, the app does not install.
	if out, err := exec.Command(exe).CombinedOutput(); err != nil {
		t.Fatalf("the downloaded app: %v\n%s", err, out)
	}
	m, _ := filepath.Glob(filepath.Join(reports, "*-installed.json"))
	var refused report
	if len(m) == 1 {
		b, _ := os.ReadFile(m[0])
		json.Unmarshal(b, &refused)
		os.Remove(m[0])
	}
	if !strings.Contains(refused.Err, libName) {
		t.Errorf("an install without %s: %+v", libName, refused)
	}
	if _, err := os.Stat(dir); !os.IsNotExist(err) {
		t.Errorf("the refused install made %s: %v", dir, err)
	}
	lib := []byte("a stand-in DLL")
	if err := os.WriteFile(filepath.Join(download, libName), lib, 0o644); err != nil {
		t.Fatal(err)
	}

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
	if b, err := os.ReadFile(filepath.Join(dir, libName)); err != nil || !bytes.Equal(b, lib) {
		t.Errorf("the installed %s: %q, %v", libName, b, err)
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
