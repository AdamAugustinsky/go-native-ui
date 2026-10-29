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

// standInDLL is the smallest PE32+ file our reader and the stand-in signer
// take, for machine: what a nativeLibs DLL looks like to gnui package, which
// copies, signs and zips it and checks its machine type.
func standInDLL(machine uint16) []byte {
	le := binary.LittleEndian
	b := make([]byte, 64+24+240)
	b[0], b[1] = 'M', 'Z'
	le.PutUint32(b[0x3C:], 64)
	copy(b[64:], "PE\x00\x00")
	le.PutUint16(b[68:], machine)
	le.PutUint16(b[64+20:], 240)    // SizeOfOptionalHeader
	le.PutUint16(b[64+22:], 0x2022) // executable image, large addresses, DLL
	opt := b[64+24:]
	le.PutUint16(opt[0:], 0x20B)
	le.PutUint16(opt[68:], 2) // GUI subsystem
	le.PutUint32(opt[108:], 16)
	return b
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

// winprobeWithLibs writes a gnui.json for the probe that adds a nativeLibs
// directory of stand-in DLLs, one per Windows architecture, and a LICENSE;
// it returns the config's path. The config sits beside the probe's (its
// main package is the folder), and is removed after the test.
func winprobeWithLibs(t *testing.T) string {
	t.Helper()
	libs := t.TempDir()
	for arch, m := range map[string]uint16{"amd64": winres.MachineAMD64, "arm64": winres.MachineARM64} {
		os.MkdirAll(filepath.Join(libs, "windows-"+arch), 0o755)
		if err := os.WriteFile(filepath.Join(libs, "windows-"+arch, "probe-lib.dll"), standInDLL(m), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	if err := os.WriteFile(filepath.Join(libs, "LICENSE"), []byte("the stand-in library's licence\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	b, err := os.ReadFile("testdata/winprobe/gnui.json")
	if err != nil {
		t.Fatal(err)
	}
	var cfg map[string]any
	if err := json.Unmarshal(b, &cfg); err != nil {
		t.Fatal(err)
	}
	cfg["nativeLibs"] = []string{libs}
	b, _ = json.Marshal(cfg)
	f, err := os.CreateTemp("testdata/winprobe", "gnui-libs-*.json")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { os.Remove(f.Name()) })
	f.Write(b)
	f.Close()
	return f.Name()
}

// TestPackageWindows builds the probe for both Windows architectures from
// whatever OS runs the test, with a nativeLibs directory of stand-in DLLs,
// signed by the stand-in tool, and reads the resources back from the files
// with our PE reader; the DLL and its licence are beside the executable and
// in the zip, declared in the version resource, and the DLL is signed too.
// On Windows it also runs the build for the runner's architecture, which
// reports what the manifest did to the process and what FindResourceW
// finds.
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
	cfg := winprobeWithLibs(t)
	var stdout, stderr bytes.Buffer
	args := []string{"package", "-c", cfg, "-o", out, "-target", "windows",
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
		if got := v.String(winres.FilesKey); got != "probe-lib.dll|probe-lib.LICENSE.txt" {
			t.Errorf("%s: %s = %q", arch, winres.FilesKey, got)
		}
		if lib := readPE(t, filepath.Join(out, "windows-"+arch, "probe-lib.dll")); lib.Machine != machine || !lib.Signed {
			t.Errorf("%s: the DLL beside the executable: machine %#x signed %v", arch, lib.Machine, lib.Signed)
		}
		if b, err := os.ReadFile(filepath.Join(out, "windows-"+arch, "probe-lib.LICENSE.txt")); err != nil || !bytes.Contains(b, []byte("licence")) {
			t.Errorf("%s: the licence beside the executable: %q, %v", arch, b, err)
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
		var names []string
		for _, f := range zr.File {
			names = append(names, f.Name)
		}
		if strings.Join(names, " ") != "WinProbe.exe probe-lib.dll probe-lib.LICENSE.txt" {
			t.Errorf("%s: the zip holds %q", arch, names)
		}
		zr.Close()
	}

	// A DLL of the other architecture is refused.
	var c struct{ NativeLibs []string }
	b, _ := os.ReadFile(cfg)
	json.Unmarshal(b, &c)
	os.WriteFile(filepath.Join(c.NativeLibs[0], "windows-arm64", "probe-lib.dll"), standInDLL(winres.MachineAMD64), 0o644)
	stdout.Reset()
	stderr.Reset()
	if code := run([]string{"package", "-c", cfg, "-o", t.TempDir(), "-target", "windows/arm64", "-win-sign", ""}, &stdout, &stderr); code == 0 || !strings.Contains(stderr.String(), "machine type") {
		t.Errorf("an amd64 DLL for windows/arm64: exit %d\n%s", code, stderr.String())
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
