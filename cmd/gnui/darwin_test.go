package main

import (
	"bytes"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"sync"
	"testing"
)

// These tests build, sign and launch real bundles with the macOS tools. The
// gnui-dev identity they use lives in a temporary keychain
// (GNUI_DEV_KEYCHAIN), never in the user's.

var (
	testKeychainOnce sync.Once
	testKeychainErr  error
	testKeychainDir  string

	addedToSearchList bool
)

func TestMain(m *testing.M) {
	code := m.Run()
	if addedToSearchList {
		removeFromSearchList()
	}
	if testKeychainDir != "" {
		if kc := filepath.Join(testKeychainDir, "gnui-dev-test.keychain-db"); runtime.GOOS == "darwin" {
			runOut("security", "delete-keychain", kc)
		}
		os.RemoveAll(testKeychainDir)
	}
	os.Exit(code)
}

// needMac skips unless the macOS signing tools exist, and points
// GNUI_DEV_KEYCHAIN at a temporary keychain holding a fresh gnui-dev identity.
func needMac(t *testing.T) {
	t.Helper()
	if runtime.GOOS != "darwin" {
		t.Skip("needs macOS")
	}
	testKeychainOnce.Do(func() {
		testKeychainDir, testKeychainErr = os.MkdirTemp("", "gnui-test-kc-")
		if testKeychainErr != nil {
			return
		}
		testKeychainDir, _ = filepath.EvalSymlinks(testKeychainDir)
		os.Setenv("GNUI_DEV_KEYCHAIN", filepath.Join(testKeychainDir, "gnui-dev-test.keychain-db"))
		if _, testKeychainErr = setupDevIdentity(func(string, ...any) {}); testKeychainErr != nil {
			return
		}
		// macOS 15 (the CI runners) finds the signing key only in keychains on
		// the search list; newer releases do not need it. Add it only if
		// signing fails without, and take it out again in TestMain.
		if signRoundTrip() != nil {
			if testKeychainErr = addToSearchList(); testKeychainErr == nil {
				addedToSearchList = true
			}
		}
	})
	if testKeychainErr != nil {
		t.Fatalf("creating the test identity: %v", testKeychainErr)
	}
}

// gnui runs a command line in-process and returns its status and output.
func gnui(t *testing.T, args ...string) (code int, stdout, stderr string) {
	t.Helper()
	var o, e bytes.Buffer
	code = run(args, &o, &e)
	return code, o.String(), e.String()
}

func mustGnui(t *testing.T, args ...string) string {
	t.Helper()
	code, o, e := gnui(t, args...)
	if code != 0 {
		t.Fatalf("gnui %s: exit %d\nstdout:\n%s\nstderr:\n%s", strings.Join(args, " "), code, o, e)
	}
	return o
}

func codesignInfo(t *testing.T, path string) string {
	t.Helper()
	out, err := runOut("codesign", "-dvvv", "--entitlements", "-", path)
	if err != nil {
		t.Fatal(err)
	}
	return out
}

func TestDevIdentityIsReusedNotRecreated(t *testing.T) {
	needMac(t)
	id1, err := findDevIdentity()
	if err != nil || id1 == nil {
		t.Fatalf("identity: %v %v", id1, err)
	}
	created, err := setupDevIdentity(func(string, ...any) {})
	if err != nil || created {
		t.Fatalf("second setup created=%v err=%v", created, err)
	}
	id2, _ := findDevIdentity()
	if id2.Hash != id1.Hash {
		t.Error("the identity changed")
	}
	if err := signRoundTrip(); err != nil {
		t.Fatal(err)
	}
	if code, _, _ := gnui(t, "doctor", "-c", filepath.Join(t.TempDir(), "none.json")); code == 0 {
		t.Error("doctor accepted a missing -c file")
	}
	code, out, errs := gnui(t, "doctor")
	if code != 0 || !strings.Contains(out, id1.Hash) {
		t.Errorf("doctor: exit %d\n%s\n%s", code, out, errs)
	}
}

