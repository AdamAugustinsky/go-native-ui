package img

import (
	"image"
	"image/color"
	"image/png"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
)

// xdgTree makes a data directory with a MIME database and icon themes, and
// points the XDG variables at it (and at nothing else), returning its root.
//
// The icons are the PNGs of the theme "hicolor" in 16, 48 and 128 pixels
// (and 32 at scale 2) for text-x-generic, folder and application-x-generic,
// the SVG-only x-office-document (no PNG), and a theme "Fancy", the GTK
// setting, that inherits hicolor and has its own 48 pixel application-pdf.
func xdgTree(t testing.TB) string {
	t.Helper()
	root := t.TempDir()
	write := func(rel, s string) {
		p := filepath.Join(root, rel)
		if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(p, []byte(s), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	// Each file's colour tells where it came from.
	icon := func(rel string, side int) {
		m := image.NewNRGBA(image.Rect(0, 0, side, side))
		c := color.NRGBA{uint8(side), uint8(len(rel)), 200, 255}
		for y := side / 8; y < side-side/8; y++ {
			for x := side / 8; x < side-side/8; x++ {
				m.SetNRGBA(x, y, c)
			}
		}
		p := filepath.Join(root, rel)
		if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
			t.Fatal(err)
		}
		f, err := os.Create(p)
		if err != nil {
			t.Fatal(err)
		}
		defer f.Close()
		if err := png.Encode(f, m); err != nil {
			t.Fatal(err)
		}
	}
	write("share/mime/globs2", `# a comment
50:text/plain:*.txt
50:application/pdf:*.pdf
50:application/x-tar:*.tar
50:application/gzip:*.gz
60:application/x-compressed-tar:*.tar.gz
50:text/x-makefile:Makefile
50:image/png:*.PNG:cs
50:text/x-csrc:*.[ch]
50:application/msword:*.doc
10:text/x-wrong:*.pdf
`)
	write("share/mime/generic-icons", "text/plain:text-x-generic\napplication/pdf:x-office-document\ninode/directory:folder\napplication/octet-stream:application-x-generic\napplication/msword:x-office-document\n")
	write("share/mime/subclasses", "text/x-makefile text/plain\napplication/x-compressed-tar application/x-tar\n")
	write("share/icons/hicolor/index.theme", `[Icon Theme]
Name=Hicolor
Directories=16x16/mimetypes,48x48/mimetypes,128x128/mimetypes,32x32@2/mimetypes,scalable/mimetypes

[16x16/mimetypes]
Size=16
Type=Fixed

[48x48/mimetypes]
Size=48
Type=Fixed

[128x128/mimetypes]
Size=128
Type=Threshold
Threshold=8

[32x32@2/mimetypes]
Size=32
Scale=2
Type=Fixed

[scalable/mimetypes]
Size=64
MinSize=8
MaxSize=512
Type=Scalable
`)
	for _, name := range []string{"text-x-generic", "folder", "application-x-generic"} {
		for _, d := range []struct {
			dir  string
			side int
		}{{"16x16", 16}, {"48x48", 48}, {"128x128", 128}} {
			icon("share/icons/hicolor/"+d.dir+"/mimetypes/"+name+".png", d.side)
		}
	}
	icon("share/icons/hicolor/32x32@2/mimetypes/folder.png", 64)
	write("share/icons/hicolor/scalable/mimetypes/x-office-document.svg", "<svg/>")
	write("share/icons/Fancy/index.theme", "[Icon Theme]\nName=Fancy\nInherits=hicolor\nDirectories=48x48/mimetypes\n\n[48x48/mimetypes]\nSize=48\nType=Fixed\n")
	icon("share/icons/Fancy/48x48/mimetypes/application-pdf.png", 48)
	write("config/gtk-3.0/settings.ini", "[Settings]\ngtk-icon-theme-name=Fancy\n")
	t.Setenv("HOME", filepath.Join(root, "home"))
	t.Setenv("XDG_DATA_HOME", filepath.Join(root, "datahome"))
	t.Setenv("XDG_DATA_DIRS", filepath.Join(root, "share"))
	t.Setenv("XDG_CONFIG_HOME", filepath.Join(root, "config"))
	return root
}

func TestXDGMimeAndIconNames(t *testing.T) {
	xdgTree(t)
	db := xdgSystem()
	for name, want := range map[string]string{
		"a.txt":       "text/plain",
		"A.TXT":       "text/plain", // globs are case-insensitive unless flagged
		"x.pdf":       "application/pdf",
		"x.tar.gz":    "application/x-compressed-tar", // the longest suffix
		"x.gz":        "application/gzip",
		"Makefile":    "text/x-makefile",
		"makefile":    "text/x-makefile", // a literal name without the "cs" flag matches in any case
		"a.PNG":       "image/png",
		"a.png":       "", // flagged cs: the case matters
		"main.c":      "", // other glob shapes are not matched
		"noextension": "",
		".txt":        "text/plain",
		"a.txt.bk":    "",
	} {
		got := db.mimeFor(name)
		if got != want {
			t.Errorf("mimeFor(%q) = %q, want %q", name, got, want)
		}
	}
	want := []string{"text-x-makefile", "text-plain", "text-x-generic"}
	if got := db.iconNames("text/x-makefile"); !reflect.DeepEqual(got, want) {
		t.Errorf("iconNames(text/x-makefile) = %v, want %v", got, want)
	}
	want = []string{"application-x-compressed-tar", "application-x-tar", "application-x-generic"}
	if got := db.iconNames("application/x-compressed-tar"); !reflect.DeepEqual(got, want) {
		t.Errorf("iconNames(tar.gz) = %v, want %v", got, want)
	}
}

func TestXDGLookup(t *testing.T) {
	root := xdgTree(t)
	db := xdgSystem()
	icons := filepath.Join(root, "share", "icons")
	file := filepath.Join(root, "notes.txt")
	makefile := filepath.Join(root, "Makefile")
	for _, f := range []string{file, makefile} {
		if err := os.WriteFile(f, []byte("x"), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	for _, tc := range []struct {
		what string
		src  Source
		size int
		want string // path under the icons directory; "" for an error
		code uint8
	}{
		// 16, 48, 128 and 32@2 (64 px, folder only): the nearest, the larger on a tie.
		{"txt at 16", ExtensionIcon("txt"), 16, "hicolor/16x16/mimetypes/text-x-generic.png", errNone},
		{"txt at 20", ExtensionIcon(".txt"), 20, "hicolor/16x16/mimetypes/text-x-generic.png", errNone},
		{"txt at 32", ExtensionIcon("txt"), 32, "hicolor/48x48/mimetypes/text-x-generic.png", errNone}, // 16 and 48 are both 16 away
		{"txt at 64", ExtensionIcon("TXT"), 64, "hicolor/48x48/mimetypes/text-x-generic.png", errNone},
		{"txt at 100", ExtensionIcon("txt"), 100, "hicolor/128x128/mimetypes/text-x-generic.png", errNone},
		{"txt at 512", ExtensionIcon("txt"), 512, "hicolor/128x128/mimetypes/text-x-generic.png", errNone},
		{"folder at 64: the 2× directory is exact", FolderIcon(), 64, "hicolor/32x32@2/mimetypes/folder.png", errNone},
		{"public.folder", TypeIcon("public.folder"), 48, "hicolor/48x48/mimetypes/folder.png", errNone},
		{"a directory", FileIcon(root), 128, "hicolor/128x128/mimetypes/folder.png", errNone},
		{"a file", FileIcon(file), 16, "hicolor/16x16/mimetypes/text-x-generic.png", errNone},
		{"a subclass falls back to its parent's icon", FileIcon(makefile), 16, "hicolor/16x16/mimetypes/text-x-generic.png", errNone},
		{"an icon that is only SVG falls back to the media's", ExtensionIcon("doc"), 48, "hicolor/48x48/mimetypes/application-x-generic.png", errNone},
		{"unknown extension: the generic icon", ExtensionIcon("zzz"), 48, "hicolor/48x48/mimetypes/application-x-generic.png", errNone},
		{"the theme's own icon over hicolor's", ExtensionIcon("pdf"), 16, "Fancy/48x48/mimetypes/application-pdf.png", errNone},
		{"a UTI is Apple's", TypeIcon("com.adobe.pdf"), 48, "", errUnsupported},
		{"no extension", ExtensionIcon(""), 48, "", errUnsupported},
		{"a missing file", FileIcon(filepath.Join(root, "missing")), 48, "", errRead},
	} {
		got, code := db.iconPNG(tc.src, tc.size)
		if code != tc.code {
			t.Errorf("%s: code %d, want %d (%s)", tc.what, code, tc.code, got)
			continue
		}
		if tc.want != "" && filepath.ToSlash(got) != filepath.ToSlash(filepath.Join(icons, tc.want)) {
			t.Errorf("%s: %s, want %s", tc.what, strings.TrimPrefix(filepath.ToSlash(got), filepath.ToSlash(icons)+"/"), tc.want)
		}
	}
}

// TestXDGWithoutDatabase: a system with no MIME database or no theme has no
// icons for files, and says so.
func TestXDGWithoutDatabase(t *testing.T) {
	root := t.TempDir()
	t.Setenv("HOME", root)
	t.Setenv("XDG_DATA_HOME", filepath.Join(root, "a"))
	t.Setenv("XDG_DATA_DIRS", filepath.Join(root, "b"))
	t.Setenv("XDG_CONFIG_HOME", filepath.Join(root, "c"))
	db := xdgSystem()
	if _, code := db.iconPNG(ExtensionIcon("txt"), 48); code != errUnsupported {
		t.Errorf("no MIME database: code %d, want unsupported", code)
	}
	if _, code := db.iconPNG(FolderIcon(), 48); code != errUnsupported {
		t.Errorf("no theme: code %d, want unsupported", code)
	}
}
