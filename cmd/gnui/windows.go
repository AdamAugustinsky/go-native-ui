package main

import (
	"archive/zip"
	"bytes"
	"encoding/binary"
	"errors"
	"fmt"
	"image"
	"image/draw"
	"image/png"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"

	"gonativeui/internal/winres"
)

// Windows packaging (D77): gnui package -target windows/amd64,windows/arm64
// builds a GUI-subsystem executable per architecture from any host, with a
// resource object generated here (the icon, the manifest and VERSIONINFO),
// signs it when a signing command is configured, and zips it.

// WindowsConfig is gnui.json's "windows" object.
type WindowsConfig struct {
	// Publisher is the version resource's CompanyName, which app/install
	// shows in Settings' list of installed apps.
	Publisher string `json:"publisher"`
	// AUMID overrides the id as the AppUserModelID app/install registers
	// (written to the version resource as AppUserModelID).
	AUMID string `json:"aumid"`
	// Description is FileDescription (default: the display name).
	Description string `json:"description"`
	// Copyright is LegalCopyright.
	Copyright string `json:"copyright"`
	// Sign is the signing command, run once per executable after the build
	// (see signWindows); "" leaves the executables unsigned. -win-sign and
	// $GNUI_WINDOWS_SIGN override it.
	Sign string `json:"sign"`
}

// windowsArchs are the architectures gnui package builds for Windows.
var windowsArchs = map[string]bool{"amd64": true, "arm64": true}

// parseTargets splits -target ("darwin/arm64,windows/amd64") into whether
// macOS is wanted and the Windows architectures, in order.
func parseTargets(s string) (darwin bool, win []string, err error) {
	seen := map[string]bool{}
	for _, t := range strings.Split(s, ",") {
		t = strings.TrimSpace(t)
		if t == "" || seen[t] {
			continue
		}
		seen[t] = true
		goos, arch, _ := strings.Cut(t, "/")
		switch {
		case t == "darwin/"+darwinArch || t == "darwin":
			darwin = true
		case goos == "windows" && windowsArchs[arch]:
			win = append(win, arch)
		case goos == "windows" && arch == "":
			for _, a := range []string{"amd64", "arm64"} {
				if !seen["windows/"+a] {
					seen["windows/"+a] = true
					win = append(win, a)
				}
			}
		default:
			return false, nil, fmt.Errorf("-target %q: gnui packages darwin/arm64, windows/amd64 and windows/arm64", t)
		}
	}
	if !darwin && len(win) == 0 {
		return false, nil, errors.New("-target names no platform")
	}
	return darwin, win, nil
}

// windowsVersion is the four-part file version: version's three parts and
// the build number as the fourth (when the build is one number).
func windowsVersion(c *Config) ([4]uint16, error) {
	q, err := winres.ParseQuad(c.Version)
	if err != nil {
		return q, fmt.Errorf("version %q: a Windows version part must be at most 65535", c.Version)
	}
	if !strings.Contains(c.Build, ".") {
		b, err := winres.ParseQuad(c.Build)
		if err != nil {
			return q, fmt.Errorf("build %q: a Windows build number must be at most 65535", c.Build)
		}
		q[3] = b[0]
	}
	return q, nil
}

// winExeName is the executable's file name.
func winExeName(c *Config) string { return c.Executable + ".exe" }

// winFile is a file that ships beside the executable: a DLL of nativeLibs
// or its licence.
type winFile struct{ name, src string }

// windowsNativeLibs lists the files of gnui.json's nativeLibs for arch: the
// .dll files of each directory's windows-<arch> subdirectory (at least one,
// each a DLL for arch's machine), and, when the directory holds a LICENSE,
// that licence as <library>.LICENSE.txt beside each DLL (as macOS puts it in
// Contents/Resources/Licenses). The names are what the executable declares
// in its version resource (winres.FilesKey).
func windowsNativeLibs(c *Config, arch string) ([]winFile, error) {
	var files []winFile
	seen := map[string]bool{}
	add := func(f winFile) error {
		if err := winres.CheckFileName(f.name); err != nil {
			return err
		}
		if k := strings.ToLower(f.name); seen[k] {
			return fmt.Errorf("nativeLibs: two files are called %s", f.name)
		} else {
			seen[k] = true
		}
		files = append(files, f)
		return nil
	}
	for _, d := range c.NativeLibs {
		dir := c.Path(d)
		sub := filepath.Join(dir, "windows-"+arch)
		libs, _ := filepath.Glob(filepath.Join(sub, "*.dll"))
		if len(libs) == 0 {
			return nil, fmt.Errorf("native libraries: no .dll in %s (build them: go run ./tools/libghostty)", sub)
		}
		for _, l := range libs {
			pf, err := readPEFile(l)
			if err != nil {
				return nil, fmt.Errorf("native library %s: %w", l, err)
			}
			if pf.Machine != winres.MachineFor(arch) {
				return nil, fmt.Errorf("native library %s is for machine type %#x, not windows/%s", l, pf.Machine, arch)
			}
			name := filepath.Base(l)
			if err := add(winFile{name: name, src: l}); err != nil {
				return nil, err
			}
			if lic := filepath.Join(dir, "LICENSE"); isFile(lic) {
				if err := add(winFile{name: strings.TrimSuffix(name, filepath.Ext(name)) + ".LICENSE.txt", src: lic}); err != nil {
					return nil, err
				}
			}
		}
	}
	return files, nil
}

