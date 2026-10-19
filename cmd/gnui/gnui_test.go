package main

import (
	"bytes"
	"debug/macho"
	"encoding/binary"
	"encoding/json"
	"image"
	"image/color"
	"image/png"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
)

const helloConfig = "testdata/hello/gnui.json"

func loadHello(t *testing.T) *Config {
	t.Helper()
	c, err := LoadConfig(helloConfig)
	if err != nil {
		t.Fatal(err)
	}
	return c
}

func TestConfigDefaultsAndDevVariant(t *testing.T) {
	dir := t.TempDir()
	p := filepath.Join(dir, "gnui.json")
	os.WriteFile(p, []byte(`{"id":"dev.example.my-app","name":"My App","loginAgents":[{"name":"helper","arguments":["--x"]}]}`), 0o644)
	c, err := LoadConfig(p)
	if err != nil {
		t.Fatal(err)
	}
	if c.Main != "." || c.Executable != "MyApp" || c.Version != "1.0.0" || c.Build != "1" || c.MinimumSystem != "14.0" {
		t.Errorf("defaults wrong: %+v", c)
	}
	if got := c.BundleID(true); got != "dev.example.my-app.dev" {
		t.Errorf("dev bundle id = %q", got)
	}
	if got := c.BundleID(false); got != "dev.example.my-app" {
		t.Errorf("release bundle id = %q", got)
	}
	// the login-agent label follows the bundle id in use
	if got := c.AgentLabel(c.LoginAgents[0], true); got != "dev.example.my-app.dev.helper" {
		t.Errorf("dev agent label = %q", got)
	}
	if got := c.AgentLabel(c.LoginAgents[0], false); got != "dev.example.my-app.helper" {
		t.Errorf("agent label = %q", got)
	}
}

func TestConfigRejects(t *testing.T) {
	cases := map[string]string{
		"unknown key":        `{"id":"dev.a.b","name":"A","colour":"red"}`,
		"no id":              `{"name":"A"}`,
		"single-word id":     `{"id":"app","name":"A"}`,
		"slash in name":      `{"id":"dev.a.b","name":"A/B"}`,
		"bad version":        `{"id":"dev.a.b","name":"A","version":"1.x"}`,
		"bad category":       `{"id":"dev.a.b","name":"A","category":"productivity"}`,
		"framework suffix":   `{"id":"dev.a.b","name":"A","frameworks":["x.so"]}`,
		"bad scheme":         `{"id":"dev.a.b","name":"A","urlSchemes":["Has Space"]}`,
		"doc type no ext":    `{"id":"dev.a.b","name":"A","documentTypes":[{"name":"x"}]}`,
		"doc type bad role":  `{"id":"dev.a.b","name":"A","documentTypes":[{"name":"x","extensions":["a"],"role":"Boss"}]}`,
		"usage key":          `{"id":"dev.a.b","name":"A","usage":{"Camera":"x"}}`,
		"usage empty":        `{"id":"dev.a.b","name":"A","usage":{"NSCameraUsageDescription":" "}}`,
		"agent name":         `{"id":"dev.a.b","name":"A","loginAgents":[{"name":"a b"}]}`,
		"agent duplicate":    `{"id":"dev.a.b","name":"A","loginAgents":[{"name":"a"},{"name":"a"}]}`,
		"resource escapes":   `{"id":"dev.a.b","name":"A","resources":[{"from":"x","to":"../y"}]}`,
		"trailing json":      `{"id":"dev.a.b","name":"A"} {}`,
		"executable is path": `{"id":"dev.a.b","name":"A","executable":"a/b"}`,
	}
	for name, body := range cases {
		p := filepath.Join(t.TempDir(), "gnui.json")
		os.WriteFile(p, []byte(body), 0o644)
		if _, err := LoadConfig(p); err == nil {
			t.Errorf("%s: accepted", name)
		}
	}
}

func TestPlistEncoding(t *testing.T) {
	data, err := encodePlist(map[string]any{
		"s":    "a<b>&c",
		"n":    float64(7),
		"r":    1.5,
		"b":    false,
		"list": []any{"x", true},
		"dict": map[string]any{},
	})
	if err != nil {
		t.Fatal(err)
	}
	if runtime.GOOS == "darwin" {
		got := plistToJSON(t, data)
		if got["s"] != "a<b>&c" || got["n"] != float64(7) || got["r"] != 1.5 || got["b"] != false {
			t.Errorf("round trip through plutil: %v", got)
		}
		if l := got["list"].([]any); len(l) != 2 || l[0] != "x" || l[1] != true {
			t.Errorf("list: %v", l)
		}
	}
	if _, err := encodePlist(map[string]any{"bad": nil}); err == nil {
		t.Error("JSON null accepted in a plist")
	}
}