func TestPackageHelloBundle(t *testing.T) {
	needMac(t)
	out := t.TempDir()
	log := mustGnui(t, "package", "-c", helloConfig, "-o", out, "-dev-cert", "-tags", "")
	app := filepath.Join(out, "Hello App.app")
	if !strings.Contains(log, "designated requirement") {
		t.Errorf("no requirement reported:\n%s", log)
	}

	// structure
	exe := filepath.Join(app, "Contents", "MacOS", "HelloApp")
	for _, p := range []string{
		filepath.Join(app, "Contents", "Info.plist"),
		filepath.Join(app, "Contents", "Resources", "AppIcon.icns"),
		filepath.Join(app, "Contents", "Resources", "data", "data.txt"),
		filepath.Join(app, "Contents", "Library", "LaunchAgents", "dev.gonativeui.test.hello.agent.plist"),
		filepath.Join(app, "Contents", "_CodeSignature", "CodeResources"),
		exe,
	} {
		if _, err := os.Stat(p); err != nil {
			t.Errorf("bundle is missing %v", err)
		}
	}
	archs, err := runOut("lipo", "-archs", exe)
	if err != nil || !strings.Contains(archs, "arm64") || !strings.Contains(archs, "x86_64") {
		t.Errorf("executable is not universal: %q %v", archs, err)
	}
	if _, err := runOut("iconutil", "-c", "iconset", filepath.Join(app, "Contents", "Resources", "AppIcon.icns"), "-o", filepath.Join(t.TempDir(), "i.iconset")); err != nil {
		t.Errorf("the icon is not a valid icns: %v", err)
	}

	// the property list was generated for the release id
	plist, _ := os.ReadFile(filepath.Join(app, "Contents", "Info.plist"))
	if m := plistToJSON(t, plist); m["CFBundleIdentifier"] != "dev.gonativeui.test.hello" {
		t.Errorf("bundle id %v", m["CFBundleIdentifier"])
	}

	// signature
	if _, err := runOut("codesign", "--verify", "--deep", "--strict", app); err != nil {
		t.Fatal(err)
	}
	info := codesignInfo(t, app)
	for _, want := range []string{"Authority=gnui-dev", "flags=0x10000(runtime)", "com.apple.security.cs.disable-library-validation", "Identifier=dev.gonativeui.test.hello"} {
		if !strings.Contains(info, want) {
			t.Errorf("codesign output lacks %q:\n%s", want, info)
		}
	}
	if _, err := runOut(exe); err != nil {
		t.Errorf("the packaged executable does not run: %v", err)
	}
}

func TestPackageWithoutAppleIdentity(t *testing.T) {
	needMac(t)
	out := t.TempDir()
	// -identity - is ad-hoc: no Developer ID, whatever this machine's keychains hold.
	code, so, se := gnui(t, "package", "-c", helloConfig, "-o", out, "-arch", runtime.GOARCH, "-identity", "-", "-notarize", "-notary-profile", "x")
	if code != 3 || !strings.Contains(se, "Developer ID Application") {
		t.Errorf("-notarize without an identity: exit %d\n%s\n%s", code, so, se)
	}
	if _, err := os.Stat(filepath.Join(out, "Hello App.app")); err == nil {
		t.Error("a bundle was built before the missing identity was reported")
	}

	so = mustGnui(t, "package", "-c", helloConfig, "-o", out, "-arch", runtime.GOARCH, "-identity", "-", "-dmg")
	for _, want := range []string{"skipped: notarize", "dmg signature and notarization"} {
		if !strings.Contains(so, want) {
			t.Errorf("output does not report %q:\n%s", want, so)
		}
	}
	app := filepath.Join(out, "Hello App.app")
	if _, err := runOut("codesign", "--verify", "--deep", "--strict", app); err != nil {
		t.Fatal(err)
	}
	dmg := filepath.Join(out, "HelloApp-2.3.4.dmg")
	if _, err := runOut("hdiutil", "verify", dmg); err != nil {
		t.Errorf("the dmg does not verify: %v", err)
	}
}

