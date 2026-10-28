package main

import (
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"time"
)

const developerIDPrefix = "Developer ID Application:"

// signingPlan is the identity a release package is signed with and what that
// identity allows.
type signingPlan struct {
	Spec      signSpec
	Real      bool // a Developer ID (or other Apple-issued) identity: timestamp, notarization possible
	Developer bool // the identity name starts with "Developer ID Application:"
}

func cmdPackage(args []string, stdout, stderr io.Writer) error {
	fs := newFlagSet("package", stderr)
	cfgPath := fs.String("c", "gnui.json", "path of the gnui.json")
	outDir := fs.String("o", "dist", "output directory")
	identity := fs.String("identity", "", `signing identity (name or SHA-1), or "-" for ad-hoc; default $GNUI_SIGN_IDENTITY, then a "Developer ID Application" identity from the keychains, then ad-hoc`)
	devCert := fs.Bool("dev-cert", false, "sign with the gnui-dev identity instead (a bundle for this Mac and the Macs that were given the same certificate)")
	noHardened := fs.Bool("no-hardened", false, "do not enable the hardened runtime")
	noTimestamp := fs.Bool("no-timestamp", false, "do not ask Apple's server for a secure timestamp")
	notarize := fs.Bool("notarize", false, "notarize and staple (needs a Developer ID identity and -notary-profile)")
	profile := fs.String("notary-profile", os.Getenv("GNUI_NOTARY_PROFILE"), "notarytool keychain profile (xcrun notarytool store-credentials); default $GNUI_NOTARY_PROFILE")
	notaryKeychain := fs.String("notary-keychain", os.Getenv("GNUI_NOTARY_KEYCHAIN"), "the keychain holding the -notary-profile, when it is not the login keychain (store-credentials --keychain); default $GNUI_NOTARY_KEYCHAIN")
	dmg := fs.Bool("dmg", false, "also build a .dmg (signed and notarized when the identity allows)")
	zipUpdate := fs.Bool("zip", false, "also write <Name>-<version>.zip, the update archive app/selfupdate installs (ditto -c -k --keepParent, after stapling)")
	tags := fs.String("tags", "", "go build -tags (gnui_release is always added)")
	ldflags := fs.String("ldflags", "", `extra go build -ldflags ("-s -w" is always added)`)
	appIn := fs.String("app", "", "sign, notarize and package this bundle, built earlier from the same gnui.json (on another Mac, say), instead of building one; it is changed in place")
	target := fs.String("target", "darwin/"+darwinArch, "platforms to package, comma-separated: darwin/arm64, windows/amd64, windows/arm64 (windows means both); Windows executables build on any host")
	winSign := fs.String("win-sign", os.Getenv("GNUI_WINDOWS_SIGN"), `the command that signs each Windows executable, {file} standing for it (gnui.json's windows.sign otherwise; default $GNUI_WINDOWS_SIGN); unsigned when none`)
	if err := parseFlags(fs, args); err != nil {
		return err
	}
	if fs.NArg() != 0 {
		return usageError{fmt.Errorf("unexpected argument %q", fs.Arg(0))}
	}
	darwin, win, err := parseTargets(*target)
	if err != nil {
		return usageError{err}
	}
	if *appIn != "" && !darwin {
		return usageError{errors.New("-app is a macOS bundle: add darwin/arm64 to -target")}
	}
	if darwin && runtime.GOOS != "darwin" {
		return errors.New("packaging and signing need macOS (-target windows/amd64,windows/arm64 builds Windows executables on any host)")
	}
	c, err := LoadConfig(*cfgPath)
	if err != nil {
		return err
	}
	if len(win) > 0 {
		if _, err := windowsVersion(c); err != nil {
			return err
		}
		if err := packageWindows(c, win, *outDir, *winSign, buildOptions{Tags: *tags, Ldflags: *ldflags, Log: stdout}, stdout); err != nil {
			return err
		}
		if !darwin {
			return nil
		}
	}
	plan, err := planSigning(*identity, *devCert)
	if err != nil {
		return err
	}
	if *notarize {
		if !plan.Developer {
			return needIdentityError{fmt.Errorf("-notarize needs a %q identity in a keychain, and none was selected (signing with %s).\n%s", developerIDPrefix, plan.Spec.Label, appleIdentityHelp)}
		}
		if *profile == "" {
			return needIdentityError{fmt.Errorf("-notarize needs notarytool credentials: pass -notary-profile <name> or set GNUI_NOTARY_PROFILE.\n%s", appleIdentityHelp)}
		}
	}

	app := filepath.Join(*outDir, c.AppFile())
	var ent string
	if *appIn != "" {
		app = *appIn
		fmt.Fprintf(stdout, "signing %s %s (%s): %s\n", c.Name, c.Version, c.ID, app)
		if err := checkBuiltApp(c, app); err != nil {
			return err
		}
		tmp, err := os.MkdirTemp("", "gnui-sign-")
		if err != nil {
			return err
		}
		defer os.RemoveAll(tmp)
		if ent, err = writeEntitlements(c, tmp); err != nil {
			return err
		}
		if err := os.MkdirAll(*outDir, 0o755); err != nil {
			return err
		}
	} else {
		fmt.Fprintf(stdout, "packaging %s %s (%s) -> %s\n", c.Name, c.Version, c.ID, app)
		var cleanup func()
		ent, cleanup, err = buildBundle(c, buildOptions{Tags: *tags, Ldflags: *ldflags, Out: app, Log: stdout})
		if err != nil {
			return err
		}
		defer cleanup()
	}

	opts := signOptions{
		Spec:         plan.Spec,
		Hardened:     !*noHardened,
		Timestamp:    plan.Real && !*noTimestamp,
		Entitlements: ent,
	}
	fmt.Fprintf(stdout, "signing with %s%s\n", plan.Spec.Label, runtimeNote(opts))
	if _, err := signBundle(app, opts); err != nil {
		return err
	}
	if err := verifyBundle(app); err != nil {
		return err
	}
	fmt.Fprintln(stdout, "verified: codesign --verify --deep --strict")
	if dr, err := designatedRequirement(app); err == nil {
		fmt.Fprintf(stdout, "designated requirement: %s\n", dr)
	}

	var skipped []string
	if *notarize {
		if err := notarizeAndStaple(app, notary{*profile, *notaryKeychain}, stdout); err != nil {
			return err
		}
		if out, err := runOut("spctl", "--assess", "--type", "execute", "-vv", app); err != nil {
			return fmt.Errorf("Gatekeeper rejects the notarized app: %w", err)
		} else {
			fmt.Fprint(stdout, strings.TrimSpace(out), "\n")
		}
	} else if plan.Developer {
		skipped = append(skipped, "notarize and staple: not requested (add -notarize -notary-profile <name>)")
	} else {
		skipped = append(skipped, "notarize and staple: needs a Developer ID Application identity and notarytool credentials")
	}

	if *zipUpdate {
		path, err := writeUpdateZip(c, app, *outDir)
		if err != nil {
			return err
		}
		fmt.Fprintf(stdout, "update zip: %s\n", path)
	}

	if *dmg {
		path, err := buildDMG(c, app, *outDir, plan, !*noTimestamp, *notarize, notary{*profile, *notaryKeychain}, stdout)
		if err != nil {
			return err
		}
		fmt.Fprintf(stdout, "dmg: %s\n", path)
		if !plan.Developer {
			skipped = append(skipped, "dmg signature and notarization: needs a Developer ID Application identity; the disk image is unsigned")
		}
	}

	fmt.Fprintf(stdout, "built %s\n", app)
	if !plan.Developer {
		fmt.Fprintf(stdout, "\nnote: signed with %s, not a Developer ID: Gatekeeper rejects this bundle on other Macs.\n", plan.Spec.Label)
	}
	for _, s := range skipped {
		fmt.Fprintf(stdout, "skipped: %s\n", s)
	}
	if len(skipped) > 0 && !plan.Developer {
		fmt.Fprintf(stdout, "\n%s\n", appleIdentityHelp)
	}
	return nil
}

