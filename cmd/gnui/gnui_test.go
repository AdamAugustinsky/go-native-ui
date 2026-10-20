package main

import (
	"bytes"
	"debug/macho"
	"encoding/binary"
	"encoding/json"
	"errors"
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
		"trailing brace":     `{"id":"dev.a.b","name":"A"}}`,
		"trailing bracket":   `{"id":"dev.a.b","name":"A"}]`,
		"trailing garbage":   `{"id":"dev.a.b","name":"A"} x`,
		"trailing comma":     `{"id":"dev.a.b","name":"A"},`,
		"resource dot":       `{"id":"dev.a.b","name":"A","resources":[{"from":"."}]}`,
		"resource dotdot":    `{"id":"dev.a.b","name":"A","resources":[{"from":"res/.."}]}`,
		"resource root":      `{"id":"dev.a.b","name":"A","resources":[{"from":"/"}]}`,
		"resource to up":     `{"id":"dev.a.b","name":"A","resources":[{"from":"x","to":"a/../../b"}]}`,
		"resource to dot":    `{"id":"dev.a.b","name":"A","resources":[{"from":"x","to":"."}]}`,
		"resource to abs":    `{"id":"dev.a.b","name":"A","resources":[{"from":"x","to":"/etc"}]}`,
		"uti public export":  `{"id":"dev.a.b","name":"A","exportedTypes":[{"identifier":"public.foo","extensions":["foo"]}]}`,
		"uti no tags":        `{"id":"dev.a.b","name":"A","exportedTypes":[{"identifier":"dev.a.foo"}]}`,
		"uti bad id":         `{"id":"dev.a.b","name":"A","exportedTypes":[{"identifier":"foo","extensions":["foo"]}]}`,
		"uti dot extension":  `{"id":"dev.a.b","name":"A","importedTypes":[{"identifier":"dev.a.foo","extensions":[".foo"]}]}`,
		"uti duplicate":      `{"id":"dev.a.b","name":"A","exportedTypes":[{"identifier":"dev.a.foo","extensions":["a"]},{"identifier":"dev.a.foo","extensions":["b"]}]}`,
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

func TestConfigAcceptsTrailingWhitespaceOnly(t *testing.T) {
	p := filepath.Join(t.TempDir(), "gnui.json")
	os.WriteFile(p, []byte("{\"id\":\"dev.a.b\",\"name\":\"A\"}\n\n  \t\n"), 0o644)
	if _, err := LoadConfig(p); err != nil {
		t.Error(err)
	}
}

func TestConfigWarnsOfUndeclaredType(t *testing.T) {
	p := filepath.Join(t.TempDir(), "gnui.json")
	os.WriteFile(p, []byte(`{"id":"dev.a.b","name":"A","documentTypes":[{"name":"x","extensions":["x"],"utis":["dev.a.x","public.plain-text"]}]}`), 0o644)
	c, err := LoadConfig(p)
	if err != nil {
		t.Fatal(err)
	}
	if w := c.Warnings(); len(w) != 1 || !strings.Contains(w[0], "dev.a.x") {
		t.Errorf("warnings: %v", w)
	}
	c.ExportedTypes = []TypeDeclaration{{Identifier: "dev.a.x", Extensions: []string{"x"}}}
	if w := c.Warnings(); len(w) != 0 {
		t.Errorf("declared type still warned: %v", w)
	}
	if h := loadHello(t); len(h.Warnings()) != 0 {
		t.Errorf("the hello example warns: %v", h.Warnings())
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
		ut := m["UTExportedTypeDeclarations"].([]any)[0].(map[string]any)
		tags := ut["UTTypeTagSpecification"].(map[string]any)
		if ut["UTTypeIdentifier"] != "dev.gonativeui.hello" || ut["UTTypeConformsTo"].([]any)[0] != "public.data" ||
			ut["UTTypeDescription"] != "Hello file" || tags["public.filename-extension"].([]any)[0] != "hello" {
			t.Errorf("exported type declaration: %v", ut)
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

func TestResolveIdentity(t *testing.T) {
	ids := []identityInfo{
		{Hash: "AAAA000000000000000000000000000000000001", Name: "Developer ID Application: Someone (ABCDE12345)", Valid: true},
		{Hash: "BBBB000000000000000000000000000000000002", Name: "Apple Development: Someone (ABCDE12345)", Valid: true},
		{Hash: "CCCC000000000000000000000000000000000003", Name: "Apple Development: Other (ZZZZZ99999)", Valid: true},
	}
	// by SHA-1, in any case: classified from the certificate's name, not from the spelling of the argument
	got, err := resolveIdentity("aaaa000000000000000000000000000000000001", ids)
	if err != nil || got.Name != ids[0].Name {
		t.Fatalf("by hash: %v %v", got, err)
	}
	if p := planFor(got, ""); !p.Developer || !p.Real || p.Spec.Identity != got.Hash {
		t.Errorf("a Developer ID chosen by SHA-1 is not classified as one: %+v", p)
	}
	got, _ = resolveIdentity(ids[1].Hash, ids)
	if p := planFor(got, ""); p.Developer || !p.Real {
		t.Errorf("an Apple Development identity must not allow notarization: %+v", p)
	}
	// by exact name and by a unique part of it
	if got, err = resolveIdentity("Developer ID Application: Someone (ABCDE12345)", ids); err != nil || got.Hash != ids[0].Hash {
		t.Errorf("by name: %v %v", got, err)
	}
	if got, err = resolveIdentity("Developer ID", ids); err != nil || got.Hash != ids[0].Hash {
		t.Errorf("by part: %v %v", got, err)
	}
	if _, err = resolveIdentity("Apple Development", ids); err == nil || !strings.Contains(err.Error(), ids[1].Hash) {
		t.Errorf("ambiguous name: %v", err)
	}
	if _, err = resolveIdentity("Nobody", ids); err == nil {
		t.Error("unknown identity accepted")
	}
	if p, err := planSigning("-", false); err != nil || !p.Spec.adhoc() || p.Real || p.Developer {
		t.Errorf("ad-hoc plan: %+v %v", p, err)
	}
}

func TestResourceDestinationCannotEscapeThroughCopiedLinks(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("symbolic links")
	}
	tmp := t.TempDir()
	outside := filepath.Join(tmp, "outside")
	os.MkdirAll(outside, 0o755)
	victim := filepath.Join(outside, "victim")
	os.WriteFile(victim, []byte("keep"), 0o644)
	root := filepath.Join(tmp, "Resources")
	os.MkdirAll(root, 0o755)

	// a resource directory with a link to somewhere else on disk is refused
	evil := filepath.Join(tmp, "evil")
	os.MkdirAll(evil, 0o755)
	os.Symlink(outside, filepath.Join(evil, "link"))
	if err := copyInto(root, "assets", evil); err == nil {
		t.Fatal("an absolute symbolic link out of the tree was copied")
	}
	// so is a relative one that climbs out, and a chain that ends outside
	os.Remove(filepath.Join(evil, "link"))
	os.Symlink("../outside", filepath.Join(evil, "up"))
	if err := copyInto(root, "assets", evil); err == nil {
		t.Fatal("a symbolic link climbing out of the tree was copied")
	}
	os.Remove(filepath.Join(evil, "up"))
	os.Symlink("../outside", filepath.Join(evil, "hop"))
	os.Symlink("hop", filepath.Join(evil, "hop2"))
	os.Remove(filepath.Join(evil, "hop"))
	os.Symlink("../outside", filepath.Join(evil, "hop"))
	if err := copyInto(root, "assets", evil); err == nil {
		t.Fatal("a chain of links ending outside the tree was copied")
	}

	// a link that stays inside is kept, but no later copy may go through it
	good := filepath.Join(tmp, "good")
	os.MkdirAll(filepath.Join(good, "real"), 0o755)
	os.WriteFile(filepath.Join(good, "real", "victim"), []byte("keep"), 0o644)
	os.Symlink("real", filepath.Join(good, "link"))
	if err := copyInto(root, "assets", good); err != nil {
		t.Fatal(err)
	}
	if l, err := os.Readlink(filepath.Join(root, "assets", "link")); err != nil || l != "real" {
		t.Fatalf("the internal link was not kept: %q %v", l, err)
	}
	other := filepath.Join(tmp, "other.txt")
	os.WriteFile(other, []byte("new"), 0o644)
	if err := copyInto(root, "assets/link/victim", other); err == nil {
		t.Fatal("a destination through a symbolic link was accepted")
	}
	if b, _ := os.ReadFile(filepath.Join(root, "assets", "real", "victim")); string(b) != "keep" {
		t.Errorf("the file behind the link was changed or deleted: %q", b)
	}
	if b, _ := os.ReadFile(victim); string(b) != "keep" {
		t.Errorf("a file outside the bundle was changed: %q", b)
	}
	// destinations that are not clean relative paths
	for _, rel := range []string{"", ".", "..", "../x", "a/../../x", "/abs"} {
		if err := copyInto(root, rel, other); err == nil {
			t.Errorf("destination %q accepted", rel)
		}
	}
	if _, err := os.Stat(root); err != nil {
		t.Errorf("the Resources directory itself is gone: %v", err)
	}
}

func TestFrameworkLinksAreKept(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("symbolic links")
	}
	tmp := t.TempDir()
	fw := filepath.Join(tmp, "F.framework")
	os.MkdirAll(filepath.Join(fw, "Versions", "A", "Resources"), 0o755)
	os.WriteFile(filepath.Join(fw, "Versions", "A", "F"), []byte("x"), 0o755)
	os.Symlink("A", filepath.Join(fw, "Versions", "Current"))
	os.Symlink("Versions/Current/F", filepath.Join(fw, "F"))
	os.Symlink("Versions/Current/Resources", filepath.Join(fw, "Resources"))
	root := filepath.Join(tmp, "Frameworks")
	os.MkdirAll(root, 0o755)
	if err := copyInto(root, "F.framework", fw); err != nil {
		t.Fatal(err)
	}
	for link, want := range map[string]string{"Versions/Current": "A", "F": "Versions/Current/F", "Resources": "Versions/Current/Resources"} {
		if got, err := os.Readlink(filepath.Join(root, "F.framework", link)); err != nil || got != want {
			t.Errorf("%s -> %q (%v), want %q", link, got, err, want)
		}
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

// hdiutil tries again while the disk image service is busy (EAGAIN), and
// only then.
func TestHdiutilRetriesOnlyABusyService(t *testing.T) {
	defer func(f func(string, ...string) (string, error)) { hdiutilRun = f }(hdiutilRun)
	busy := errors.New("hdiutil verify x.dmg: hdiutil: verify failed - Resource temporarily unavailable")
	var calls int
	hdiutilRun = func(string, ...string) (string, error) {
		if calls++; calls < 3 {
			return "", busy
		}
		return "ok", nil
	}
	if out, err := hdiutil("verify", "x.dmg"); err != nil || out != "ok" || calls != 3 {
		t.Errorf("busy twice: %q %v after %d calls, want ok after 3", out, err, calls)
	}
	// EBUSY: XProtect holding a new image (CI, on a create).
	calls = 0
	busy = errors.New("hdiutil create -ov x.dmg: hdiutil: create failed - Resource busy")
	if out, err := hdiutil("create", "-ov", "x.dmg"); err != nil || out != "ok" || calls != 3 {
		t.Errorf("EBUSY twice: %q %v after %d calls, want ok after 3", out, err, calls)
	}
	calls = 0
	hdiutilRun = func(string, ...string) (string, error) {
		calls++
		return "", errors.New("hdiutil: verify failed - image not recognized")
	}
	if _, err := hdiutil("verify", "x.dmg"); err == nil || calls != 1 {
		t.Errorf("another failure: %v after %d calls, want it after 1", err, calls)
	}
}