func TestInfoPlistContent(t *testing.T) {
	if runtime.GOOS != "darwin" {
		t.Skip("reads the plist back with plutil")
	}
	c := loadHello(t)
	for _, dev := range []bool{false, true} {
		data, err := encodePlist(infoPlist(c, dev))
		if err != nil {
			t.Fatal(err)
		}
		m := plistToJSON(t, data)
		wantID := c.ID
		if dev {
			wantID += ".dev"
		}
		if m["CFBundleIdentifier"] != wantID || m["CFBundleExecutable"] != "HelloApp" || m["CFBundleName"] != "Hello App" ||
			m["CFBundleShortVersionString"] != "2.3.4" || m["CFBundleVersion"] != "42" || m["LSMinimumSystemVersion"] != "13.0" ||
			m["CFBundleIconFile"] != "AppIcon" || m["NSMicrophoneUsageDescription"] != "Hello listens for a test." ||
			m["LSUIElement"] != true || m["GnuiTestNumber"] != float64(7) || m["NSHighResolutionCapable"] != true {
			t.Errorf("dev=%v: Info.plist = %v", dev, m)
		}
		urls := m["CFBundleURLTypes"].([]any)[0].(map[string]any)
		if urls["CFBundleURLSchemes"].([]any)[0] != "gnui-hello" {
			t.Errorf("URL types: %v", urls)
		}
		doc := m["CFBundleDocumentTypes"].([]any)[0].(map[string]any)
		if doc["CFBundleTypeRole"] != "Editor" || doc["CFBundleTypeExtensions"].([]any)[0] != "hello" || doc["LSItemContentTypes"].([]any)[0] != "dev.gonativeui.hello" {
			t.Errorf("document types: %v", doc)
		}

		ag, err := encodePlist(agentPlist(c, c.LoginAgents[0], dev))
		if err != nil {
			t.Fatal(err)
		}
		a := plistToJSON(t, ag)
		if a["Label"] != wantID+".agent" || a["BundleProgram"] != "Contents/MacOS/HelloApp" ||
			a["AssociatedBundleIdentifiers"].([]any)[0] != wantID {
			t.Errorf("dev=%v: agent plist = %v", dev, a)
		}
		args := a["ProgramArguments"].([]any)
		if len(args) != 2 || args[0] != "HelloApp" || args[1] != "--start-hidden" {
			t.Errorf("agent arguments: %v", args)
		}
	}
}

func TestICNS(t *testing.T) {
	// A 256 px image: left half opaque red, right half transparent.
	src := image.NewNRGBA(image.Rect(0, 0, 256, 256))
	for y := 0; y < 256; y++ {
		for x := 0; x < 128; x++ {
			src.SetNRGBA(x, y, color.NRGBA{200, 10, 20, 255})
		}
	}
	data, err := icnsFromImage(src)
	if err != nil {
		t.Fatal(err)
	}
	if string(data[:4]) != "icns" || int(binary.BigEndian.Uint32(data[4:8])) != len(data) {
		t.Fatalf("bad icns header")
	}
	sizes := map[string]int{}
	for off := 8; off < len(data); {
		typ := string(data[off : off+4])
		n := int(binary.BigEndian.Uint32(data[off+4 : off+8]))
		img, err := png.Decode(bytes.NewReader(data[off+8 : off+n]))
		if err != nil {
			t.Fatalf("%s: %v", typ, err)
		}
		w := img.Bounds().Dx()
		sizes[typ] = w
		if img.Bounds().Dy() != w {
			t.Errorf("%s not square", typ)
		}
		// colour and alpha survive downscaling: opaque, un-darkened red on the left, clear on the right
		l := color.NRGBAModel.Convert(img.At(w/4, w/2)).(color.NRGBA)
		r := color.NRGBAModel.Convert(img.At(3*w/4, w/2)).(color.NRGBA)
		if l.A != 255 || abs(int(l.R)-200) > 1 || abs(int(l.G)-10) > 1 || r.A != 0 {
			t.Errorf("%s (%d px): left %v right %v", typ, w, l, r)
		}
		off += n
	}
	for typ, w := range map[string]int{"icp4": 16, "icp5": 32, "ic11": 32, "ic12": 64, "ic07": 128, "ic13": 256, "ic08": 256} {
		if sizes[typ] != w {
			t.Errorf("entry %s is %d px, want %d", typ, sizes[typ], w)
		}
	}
	if _, big := sizes["ic09"]; big {
		t.Error("512 px entry made from a 256 px source")
	}
	if _, err := icnsFromImage(image.NewNRGBA(image.Rect(0, 0, 64, 32))); err == nil {
		t.Error("non-square icon accepted")
	}
}

func abs(x int) int {
	if x < 0 {
		return -x
	}
	return x
}

// thinMachO returns a minimal valid 64-bit Mach-O header for cpu.
func thinMachO(cpu macho.Cpu, sub uint32, payload byte) []byte {
	b := make([]byte, 32+64)
	binary.LittleEndian.PutUint32(b[0:], 0xfeedfacf)
	binary.LittleEndian.PutUint32(b[4:], uint32(cpu))
	binary.LittleEndian.PutUint32(b[8:], sub)
	binary.LittleEndian.PutUint32(b[12:], 2) // MH_EXECUTE
	for i := 32; i < len(b); i++ {
		b[i] = payload
	}
	return b
}