const appleIdentityHelp = `To distribute outside this Mac you need your Apple Developer ID:
  1. In Xcode (Settings > Accounts) or at developer.apple.com create a "Developer ID Application" certificate and install it in your keychain.
  2. Store notarization credentials once:  xcrun notarytool store-credentials <profile> --apple-id <you> --team-id <TEAMID>
  3. gnui package -identity "Developer ID Application: Name (TEAMID)" -notarize -notary-profile <profile> -dmg`

func runtimeNote(o signOptions) string {
	var parts []string
	if o.Hardened {
		parts = append(parts, "hardened runtime")
	}
	if o.Timestamp {
		parts = append(parts, "secure timestamp")
	}
	if o.Entitlements != "" {
		parts = append(parts, "entitlements")
	}
	if len(parts) == 0 {
		return ""
	}
	return " (" + strings.Join(parts, ", ") + ")"
}

// planSigning picks the identity for a release package.
func planSigning(flagIdentity string, devCert bool) (signingPlan, error) {
	return planSigningIn(flagIdentity, devCert, "", true)
}

// planSigningIn is planSigning against one keychain (the search list when
// empty); validOnly limits the choice to identities macOS trusts, which is
// what a release needs.
func planSigningIn(flagIdentity string, devCert bool, keychain string, validOnly bool) (signingPlan, error) {
	if devCert {
		spec, err := devSignSpec()
		if err != nil {
			return signingPlan{}, err
		}
		return signingPlan{Spec: spec}, nil
	}
	id := flagIdentity
	if id == "" {
		id = os.Getenv("GNUI_SIGN_IDENTITY")
	}
	if id == "-" {
		return signingPlan{Spec: signSpec{Identity: "-", Label: "an ad-hoc signature"}}, nil
	}
	ids, err := listIdentities(keychain)
	if err != nil {
		return signingPlan{}, err
	}
	if validOnly {
		valid := ids[:0:0]
		for _, i := range ids {
			if i.Valid {
				valid = append(valid, i)
			}
		}
		ids = valid
	}
	if id == "" {
		for _, i := range ids {
			if strings.HasPrefix(i.Name, developerIDPrefix) {
				return planFor(i, keychain), nil
			}
		}
		return signingPlan{Spec: signSpec{Identity: "-", Label: "an ad-hoc signature (no Developer ID Application identity found)"}}, nil
	}
	got, err := resolveIdentity(id, ids)
	if err != nil {
		return signingPlan{}, err
	}
	return planFor(got, keychain), nil
}

