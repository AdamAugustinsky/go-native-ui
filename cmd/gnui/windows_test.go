package main

import (
	"archive/zip"
	"bytes"
	"encoding/binary"
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"testing"

	"gonativeui/internal/winres"
)

// fakeSignEnv makes the test binary a stand-in signing tool: it appends a
// certificate table to the executable it is given (TestMain).
const fakeSignEnv = "GNUI_TEST_FAKE_SIGN"

// fakeSign gives the PE file at path a certificate table holding junk, as a
// signing tool's would hold a PKCS#7 signature.
func fakeSign(path string) error {
	b, err := os.ReadFile(path)
	if err != nil {
		return err
	}
	le := binary.LittleEndian
	pe := int(le.Uint32(b[0x3C:]))
	opt := pe + 24
	if le.Uint16(b[opt:]) != 0x20B {
		return os.ErrInvalid
	}
	for len(b)%8 != 0 {
		b = append(b, 0)
	}
	off := len(b)
	cert := make([]byte, 16)
	le.PutUint32(cert[0:], 16)     // dwLength
	le.PutUint16(cert[4:], 0x0200) // WIN_CERT_REVISION_2_0
	le.PutUint16(cert[6:], 2)      // WIN_CERT_TYPE_PKCS_SIGNED_DATA
	b = append(b, cert...)
	le.PutUint32(b[opt+112+8*4:], uint32(off))
	le.PutUint32(b[opt+112+8*4+4:], uint32(len(cert)))
	return os.WriteFile(path, b, 0o755)
}

func readPE(t *testing.T, path string) *winres.File {
	t.Helper()
	f, err := os.Open(path)
	if err != nil {
		t.Fatal(err)
	}
	defer f.Close()
	st, err := f.Stat()
	if err != nil {
		t.Fatal(err)
	}
	pf, err := winres.Read(f, st.Size())
	if err != nil {
		t.Fatalf("%s: %v", path, err)
	}
	return pf
}

func TestParseTargets(t *testing.T) {
	for in, want := range map[string]string{
		"darwin/arm64":                "darwin",
		"windows/amd64,windows/arm64": "amd64,arm64",
		"windows":                     "amd64,arm64",
		"darwin/arm64, windows/arm64": "darwin,arm64",
	} {
		d, w, err := parseTargets(in)
		if err != nil {
			t.Fatalf("%q: %v", in, err)
		}
		got := strings.Join(w, ",")
		if d {
			got = strings.Trim("darwin,"+got, ",")
		}
		if got != want {
			t.Errorf("parseTargets(%q) = %s, want %s", in, got, want)
		}
	}
	for _, bad := range []string{"", "linux/amd64", "windows/386", "darwin/amd64"} {
		if _, _, err := parseTargets(bad); err == nil {
			t.Errorf("parseTargets(%q) accepted", bad)
		}
	}
}

func TestSplitCommand(t *testing.T) {
	got, err := splitCommand(`jsign --storepass "${TOKEN}" --alias 'a b/c' C:\x\{file}`)
	if err != nil {
		t.Fatal(err)
	}
	want := []string{"jsign", "--storepass", "${TOKEN}", "--alias", "a b/c", `C:\x\{file}`}
	if strings.Join(got, "|") != strings.Join(want, "|") {
		t.Errorf("splitCommand = %q", got)
	}
	if _, err := splitCommand(`a "b`); err == nil {
		t.Error("an unterminated quote was accepted")
	}
}