// TestRunKeepsDesignatedRequirement is the point of the gnui-dev identity: a
// rebuild with a different binary has the same designated requirement, which
// is what macOS stores for a privacy, Keychain or notification grant, while
// an ad-hoc build's requirement is its code hash and changes.
func TestRunKeepsDesignatedRequirement(t *testing.T) {
	needMac(t)
	dir := t.TempDir()
	build := func(n string, flags ...string) (app, dr string) {
		args := append([]string{"run", "-c", helloConfig, "-o", dir, "-ldflags", "-X main.build=" + n, "-env", "GNUI_TEST_MARK=m" + n}, flags...)
		args = append(args, "--", "one", "two")
		mustGnui(t, args...)
		saved := filepath.Join(t.TempDir(), n+".app")
		if _, err := runOut("cp", "-R", filepath.Join(dir, "Hello App.app"), saved); err != nil {
			t.Fatal(err)
		}
		dr, err := designatedRequirement(saved)
		if err != nil {
			t.Fatal(err)
		}
		return saved, dr
	}
	app1, dr1 := build("1")
	app2, dr2 := build("2")
	if dr1 != dr2 {
		t.Errorf("designated requirement changed between rebuilds:\n%s\n%s", dr1, dr2)
	}
	id, _ := findDevIdentity()
	if !strings.Contains(dr1, `identifier "dev.gonativeui.test.hello.dev"`) || !strings.Contains(strings.ToUpper(dr1), id.Hash) {
		t.Errorf("requirement does not pin the .dev bundle id and the gnui-dev certificate: %s", dr1)
	}
	b1, _ := os.ReadFile(filepath.Join(app1, "Contents", "MacOS", "HelloApp"))
	b2, _ := os.ReadFile(filepath.Join(app2, "Contents", "MacOS", "HelloApp"))
	if bytes.Equal(b1, b2) {
		t.Fatal("the rebuild produced the same binary; the test proves nothing")
	}
	// the second build satisfies the requirement recorded for the first
	if _, err := runOut("codesign", "--verify", "-R="+dr1, app2); err != nil {
		t.Errorf("rebuilt app does not satisfy the earlier requirement: %v", err)
	}

	// launched through LaunchServices with arguments and environment, output streamed to the logs
	logOut, _ := os.ReadFile(filepath.Join(dir, "Hello App.out.log"))
	if !strings.Contains(string(logOut), "hello build=2 args=one,two env=m2") {
		t.Errorf("app output not captured: %q", logOut)
	}
	logErr, _ := os.ReadFile(filepath.Join(dir, "Hello App.err.log"))
	if !strings.Contains(string(logErr), "hello on stderr") {
		t.Errorf("stderr not captured: %q", logErr)
	}

	// the development bundle: .dev id and a .dev login-agent label that targets its own executable
	plist, err := os.ReadFile(filepath.Join(app2, "Contents", "Library", "LaunchAgents", "dev.gonativeui.test.hello.dev.agent.plist"))
	if err != nil {
		t.Fatal(err)
	}
	if a := plistToJSON(t, plist); a["Label"] != "dev.gonativeui.test.hello.dev.agent" || a["AssociatedBundleIdentifiers"].([]any)[0] != "dev.gonativeui.test.hello.dev" {
		t.Errorf("agent plist: %v", a)
	}

	// control: ad-hoc builds do not keep it
	_, adhoc1 := build("3", "-adhoc")
	_, adhoc2 := build("4", "-adhoc")
	if adhoc1 == adhoc2 || !strings.Contains(adhoc1, "cdhash") {
		t.Errorf("ad-hoc requirements: %q and %q", adhoc1, adhoc2)
	}
}

func TestRunNeedsDevIdentity(t *testing.T) {
	needMac(t)
	t.Setenv("GNUI_DEV_KEYCHAIN", filepath.Join(t.TempDir(), "absent.keychain-db"))
	code, _, se := gnui(t, "run", "-c", helloConfig, "-o", t.TempDir())
	if code != 1 || !strings.Contains(se, "doctor -setup-dev-cert") {
		t.Errorf("exit %d: %s", code, se)
	}
}