// planFor classifies a resolved identity by its certificate's common name:
// only a Developer ID Application certificate can be notarized.
func planFor(i identityInfo, keychain string) signingPlan {
	return signingPlan{
		Spec:      signSpec{Identity: i.Hash, Keychain: keychain, Label: i.Name},
		Real:      true,
		Developer: strings.HasPrefix(i.Name, developerIDPrefix),
	}
}

// resolveIdentity finds the identity a -identity value names among ids, by
// SHA-1, by exact common name, or by a part of the name that matches exactly
// one identity (as codesign accepts).
func resolveIdentity(spec string, ids []identityInfo) (identityInfo, error) {
	for _, i := range ids {
		if strings.EqualFold(i.Hash, spec) {
			return i, nil
		}
	}
	for _, i := range ids {
		if i.Name == spec {
			return i, nil
		}
	}
	var part []identityInfo
	for _, i := range ids {
		if strings.Contains(i.Name, spec) {
			part = append(part, i)
		}
	}
	switch len(part) {
	case 1:
		return part[0], nil
	case 0:
		return identityInfo{}, fmt.Errorf("no valid code-signing identity matches %q (see `security find-identity -v -p codesigning`; for the development identity use -dev-cert)", spec)
	}
	names := make([]string, len(part))
	for i, p := range part {
		names[i] = fmt.Sprintf("%s %s", p.Hash, p.Name)
	}
	return identityInfo{}, fmt.Errorf("%q matches %d identities; use the SHA-1 of one:\n  %s", spec, len(part), strings.Join(names, "\n  "))
}