// readPEFile reads the PE headers and resources of the file at path.
func readPEFile(path string) (*winres.File, error) {
	f, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	defer f.Close()
	st, err := f.Stat()
	if err != nil {
		return nil, err
	}
	return winres.Read(f, st.Size())
}

// windowsResources are the resources linked into the executable for arch;
// files are the names that ship beside it.
func windowsResources(c *Config, arch string, files []string) ([]winres.Resource, error) {
	q, err := windowsVersion(c)
	if err != nil {
		return nil, err
	}
	w := c.Windows
	desc := w.Description
	if desc == "" {
		desc = c.ShownName()
	}
	aumid := w.AUMID
	if aumid == "" {
		aumid = c.ID
	}
	strs := []winres.KV{
		{Key: "CompanyName", Value: w.Publisher},
		{Key: "FileDescription", Value: desc},
		{Key: "FileVersion", Value: winres.QuadString(q)},
		{Key: "InternalName", Value: c.Executable},
		{Key: "LegalCopyright", Value: w.Copyright},
		{Key: "OriginalFilename", Value: winExeName(c)},
		{Key: "ProductName", Value: c.ShownName()},
		{Key: "ProductVersion", Value: c.Version},
		{Key: "AppUserModelID", Value: aumid},
		{Key: winres.FilesKey, Value: strings.Join(files, "|")},
	}
	kept := strs[:0]
	for _, kv := range strs {
		if kv.Value != "" {
			kept = append(kept, kv)
		}
	}
	v := winres.Version{File: q, Product: q, Strings: kept}
	rs := []winres.Resource{
		{Type: winres.TypeVersion, ID: 1, Lang: winres.LangEnUS, Data: v.Encode()},
		{Type: winres.TypeManifest, ID: 1, Lang: winres.LangEnUS, Data: windowsManifest(c, q)},
	}
	if c.Icon != "" {
		icons, err := windowsIcons(c.Path(c.Icon))
		if err != nil {
			return nil, err
		}
		rs = append(rs, icons...)
	}
	return rs, nil
}

// windowsManifest is the application manifest: Per-Monitor v2 DPI awareness
// (with the per-monitor fallback for Windows before 1703), Windows 10 and 11
// as supported systems, UTF-8 as the process code page, long paths, Common
// Controls 6, and asInvoker (never an elevation prompt).
func windowsManifest(c *Config, q [4]uint16) []byte {
	esc := func(s string) string {
		var b bytes.Buffer
		for _, r := range s {
			switch r {
			case '&':
				b.WriteString("&amp;")
			case '<':
				b.WriteString("&lt;")
			case '>':
				b.WriteString("&gt;")
			case '"':
				b.WriteString("&quot;")
			default:
				b.WriteRune(r)
			}
		}
		return b.String()
	}
	return []byte(`<?xml version="1.0" encoding="UTF-8" standalone="yes"?>
<assembly xmlns="urn:schemas-microsoft-com:asm.v1" manifestVersion="1.0">
  <assemblyIdentity type="win32" name="` + esc(c.ID) + `" version="` + winres.QuadString(q) + `" processorArchitecture="*"/>
  <description>` + esc(c.ShownName()) + `</description>
  <trustInfo xmlns="urn:schemas-microsoft-com:asm.v3">
    <security>
      <requestedPrivileges>
        <requestedExecutionLevel level="asInvoker" uiAccess="false"/>
      </requestedPrivileges>
    </security>
  </trustInfo>
  <compatibility xmlns="urn:schemas-microsoft-com:compatibility.v1">
    <application>
      <supportedOS Id="{8e0f7a12-bfb3-4fe8-b9a5-48fd50a15a9a}"/>
    </application>
  </compatibility>
  <application xmlns="urn:schemas-microsoft-com:asm.v3">
    <windowsSettings>
      <dpiAware xmlns="http://schemas.microsoft.com/SMI/2005/WindowsSettings">true/pm</dpiAware>
      <dpiAwareness xmlns="http://schemas.microsoft.com/SMI/2016/WindowsSettings">PerMonitorV2, PerMonitor</dpiAwareness>
      <longPathAware xmlns="http://schemas.microsoft.com/SMI/2016/WindowsSettings">true</longPathAware>
      <activeCodePage xmlns="http://schemas.microsoft.com/SMI/2019/WindowsSettings">UTF-8</activeCodePage>
    </windowsSettings>
  </application>
  <dependency>
    <dependentAssembly>
      <assemblyIdentity type="win32" name="Microsoft.Windows.Common-Controls" version="6.0.0.0" processorArchitecture="*" publicKeyToken="6595b64144ccf1df" language="*"/>
    </dependentAssembly>
  </dependency>
</assembly>
`)
}