// TestPackageWindows builds the probe for both Windows architectures from
// whatever OS runs the test, signed by the stand-in tool, and reads the
// resources back from the files with our PE reader. On Windows it also runs
// the build for the runner's architecture, which reports what the manifest
// did to the process and what FindResourceW finds.
func TestPackageWindows(t *testing.T) {
	if testing.Short() {
		t.Skip("builds two executables")
	}
	out := t.TempDir()
	exe, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	t.Setenv(fakeSignEnv, "1")
	var stdout, stderr bytes.Buffer
	args := []string{"package", "-c", "testdata/winprobe/gnui.json", "-o", out, "-target", "windows",
		"-win-sign", `"` + exe + `" {file}`}
	if code := run(args, &stdout, &stderr); code != 0 {
		t.Fatalf("gnui package exited %d:\n%s\n%s", code, stdout.String(), stderr.String())
	}
	if _, err := os.Stat("testdata/winprobe/" + sysoName("amd64")); !os.IsNotExist(err) {
		t.Errorf("the resource object was left in the package: %v", err)
	}
	for arch, machine := range map[string]uint16{"amd64": winres.MachineAMD64, "arm64": winres.MachineARM64} {
		path := filepath.Join(out, "windows-"+arch, "WinProbe.exe")
		pf := readPE(t, path)
		if pf.Machine != machine || pf.Subsystem != winres.SubsystemGUI || !pf.Signed {
			t.Errorf("%s: machine %#x subsystem %d signed %v", arch, pf.Machine, pf.Subsystem, pf.Signed)
		}
		vr, ok := winres.Find(pf.Resources, winres.TypeVersion, 1)
		if !ok {
			t.Fatalf("%s: no version resource", arch)
		}
		v, err := winres.ParseVersion(vr.Data)
		if err != nil {
			t.Fatal(err)
		}
		if v.File != [4]uint16{1, 2, 3, 7} || v.String("ProductVersion") != "1.2.3" || v.String("CompanyName") != "Go Native UI tests" ||
			v.String("OriginalFilename") != "WinProbe.exe" || v.String("AppUserModelID") != "dev.gonativeui.test.winprobe" || v.String("ProductName") != "Win Probe" {
			t.Errorf("%s: version %+v", arch, v)
		}
		mr, ok := winres.Find(pf.Resources, winres.TypeManifest, 1)
		if !ok || !bytes.Contains(mr.Data, []byte("PerMonitorV2")) || !bytes.Contains(mr.Data, []byte(`level="asInvoker"`)) {
			t.Errorf("%s: manifest %q", arch, mr.Data)
		}
		gr, ok := winres.Find(pf.Resources, winres.TypeGroupIcon, 1)
		if !ok {
			t.Fatalf("%s: no icon group", arch)
		}
		n := int(binary.LittleEndian.Uint16(gr.Data[4:]))
		if n != len(windowsIconSizes) {
			t.Errorf("%s: %d icons, want %d", arch, n, len(windowsIconSizes))
		}
		for i := 0; i < n; i++ {
			e := gr.Data[6+14*i:]
			id := binary.LittleEndian.Uint16(e[12:])
			ic, ok := winres.Find(pf.Resources, winres.TypeIcon, id)
			if !ok || len(ic.Data) != int(binary.LittleEndian.Uint32(e[8:])) {
				t.Errorf("%s: icon %d missing or of the wrong size", arch, id)
			}
		}
		zr, err := zip.OpenReader(filepath.Join(out, "WinProbe-1.2.3-windows-"+arch+".zip"))
		if err != nil {
			t.Fatal(err)
		}
		if len(zr.File) != 1 || zr.File[0].Name != "WinProbe.exe" {
			t.Errorf("%s: the zip holds %v", arch, zr.File)
		}
		zr.Close()
	}

	if runtime.GOOS != "windows" {
		return
	}
	// The unsigned build of the runner's architecture, run.
	out2 := t.TempDir()
	stdout.Reset()
	if code := run([]string{"package", "-c", "testdata/winprobe/gnui.json", "-o", out2, "-target", "windows/" + runtime.GOARCH, "-win-sign", ""}, &stdout, &stderr); code != 0 {
		t.Fatalf("gnui package exited %d:\n%s\n%s", code, stdout.String(), stderr.String())
	}
	got, err := exec.Command(filepath.Join(out2, "windows-"+runtime.GOARCH, "WinProbe.exe")).Output()
	if err != nil {
		t.Fatal(err)
	}
	var r struct {
		Awareness    int
		PerMonitorV2 bool
		CodePage     int
		Resources    map[string]bool
	}
	if err := json.Unmarshal(got, &r); err != nil {
		t.Fatalf("%v: %s", err, got)
	}
	t.Logf("the probe says %s", bytes.TrimSpace(got))
	if r.Awareness != 2 || !r.PerMonitorV2 { // DPI_AWARENESS_PER_MONITOR_AWARE
		t.Errorf("DPI awareness %d (per-monitor v2 %v), want the manifest's PerMonitorV2", r.Awareness, r.PerMonitorV2)
	}
	if r.CodePage != 65001 {
		t.Errorf("code page %d, want the manifest's UTF-8 (65001)", r.CodePage)
	}
	for _, k := range []string{"manifest", "version", "groupIcon"} {
		if !r.Resources[k] {
			t.Errorf("FindResourceW found no %s", k)
		}
	}
}
