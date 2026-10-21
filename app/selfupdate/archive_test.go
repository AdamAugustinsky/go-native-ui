package selfupdate

import (
	"archive/zip"
	"bytes"
	"errors"
	"fmt"
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

// The code points HFS+ skips when it compares names, one by one (measured on a
// real volume by TestFoldKeyIgnoresWhatHFSPlusIgnores on macOS).
var hfsIgnored = []rune{
	0x200C, 0x200D, 0x200E, 0x200F, // zero-width non-joiner and joiner, left-to-right and right-to-left marks
	0x202A, 0x202B, 0x202C, 0x202D, 0x202E, // embeddings and overrides
	0x206A, 0x206B, 0x206C, 0x206D, 0x206E, 0x206F, // deprecated format characters
	0xFEFF, // byte order mark
}

// Look-alikes and neighbours that HFS+ does not skip: they must still tell
// names apart, or the key would match things the volume keeps distinct.
var hfsNotIgnored = []rune{0x200B, 0x2060, 0x00AD, 0x180E, 0x034F, 0x202F, 0x2065, 0xFFFE}

func TestFoldKeyDropsWhatHFSPlusIgnores(t *testing.T) {
	for _, r := range hfsIgnored {
		c := string(r)
		for _, p := range []struct{ where, a, b string }{
			{"at the end", "a" + c, "a"},
			{"in the middle", "a" + c + "b", "ab"},
			{"at the start", c + "a", "a"},
			{"alone", c, ""},
			{"several", c + "a" + c + c + "b" + c, "ab"},
			{"after a non-ASCII letter", "\u00e9" + c, "\u00e9"},
			{"in a folder name", "x/a" + c + "/y", "x/a/y"},
			{"upper case", "A" + c, "a"},
		} {
			if foldKey(p.a) != foldKey(p.b) {
				t.Errorf("U+%04X %s: foldKey(%q) = %q, foldKey(%q) = %q", r, p.where, p.a, foldKey(p.a), p.b, foldKey(p.b))
			}
		}
	}
	for _, r := range hfsNotIgnored {
		if c := string(r); foldKey("a"+c) == foldKey("a") || foldKey("a"+c+"b") == foldKey("ab") {
			t.Errorf("U+%04X is not ignored by HFS+, yet foldKey drops it", r)
		}
	}
}

// An entry whose name differs from a link's only by a code point HFS+ skips is
// the link there: written through it, or over it. It is refused as the plain
// name is, wherever the code point stands and on whichever side it is.
func TestArchiveRejectsHFSPlusAliasesOfALink(t *testing.T) {
	const dir = "SUTest.app/Contents/"
	for _, r := range hfsIgnored {
		c := string(r)
		for _, p := range []struct{ where, link, entry, want string }{
			{"at the end", "a", "a" + c + "/x", "through the link"},
			{"in the middle", "ab", "a" + c + "b/x", "through the link"},
			{"at the start", "a", c + "a/x", "through the link"},
			{"deeper", "a", "a" + c + "/d/e/x", "through the link"},
			{"in the link's name", "a" + c, "a/x", "through the link"},
			{"a file over the link", "a", "a" + c, "shares its name"},
			{"a link over a file", "a" + c, "a", "shares its name"},
		} {
			t.Run(fmt.Sprintf("U+%04X %s", r, p.where), func(t *testing.T) {
				r := mkzip(t, bundle(link(dir+p.link, "Resources"), file(dir+p.entry, "x"))...)
				_, err := checkArchive(r, r.Size())
				if !errors.Is(err, ErrRejected) || !strings.Contains(err.Error(), p.want) {
					t.Fatalf("checkArchive = %v, want ErrRejected mentioning %q", err, p.want)
				}
			})
		}
	}
	// Control: a neighbour HFS+ does not skip is another name, and passes.
	r := mkzip(t, bundle(link(dir+"a", "Resources"), file(dir+"a\u200b/x", "x"))...)
	if _, err := checkArchive(r, r.Size()); err != nil {
		t.Errorf("a name with U+200B, which HFS+ keeps, was refused: %v", err)
	}
}