// windowsIconSizes are the icon's sizes in pixels: the shell's list, menu,
// taskbar and tile sizes at 100–200 %, and 256 for large views.
var windowsIconSizes = []int{16, 20, 24, 32, 40, 48, 64, 256}

// windowsIcons turns the square PNG at path into RT_ICON resources (DIBs
// below 256 px, a PNG at 256, as the shell expects) and their RT_GROUP_ICON
// (ID 1, the executable's icon). Sizes above the source's are left out.
func windowsIcons(path string) ([]winres.Resource, error) {
	f, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	defer f.Close()
	src, err := png.Decode(f)
	if err != nil {
		return nil, fmt.Errorf("icon %s: %w", path, err)
	}
	b := src.Bounds()
	if b.Dx() != b.Dy() || b.Dx() < 16 {
		return nil, fmt.Errorf("icon %s must be square and at least 16 px, got %d×%d", path, b.Dx(), b.Dy())
	}
	rgba := image.NewNRGBA(image.Rect(0, 0, b.Dx(), b.Dy()))
	draw.Draw(rgba, rgba.Bounds(), src, b.Min, draw.Src)
	var rs []winres.Resource
	var group bytes.Buffer
	le := binary.LittleEndian
	var hdr [6]byte
	le.PutUint16(hdr[2:], 1) // type: icon
	group.Write(hdr[:])
	n := uint16(0)
	for _, size := range windowsIconSizes {
		if size > b.Dx() {
			continue
		}
		img := rgba
		if size != b.Dx() {
			img = resizeArea(rgba, size)
		}
		var data []byte
		if size >= 256 {
			var p bytes.Buffer
			if err := (&png.Encoder{CompressionLevel: png.BestCompression}).Encode(&p, img); err != nil {
				return nil, err
			}
			data = p.Bytes()
		} else {
			data = iconDIB(img)
		}
		n++
		rs = append(rs, winres.Resource{Type: winres.TypeIcon, ID: n, Lang: winres.LangEnUS, Data: data})
		var e [14]byte // GRPICONDIRENTRY
		if size < 256 {
			e[0], e[1] = byte(size), byte(size) // 0 means 256
		}
		le.PutUint16(e[4:], 1)  // planes
		le.PutUint16(e[6:], 32) // bits per pixel
		le.PutUint32(e[8:], uint32(len(data)))
		le.PutUint16(e[12:], n)
		group.Write(e[:])
	}
	gb := group.Bytes()
	le.PutUint16(gb[4:], n)
	return append(rs, winres.Resource{Type: winres.TypeGroupIcon, ID: 1, Lang: winres.LangEnUS, Data: gb}), nil
}

// iconDIB is an icon image as a 32-bit BITMAPINFOHEADER DIB: BGRA rows bottom
// up, the height doubled for the AND mask, which follows (all zero: the alpha
// channel decides).
func iconDIB(img *image.NRGBA) []byte {
	s := img.Bounds().Dx()
	maskRow := ((s + 31) / 32) * 4
	b := make([]byte, 40+4*s*s+maskRow*s)
	le := binary.LittleEndian
	le.PutUint32(b[0:], 40)
	le.PutUint32(b[4:], uint32(s))
	le.PutUint32(b[8:], uint32(2*s))
	le.PutUint16(b[12:], 1)
	le.PutUint16(b[14:], 32)
	le.PutUint32(b[20:], uint32(4*s*s+maskRow*s))
	p := b[40:]
	for y := 0; y < s; y++ {
		row := p[4*s*(s-1-y):]
		for x := 0; x < s; x++ {
			c := img.NRGBAAt(x, y)
			row[4*x], row[4*x+1], row[4*x+2], row[4*x+3] = c.B, c.G, c.R, c.A
		}
	}
	return b
}