func TestUniversalBinary(t *testing.T) {
	arm := thinMachO(macho.CpuArm64, 0, 0xA1)
	amd := thinMachO(macho.CpuAmd64, 3, 0xB2)
	fat, err := makeUniversal([]machoSlice{{arm}, {amd}})
	if err != nil {
		t.Fatal(err)
	}
	ff, err := macho.NewFatFile(bytes.NewReader(fat))
	if err != nil {
		t.Fatal(err)
	}
	if len(ff.Arches) != 2 {
		t.Fatalf("%d arches", len(ff.Arches))
	}
	want := map[macho.Cpu][]byte{macho.CpuArm64: arm, macho.CpuAmd64: amd}
	hdr := binary.BigEndian.Uint32(fat[8+20*0+8:]) // offset of first slice
	if hdr%(1<<14) != 0 {
		t.Errorf("arm64 slice at %d is not 16 KiB aligned", hdr)
	}
	for i, a := range ff.Arches {
		off := binary.BigEndian.Uint32(fat[8+20*i+8:])
		size := binary.BigEndian.Uint32(fat[8+20*i+12:])
		if !bytes.Equal(fat[off:off+size], want[a.Cpu]) {
			t.Errorf("slice %v differs from its input", a.Cpu)
		}
	}
	if _, err := makeUniversal([]machoSlice{{arm}, {arm}}); err == nil {
		t.Error("two slices of one cpu accepted")
	}
	if _, err := makeUniversal([]machoSlice{{[]byte("not macho")}}); err == nil {
		t.Error("garbage accepted")
	}
}

func TestNestedCodeOrder(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("symbolic links")
	}
	app := filepath.Join(t.TempDir(), "A.app")
	bin := thinMachO(macho.CpuArm64, 0, 1)
	put := func(rel string, data []byte) string {
		p := filepath.Join(app, rel)
		os.MkdirAll(filepath.Dir(p), 0o755)
		os.WriteFile(p, data, 0o755)
		return p
	}
	exe := put("Contents/MacOS/A", bin)
	helper := put("Contents/MacOS/helper", bin)
	lib := put("Contents/Frameworks/libx.dylib", bin)
	fwBin := put("Contents/Frameworks/F.framework/Versions/A/F", bin)
	put("Contents/Frameworks/F.framework/Versions/A/Resources/Info.plist", []byte("<plist/>"))
	put("Contents/Resources/data.txt", []byte("not code"))
	os.Symlink("Versions/A/F", filepath.Join(app, "Contents/Frameworks/F.framework/F"))
	os.Symlink("A", filepath.Join(app, "Contents/Frameworks/F.framework/Versions/Current"))
	fw := filepath.Join(app, "Contents/Frameworks/F.framework")

	got, err := nestedCode(app, exe)
	if err != nil {
		t.Fatal(err)
	}
	idx := map[string]int{}
	for i, p := range got {
		idx[p] = i
	}
	for _, p := range []string{helper, lib, fwBin, fw} {
		if _, ok := idx[p]; !ok {
			t.Errorf("%s missing from %v", p, got)
		}
	}
	if len(got) != 4 {
		t.Errorf("got %d items: %v", len(got), got)
	}
	if idx[fwBin] > idx[fw] {
		t.Error("the framework is signed before the code inside it")
	}
	if _, ok := idx[exe]; ok {
		t.Error("main executable listed as nested")
	}
}

func TestPlanSigningExplicitIdentity(t *testing.T) {
	p, err := planSigning("-", false)
	if err != nil || !p.Spec.adhoc() || p.Real || p.Developer {
		t.Errorf("ad-hoc plan: %+v %v", p, err)
	}
	p, _ = planSigning("Developer ID Application: Someone (ABCDE12345)", false)
	if !p.Real || !p.Developer {
		t.Errorf("Developer ID plan: %+v", p)
	}
	p, _ = planSigning("Apple Development: Someone (ABCDE12345)", false)
	if !p.Real || p.Developer {
		t.Errorf("non-distribution identity must not allow notarization: %+v", p)
	}
}

func TestUsageErrors(t *testing.T) {
	var out, errb bytes.Buffer
	if code := run(nil, &out, &errb); code != 2 {
		t.Errorf("no command: exit %d", code)
	}
	if code := run([]string{"frobnicate"}, &out, &errb); code != 2 {
		t.Errorf("unknown command: exit %d", code)
	}
	if code := run([]string{"package", "-arch", "ppc"}, &out, &errb); code != 2 {
		t.Errorf("bad -arch: exit %d", code)
	}
	if code := run([]string{"help"}, &out, &errb); code != 0 || !strings.Contains(out.String(), "gnui package") {
		t.Errorf("help: exit %d", code)
	}
}

func plistToJSON(t *testing.T, plist []byte) map[string]any {
	t.Helper()
	f := filepath.Join(t.TempDir(), "x.plist")
	if err := os.WriteFile(f, plist, 0o644); err != nil {
		t.Fatal(err)
	}
	out, err := runOut("plutil", "-convert", "json", "-o", "-", f)
	if err != nil {
		t.Fatalf("plutil rejects the generated property list: %v", err)
	}
	var m map[string]any
	if err := json.Unmarshal([]byte(out), &m); err != nil {
		t.Fatal(err)
	}
	return m
}