// notary is where notarytool finds its credentials: a stored profile, and
// the keychain it was stored in ("" for the login keychain).
type notary struct{ profile, keychain string }

// args are notarytool's options for the credentials.
func (n notary) args() []string {
	a := []string{"--keychain-profile", n.profile}
	if n.keychain != "" {
		a = append(a, "--keychain", n.keychain)
	}
	return a
}

// notarizeAndStaple submits path (an .app, zipped first, or a .dmg) to Apple's
// notary service, waits, and staples the ticket.
func notarizeAndStaple(path string, n notary, out io.Writer) error {
	submit := path
	if strings.HasSuffix(path, ".app") {
		zip := path + ".zip"
		defer os.Remove(zip)
		if _, err := runOut("ditto", "-c", "-k", "--keepParent", path, zip); err != nil {
			return err
		}
		submit = zip
	}
	fmt.Fprintf(out, "notarizing %s (this waits for Apple)\n", filepath.Base(path))
	text, err := runOut("xcrun", append(append([]string{"notarytool", "submit", submit}, n.args()...), "--wait")...)
	fmt.Fprint(out, text)
	if err != nil {
		return err
	}
	if !strings.Contains(text, "status: Accepted") {
		return fmt.Errorf("notarization of %s was not accepted; see `xcrun notarytool log <id> %s`", filepath.Base(path), strings.Join(n.args(), " "))
	}
	if _, err := runOut("xcrun", "stapler", "staple", path); err != nil {
		return err
	}
	if _, err := runOut("xcrun", "stapler", "validate", path); err != nil {
		return err
	}
	fmt.Fprintf(out, "stapled %s\n", filepath.Base(path))
	return nil
}

// writeUpdateZip writes <Name>-<version>.zip next to the app: the bundle as
// ditto -c -k --keepParent archives it (symbolic links, modes and extended
// attributes kept), the form app/selfupdate's Prepare takes.
func writeUpdateZip(c *Config, app, outDir string) (string, error) {
	zip := filepath.Join(outDir, fmt.Sprintf("%s-%s.zip", strings.ReplaceAll(c.Name, " ", ""), c.Version))
	os.Remove(zip)
	if _, err := runOut("/usr/bin/ditto", "-c", "-k", "--keepParent", app, zip); err != nil {
		return "", err
	}
	return zip, nil
}

// buildDMG writes <Name>-<version>.dmg next to the app with the app and an
// Applications link in it, signs it when the identity is a Developer ID, and
// notarizes and staples it when asked.
func buildDMG(c *Config, app, outDir string, plan signingPlan, timestamp, notarize bool, n notary, out io.Writer) (string, error) {
	stage, err := os.MkdirTemp("", "gnui-dmg-*")
	if err != nil {
		return "", err
	}
	defer os.RemoveAll(stage)
	if err := copyTree(app, filepath.Join(stage, filepath.Base(app))); err != nil {
		return "", err
	}
	if err := os.Symlink("/Applications", filepath.Join(stage, "Applications")); err != nil {
		return "", err
	}
	dmg := filepath.Join(outDir, fmt.Sprintf("%s-%s.dmg", strings.ReplaceAll(c.Name, " ", ""), c.Version))
	os.Remove(dmg)
	if _, err := hdiutil("create", "-volname", c.Name, "-srcfolder", stage, "-ov", "-format", "UDZO", dmg); err != nil {
		return "", err
	}
	if plan.Developer {
		args := []string{"--force", "--sign", plan.Spec.Identity}
		if timestamp {
			args = append(args, "--timestamp")
		}
		if _, err := runOut("codesign", append(args, dmg)...); err != nil {
			return "", err
		}
		if notarize {
			if err := notarizeAndStaple(dmg, n, out); err != nil {
				return "", err
			}
		}
	}
	return dmg, nil
}