// sysoName is the resource object gnui puts in the main package for a build;
// its _windows_<arch> suffix keeps it out of every other build.
func sysoName(arch string) string { return "zz_gnui_rsrc_windows_" + arch + ".syso" }

// buildWindows builds the executable for arch at out, a GUI-subsystem
// release build with its resources linked in, declaring files as the ones
// beside it.
func buildWindows(c *Config, arch, out string, files []string, o buildOptions) error {
	rs, err := windowsResources(c, arch, files)
	if err != nil {
		return err
	}
	obj, err := winres.COFF(winres.MachineFor(arch), rs)
	if err != nil {
		return err
	}
	// go build links the .syso files in the main package's folder; its
	// -overlay does not carry them, so the object is put there for the build
	// and removed after it. Another .syso there would add a second set of
	// resources.
	pkg := c.Path(c.Main)
	ents, err := os.ReadDir(pkg)
	if err != nil {
		return fmt.Errorf("main package %s: %w", pkg, err)
	}
	for _, e := range ents {
		if strings.HasSuffix(e.Name(), ".syso") && !strings.HasPrefix(e.Name(), "zz_gnui_rsrc_") {
			return fmt.Errorf("%s has a resource object of its own (%s); gnui writes the icon, manifest and version itself: remove it", pkg, e.Name())
		}
	}
	syso := filepath.Join(pkg, sysoName(arch))
	if err := os.WriteFile(syso, obj, 0o644); err != nil {
		return err
	}
	defer os.Remove(syso)

	goBin, err := goTool()
	if err != nil {
		return fmt.Errorf("go not found: put the Go toolchain on PATH or set GNUI_GO (%w)", err)
	}
	o.Ldflags = strings.TrimSpace("-H windowsgui " + o.Ldflags)
	cmd := exec.Command(goBin, buildArgs(c, out, o)...)
	cmd.Dir = c.dir
	cmd.Env = append(os.Environ(), "CGO_ENABLED=0", "GOOS=windows", "GOARCH="+arch)
	if outp, err := cmd.CombinedOutput(); err != nil {
		return fmt.Errorf("go build (windows/%s) failed:\n%s", arch, strings.TrimSpace(string(outp)))
	}
	return nil
}

// packageWindows builds, signs and zips the Windows executables:
// <out>/windows-<arch>/<Executable>.exe and <out>/<Name>-<version>-windows-<arch>.zip.
// The files of nativeLibs go beside the executable, signed with it, and into
// the zip with it, all at the zip's top.
func packageWindows(c *Config, archs []string, outDir, signCmd string, o buildOptions, stdout io.Writer) error {
	if signCmd == "" {
		signCmd = c.Windows.Sign
	}
	for _, arch := range archs {
		libs, err := windowsNativeLibs(c, arch)
		if err != nil {
			return err
		}
		names := make([]string, len(libs))
		for i, l := range libs {
			names[i] = l.name
		}
		dir := filepath.Join(outDir, "windows-"+arch)
		if err := os.MkdirAll(dir, 0o755); err != nil {
			return err
		}
		exe := filepath.Join(dir, winExeName(c))
		fmt.Fprintf(stdout, "packaging %s %s (%s) for windows/%s -> %s\n", c.Name, c.Version, c.ID, arch, exe)
		if err := buildWindows(c, arch, exe, names, o); err != nil {
			return err
		}
		paths := []string{exe}
		for _, l := range libs {
			dst := filepath.Join(dir, l.name)
			os.Remove(dst)
			if err := copyInto(dir, l.name, l.src); err != nil {
				return fmt.Errorf("native library %s: %w", l.src, err)
			}
			paths = append(paths, dst)
		}
		if signCmd != "" {
			for _, p := range paths {
				if !strings.EqualFold(filepath.Ext(p), ".txt") {
					if err := signWindows(signCmd, p, stdout); err != nil {
						return err
					}
					fmt.Fprintf(stdout, "signed %s\n", p)
				}
			}
		}
		zipPath := filepath.Join(outDir, fmt.Sprintf("%s-%s-windows-%s.zip", strings.ReplaceAll(c.Name, " ", ""), c.Version, arch))
		if err := zipFiles(zipPath, paths); err != nil {
			return err
		}
		fmt.Fprintf(stdout, "zip: %s\n", zipPath)
	}
	if signCmd == "" {
		fmt.Fprintln(stdout, "\nnote: the Windows executables are unsigned: SmartScreen warns on a first download, and app/selfupdate needs Options.Unsigned with a verified digest. Configure windows.sign (or -win-sign) to sign them.")
	}
	return nil
}