// TestNestedFrameworksSignedInsideOut builds a bundle with a framework (with
// symbolic links) and a bare dylib and checks that strict deep verification
// passes, which it only does when everything inside was signed first.
func TestNestedFrameworksSignedInsideOut(t *testing.T) {
	needMac(t)
	tmp := t.TempDir()
	mach := func(p string) {
		os.MkdirAll(filepath.Dir(p), 0o755)
		if err := copyFile("/usr/bin/true", p, 0o755); err != nil {
			t.Fatal(err)
		}
	}
	fw := filepath.Join(tmp, "Fake.framework")
	mach(filepath.Join(fw, "Versions", "A", "Fake"))
	os.MkdirAll(filepath.Join(fw, "Versions", "A", "Resources"), 0o755)
	os.WriteFile(filepath.Join(fw, "Versions", "A", "Resources", "Info.plist"), []byte(`<?xml version="1.0" encoding="UTF-8"?>
<!DOCTYPE plist PUBLIC "-//Apple//DTD PLIST 1.0//EN" "http://www.apple.com/DTDs/PropertyList-1.0.dtd">
<plist version="1.0"><dict><key>CFBundleExecutable</key><string>Fake</string><key>CFBundleIdentifier</key><string>dev.gonativeui.test.fake</string><key>CFBundlePackageType</key><string>FMWK</string></dict></plist>
`), 0o644)
	os.Symlink("A", filepath.Join(fw, "Versions", "Current"))
	os.Symlink("Versions/Current/Fake", filepath.Join(fw, "Fake"))
	os.Symlink("Versions/Current/Resources", filepath.Join(fw, "Resources"))
	dylib := filepath.Join(tmp, "libfake.dylib")
	mach(dylib)

	c := loadHello(t)
	c.Frameworks = []string{fw, dylib}
	app := filepath.Join(tmp, "out", "Hello App.app")
	ent, cleanup, err := buildBundle(c, buildOptions{Archs: []string{runtime.GOARCH}, Out: app})
	if err != nil {
		t.Fatal(err)
	}
	defer cleanup()
	spec, err := devSignSpec()
	if err != nil {
		t.Fatal(err)
	}
	nested, err := signBundle(app, signOptions{Spec: spec, Hardened: true, Entitlements: ent})
	if err != nil {
		t.Fatal(err)
	}
	if len(nested) != 3 { // the framework's binary, the framework, the dylib
		t.Errorf("signed %d nested items: %v", len(nested), nested)
	}
	if err := verifyBundle(app); err != nil {
		t.Fatal(err)
	}
	for _, p := range []string{
		filepath.Join(app, "Contents", "Frameworks", "libfake.dylib"),
		filepath.Join(app, "Contents", "Frameworks", "Fake.framework"),
	} {
		if info := codesignInfo(t, p); !strings.Contains(info, "Authority=gnui-dev") {
			t.Errorf("%s not signed by gnui-dev:\n%s", p, info)
		}
	}
}

// TestPackageGallery packages the real gallery application as a universal,
// signed bundle.
func TestPackageGallery(t *testing.T) {
	if testing.Short() {
		t.Skip("builds the gallery twice")
	}
	needMac(t)
	out := t.TempDir()
	mustGnui(t, "package", "-c", filepath.Join("..", "gallery", "gnui.json"), "-o", out, "-dev-cert")
	app := filepath.Join(out, "Gallery.app")
	exe := filepath.Join(app, "Contents", "MacOS", "Gallery")
	if archs, err := runOut("lipo", "-archs", exe); err != nil || !strings.Contains(archs, "arm64") || !strings.Contains(archs, "x86_64") {
		t.Errorf("not universal: %q %v", archs, err)
	}
	if _, err := runOut("codesign", "--verify", "--deep", "--strict", app); err != nil {
		t.Fatal(err)
	}
	plist, _ := os.ReadFile(filepath.Join(app, "Contents", "Info.plist"))
	if m := plistToJSON(t, plist); m["CFBundleIdentifier"] != "dev.gonativeui.gallery" || m["CFBundleExecutable"] != "Gallery" {
		t.Errorf("Info.plist: %v", m)
	}
}
