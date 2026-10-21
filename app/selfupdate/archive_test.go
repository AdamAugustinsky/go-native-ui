package selfupdate

import (
	"archive/zip"
	"bytes"
	"errors"
	"io/fs"
	"strings"
	"testing"
)

type entry struct {
	name string
	mode fs.FileMode
	body string
	size uint64 // declared size of a raw entry; 0: the body's
}

func dir(name string) entry        { return entry{name: name, mode: fs.ModeDir | 0o755} }
func file(name, body string) entry { return entry{name: name, mode: 0o644, body: body} }
func link(name, target string) entry {
	return entry{name: name, mode: fs.ModeSymlink | 0o755, body: target}
}

func mkzip(t *testing.T, entries ...entry) *bytes.Reader {
	t.Helper()
	var b bytes.Buffer
	w := zip.NewWriter(&b)
	for _, e := range entries {
		h := &zip.FileHeader{Name: e.name}
		h.SetMode(e.mode)
		if e.size != 0 {
			h.UncompressedSize64, h.CompressedSize64 = e.size, uint64(len(e.body))
			fw, err := w.CreateRaw(h)
			if err != nil {
				t.Fatal(err)
			}
			fw.Write([]byte(e.body))
			continue
		}
		fw, err := w.CreateHeader(h)
		if err != nil {
			t.Fatal(err)
		}
		fw.Write([]byte(e.body))
	}
	if err := w.Close(); err != nil {
		t.Fatal(err)
	}
	return bytes.NewReader(b.Bytes())
}

// bundle is the layout ditto -c -k --keepParent writes for a small app.
func bundle(extra ...entry) []entry {
	return append([]entry{
		dir("SUTest.app/"),
		dir("SUTest.app/Contents/"),
		dir("SUTest.app/Contents/MacOS/"),
		file("SUTest.app/Contents/MacOS/testapp", "\xcf\xfa\xed\xfe"),
		file("SUTest.app/Contents/._MacOS", "appledouble"),
		file("SUTest.app/Contents/Info.plist", "<plist/>"),
		dir("SUTest.app/Contents/Frameworks/"),
		dir("SUTest.app/Contents/Frameworks/F.framework/"),
		dir("SUTest.app/Contents/Frameworks/F.framework/Versions/"),
		dir("SUTest.app/Contents/Frameworks/F.framework/Versions/A/"),
		file("SUTest.app/Contents/Frameworks/F.framework/Versions/A/F", "lib"),
		link("SUTest.app/Contents/Frameworks/F.framework/Versions/Current", "A"),
		link("SUTest.app/Contents/Frameworks/F.framework/F", "Versions/Current/F"),
		dir("SUTest.app/Contents/Resources/"),
		file("SUTest.app/Contents/Resources/R\u00e9sum\u00e9.txt", "x"),
		file("SUTest.app/Contents/Resources/\u00dcber.txt", "y"),
	}, extra...)
}

func TestArchiveAcceptsABundle(t *testing.T) {
	r := mkzip(t, bundle()...)
	top, err := checkArchive(r, r.Size())
	if err != nil || top != "SUTest.app" {
		t.Fatalf("checkArchive = %q, %v", top, err)
	}
}

func TestArchiveRejects(t *testing.T) {
	for _, c := range []struct {
		name    string
		entries []entry
		want    string
	}{
		{"dot-dot", bundle(file("SUTest.app/../evil", "x")), `".."`},
		{"dot-dot inside", bundle(file("SUTest.app/Contents/../../evil", "x")), `".."`},
		{"absolute", bundle(file("/tmp/evil", "x")), "absolute"},
		{"backslash", bundle(file(`SUTest.app\..\evil`, "x")), "backslash"},
		{"empty component", bundle(file("SUTest.app//evil", "x")), "empty component"},
		{"control character", bundle(file("SUTest.app/a\nb", "x")), "control"},
		{"link outside", bundle(link("SUTest.app/Contents/l", "../../..")), "outside"},
		{"link absolute", bundle(link("SUTest.app/Contents/l", "/etc")), "absolute"},
		{"link to the parent folder", bundle(link("SUTest.app/Contents/l", "../../")), "outside"},
		{"through a link", bundle(link("SUTest.app/Contents/l", "Resources"), file("SUTest.app/Contents/l/x", "x")), "through the link"},
		{"through a link, other case", bundle(link("SUTest.app/Contents/l", "Resources"), file("SUTest.app/Contents/L/x", "x")), "through the link"},
		{"through a link, Kelvin sign", bundle(link("SUTest.app/Contents/k", "Resources"), file("SUTest.app/Contents/\u212A/x", "x")), "through the link"},
		{"through a link, other Unicode form", bundle(link("SUTest.app/Contents/\u00e9", "Resources"), file("SUTest.app/Contents/e\u0301/x", "x")), "through the link"},
		{"file over a link", bundle(link("SUTest.app/Contents/l", "Resources"), file("SUTest.app/Contents/L", "x")), "shares its name"},
		{"top is a link", []entry{link("SUTest.app", "x")}, "top-level item is a link"},
		{"two tops", bundle(dir("Other.app/")), "more than one item"},
		{"two tops by case", bundle(dir("sutest.app/")), "more than one item"},
		{"not an app", []entry{dir("SUTest/"), file("SUTest/x", "x")}, "not an .app"},
		{"no top entry", []entry{file("SUTest.app/Contents/Info.plist", "x")}, "no entry"},
		{"device", bundle(entry{name: "SUTest.app/dev", mode: fs.ModeDevice | 0o644}), "not a file, folder or link"},
		{"named pipe", bundle(entry{name: "SUTest.app/p", mode: fs.ModeNamedPipe | 0o644}), "not a file, folder or link"},
		{"duplicate", bundle(file("SUTest.app/Contents/Info.plist", "again")), "twice"},
		{"too large", bundle(entry{name: "SUTest.app/big", mode: 0o644, body: "x", size: maxTotalSize + 1}), "more than"},
		{"folder named like a file", bundle(entry{name: "SUTest.app/x", mode: fs.ModeDir | 0o755}), "disagree"},
	} {
		t.Run(c.name, func(t *testing.T) {
			r := mkzip(t, c.entries...)
			_, err := checkArchive(r, r.Size())
			if !errors.Is(err, ErrRejected) || !strings.Contains(err.Error(), c.want) {
				t.Fatalf("checkArchive = %v, want ErrRejected mentioning %q", err, c.want)
			}
		})
	}
}

func TestArchiveRejectsJunk(t *testing.T) {
	r := bytes.NewReader([]byte("not a zip"))
	if _, err := checkArchive(r, r.Size()); !errors.Is(err, ErrRejected) {
		t.Fatalf("checkArchive = %v", err)
	}
}

func TestFoldKey(t *testing.T) {
	for _, c := range []struct{ a, b string }{
		{"A.app/Contents", "a.app/contents"},
		{"x/\u212A", "x/k"},
		{"x/\u017Fo", "x/so"},
		{"x/\u00e9", "x/e\u0301"}, // any two non-ASCII components match
		{"x/a;b", "x/a\u037Eb"},
	} {
		if foldKey(c.a) != foldKey(c.b) {
			t.Errorf("foldKey(%q) != foldKey(%q)", c.a, c.b)
		}
	}
	if foldKey("x/a") == foldKey("x/b") || foldKey("x/\u00e9") == foldKey("x/e") {
		t.Error("foldKey matches names that differ")
	}
}