// signWindows runs the signing command on exe and checks that the file then
// carries a signature. The command is split into words (single and double
// quotes group, as in a shell, with no other expansion); in each word {file}
// becomes the executable's path and $NAME or ${NAME} the environment
// variable's value, so a secret stays in the environment, never in
// gnui.json. Examples:
//
//	signtool sign /fd SHA256 /tr http://timestamp.acs.microsoft.com /td SHA256 /dlib Azure.CodeSigning.Dlib.dll /dmdf metadata.json {file}
//	jsign --storetype TRUSTEDSIGNING --keystore weu.codesigning.azure.net --storepass ${AZURE_TOKEN} --alias account/profile --tsaurl http://timestamp.acs.microsoft.com --tsmode RFC3161 {file}
//	osslsigncode sign -pkcs12 cert.p12 -pass ${CERT_PASS} -ts http://timestamp.digicert.com -in {file} -out {file}.signed
//
// osslsigncode writes a new file: a command whose words include
// "{file}.signed" has that file moved over the executable afterwards.
func signWindows(command, exe string, stdout io.Writer) error {
	words, err := splitCommand(command)
	if err != nil {
		return fmt.Errorf("windows signing command: %w", err)
	}
	if len(words) == 0 {
		return errors.New("windows signing command is empty")
	}
	hasFile, signedCopy := false, false
	for i, w := range words {
		if strings.Contains(w, "{file}.signed") {
			signedCopy = true
		}
		if strings.Contains(w, "{file}") {
			hasFile = true
		}
		words[i] = os.Expand(strings.ReplaceAll(w, "{file}", exe), os.Getenv)
	}
	if !hasFile {
		return errors.New("windows signing command must name the executable as {file}")
	}
	cmd := exec.Command(words[0], words[1:]...)
	var out bytes.Buffer
	cmd.Stdout, cmd.Stderr = &out, &out
	if err := cmd.Run(); err != nil {
		return fmt.Errorf("signing %s with %s failed: %v\n%s", filepath.Base(exe), filepath.Base(words[0]), err, strings.TrimSpace(out.String()))
	}
	if signedCopy {
		if err := os.Rename(exe+".signed", exe); err != nil {
			return err
		}
	}
	f, err := os.Open(exe)
	if err != nil {
		return err
	}
	defer f.Close()
	st, err := f.Stat()
	if err != nil {
		return err
	}
	pf, err := winres.Read(f, st.Size())
	if err != nil {
		return err
	}
	if !pf.Signed {
		return fmt.Errorf("%s has no signature after %s ran", filepath.Base(exe), filepath.Base(words[0]))
	}
	return nil
}

// splitCommand splits s into words: spaces separate, and single or double
// quotes group (a backslash is an ordinary character, as in Windows paths).
func splitCommand(s string) ([]string, error) {
	var words []string
	var cur strings.Builder
	in, quote := false, rune(0)
	for _, r := range s {
		switch {
		case quote != 0:
			if r == quote {
				quote = 0
			} else {
				cur.WriteRune(r)
			}
		case r == '\'' || r == '"':
			quote, in = r, true
		case r == ' ' || r == '\t' || r == '\n':
			if in {
				words = append(words, cur.String())
				cur.Reset()
				in = false
			}
		default:
			cur.WriteRune(r)
			in = true
		}
	}
	if quote != 0 {
		return nil, errors.New("unterminated quote")
	}
	if in {
		words = append(words, cur.String())
	}
	return words, nil
}

// zipFiles writes a zip holding the files srcs under their base names.
func zipFiles(dst string, srcs []string) (err error) {
	out, err := os.Create(dst)
	if err != nil {
		return err
	}
	defer func() {
		if cerr := out.Close(); err == nil {
			err = cerr
		}
		if err != nil {
			os.Remove(dst)
		}
	}()
	zw := zip.NewWriter(out)
	mod := time.Now().UTC().Truncate(time.Second)
	for _, src := range srcs {
		if err := zipAdd(zw, src, mod); err != nil {
			return err
		}
	}
	return zw.Close()
}

func zipAdd(zw *zip.Writer, src string, mod time.Time) error {
	in, err := os.Open(src)
	if err != nil {
		return err
	}
	defer in.Close()
	st, err := in.Stat()
	if err != nil {
		return err
	}
	h, err := zip.FileInfoHeader(st)
	if err != nil {
		return err
	}
	h.Method = zip.Deflate
	h.Modified = mod
	w, err := zw.CreateHeader(h)
	if err != nil {
		return err
	}
	_, err = io.Copy(w, in)
	return err
}