// cmdSign signs an existing bundle, nested code first.
func cmdSign(args []string, stdout, stderr io.Writer) error {
	fs := newFlagSet("sign", stderr)
	identity := fs.String("identity", "", `identity name or SHA-1, "-" for ad-hoc; default: the gnui-dev identity`)
	adhoc := fs.Bool("adhoc", false, "sign ad-hoc")
	hardened := fs.Bool("hardened", false, "enable the hardened runtime")
	ent := fs.String("entitlements", "", "entitlements plist for the bundle")
	if err := parseFlags(fs, args); err != nil {
		return err
	}
	if fs.NArg() != 1 || !strings.HasSuffix(strings.TrimSuffix(fs.Arg(0), "/"), ".app") {
		return usageError{errors.New("give exactly one path ending in .app")}
	}
	if runtime.GOOS != "darwin" {
		return errors.New("signing needs macOS")
	}
	app := strings.TrimSuffix(fs.Arg(0), "/")
	if _, err := os.Stat(app); err != nil {
		return err
	}
	var spec signSpec
	switch {
	case *adhoc:
		spec = signSpec{Identity: "-", Label: "an ad-hoc signature"}
	case *identity != "":
		spec = signSpec{Identity: *identity, Label: *identity}
	default:
		var err error
		if spec, err = devSignSpec(); err != nil {
			return err
		}
	}
	if _, err := signBundle(app, signOptions{Spec: spec, Hardened: *hardened, Entitlements: *ent, KeepMain: true}); err != nil {
		return err
	}
	if err := verifyBundle(app); err != nil {
		return err
	}
	dr, _ := designatedRequirement(app)
	fmt.Fprintf(stdout, "signed %s with %s\ndesignated requirement: %s\n", app, spec.Label, dr)
	return nil
}

// hdiutil runs hdiutil, and again while it fails because something holds
// the image for a moment, up to 7 more times 0.5, 1, 2, then 4 s apart (about
// 20 s): EAGAIN ("Resource temporarily unavailable": the disk image service
// is busy with another image; CI saw it on a verify) and EBUSY ("Resource
// busy", error 49168: XProtect's behavior service locks a new image while it
// looks at it; CI saw it on a create, and actions/runner-images#7522 has it
// on every macOS since 13, with a retry as the cure). Any other failure is
// returned at once.
func hdiutil(args ...string) (string, error) {
	wait := 500 * time.Millisecond
	for try := 0; ; try++ {
		out, err := hdiutilRun("hdiutil", args...)
		if err == nil || try == 7 || !hdiutilBusy(err) {
			return out, err
		}
		time.Sleep(wait)
		wait = min(2*wait, 4*time.Second)
	}
}

func hdiutilBusy(err error) bool {
	m := err.Error()
	return strings.Contains(m, "Resource temporarily unavailable") || strings.Contains(m, "Resource busy")
}

// hdiutilRun runs the tool (a test replaces it).
var hdiutilRun = runOut

// checkBuiltApp refuses a bundle -app was given that is not this
// configuration's app: its identifier and version must be the gnui.json's,
// so the entitlements and the archives' names match what is signed.
func checkBuiltApp(c *Config, app string) error {
	info := filepath.Join(app, "Contents", "Info.plist")
	if _, err := os.Stat(info); err != nil {
		return fmt.Errorf("-app %s: not an app bundle: %w", app, err)
	}
	// the identifier first: another app's bundle is the mistake to name, whatever its version
	for _, kv := range [...][2]string{{"CFBundleIdentifier", c.ID}, {"CFBundleShortVersionString", c.Version}} {
		key, want := kv[0], kv[1]
		got, err := runOut("/usr/libexec/PlistBuddy", "-c", "Print :"+key, info)
		if err != nil {
			return fmt.Errorf("-app %s: reading %s: %w", app, key, err)
		}
		if got = strings.TrimSpace(got); got != want {
			return fmt.Errorf("-app %s: %s is %q, and %s says %q", app, key, got, "gnui.json", want)
		}
	}
	return nil
}
