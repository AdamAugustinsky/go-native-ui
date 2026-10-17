package pilot

import (
	"testing"
)

// Both rule sets are pure string functions, so both are tested on every OS.

func TestResolvePathUnix(t *testing.T) {
	for _, tc := range []struct{ in, want string }{
		{"~", "/home/u"},
		{"~/Docs", "/home/u/Docs"},
		{"~/", "/home/u"},
		{"~x", "/cur/~x"}, // not a home reference
		{"sub", "/cur/sub"},
		{"../up/", "/up"},
		{"../../../..", "/"},
		{"/abs//x/./y", "/abs/x/y"},
		{"  /padded  ", "/padded"},
		{"", "/cur"},
		{`back\slash`, `/cur/back\slash`}, // a legal name on Unix
		{`"quoted"`, `/cur/"quoted"`},
	} {
		if got := resolveWith(false, tc.in, "/cur", "/home/u"); got != tc.want {
			t.Errorf("unix resolve(%q) = %q, want %q", tc.in, got, tc.want)
		}
	}
}

func TestResolvePathWindows(t *testing.T) {
	const dir, home = `C:\Users\rex\Work`, `C:\Users\rex`
	for _, tc := range []struct{ in, want string }{
		{"", dir},
		{"~", home},
		{`~\Docs`, `C:\Users\rex\Docs`},
		{"~/Docs", `C:\Users\rex\Docs`},
		{"sub", `C:\Users\rex\Work\sub`},
		{`sub\deep/er`, `C:\Users\rex\Work\sub\deep\er`},
		{`..\..`, `C:\Users`},
		{`..\..\..\..\..`, `C:\`},
		{`\x`, `C:\x`}, // root-relative: the volume of the folder shown
		{"/x/y", `C:\x\y`},
		{`\`, `C:\`},
		{`D:\x`, `D:\x`},
		{"D:/x//y/./z", `D:\x\y\z`},
		{"d:\\x", `d:\x`},
		{"D:", `D:\`},
		{`D:x`, `D:\x`},                // another drive: its root
		{`c:x`, `C:\Users\rex\Work\x`}, // the drive of the folder: relative to it
		{`D:\..\x`, `D:\x`},
		{`\\server\share\a\..\b`, `\\server\share\b`},
		{`//server/share/x`, `\\server\share\x`},
		{`\\server\share`, `\\server\share\`},
		{`\\server\share\`, `\\server\share\`},
		{`\\server\share\..\..`, `\\server\share\`},
		{`\\server`, `C:\server`},                          // no share: not a UNC path
		{`"C:\Program Files\App"`, `C:\Program Files\App`}, // Explorer's "Copy as path"
		{`  D:\padded  `, `D:\padded`},
		{`...`, `C:\Users\rex\Work\...`}, // a legal (if odd) name
	} {
		if got := resolveWith(true, tc.in, dir, home); got != tc.want {
			t.Errorf("windows resolve(%q) = %q, want %q", tc.in, got, tc.want)
		}
	}
	// the volume of the shown folder is kept for root-relative paths, a UNC share included
	if got := resolveWith(true, `\x`, `\\srv\data\a\b`, home); got != `\\srv\data\x` {
		t.Errorf(`\x in a share: %q`, got)
	}
	if got := resolveWith(true, "..", `\\srv\data\a`, home); got != `\\srv\data\` {
		t.Errorf(`.. in a share: %q`, got)
	}
	if got := resolveWith(true, "sub", `D:\`, home); got != `D:\sub` {
		t.Errorf("below a drive root: %q", got)
	}
}

func TestParentOfARootIsItself(t *testing.T) {
	for _, tc := range []struct {
		win     bool
		dir, up string
	}{
		{false, "/", "/"},
		{false, "/a", "/"},
		{false, "/a/b/c", "/a/b"},
		{true, `C:\`, `C:\`},
		{true, `C:\a`, `C:\`},
		{true, `C:\a\b\c`, `C:\a\b`},
		{true, `C:`, `C:\`},
		{true, `\\server\share\`, `\\server\share\`},
		{true, `\\server\share\a`, `\\server\share\`},
		{true, `\\server\share\a\b`, `\\server\share\a`},
		{true, `C:/a/b`, `C:\a`},
	} {
		if got := parentWith(tc.win, tc.dir); got != tc.up {
			t.Errorf("parent(%v, %q) = %q, want %q", tc.win, tc.dir, got, tc.up)
		}
	}
	// Walking up from anywhere reaches a root in a bounded number of steps and stays there.
	for _, tc := range []struct {
		win bool
		dir string
	}{{false, "/a/b/c/d"}, {true, `C:\a\b\c\d`}, {true, `\\srv\share\a\b`}, {true, `D:\`}} {
		d, steps := tc.dir, 0
		for p := parentWith(tc.win, d); p != d; p = parentWith(tc.win, d) {
			d = p
			if steps++; steps > 20 {
				t.Fatalf("walking up from %q does not end", tc.dir)
			}
		}
		if again := parentWith(tc.win, d); again != d {
			t.Errorf("the root %q has parent %q", d, again)
		}
	}
}

func TestWindowsVolume(t *testing.T) {
	for _, tc := range []struct{ in, vol, rest string }{
		{`C:\a`, "C:", `\a`},
		{`c:a`, "c:", "a"},
		{`\a`, "", `\a`},
		{`a\b`, "", `a\b`},
		{`\\s\h\x`, `\\s\h`, `\x`},
		{`//s/h`, `\\s\h`, ""},
		{`\\s`, "", `\\s`},
		{`\\s\`, "", `\\s\`},
		{`\\\x`, "", `\\\x`},
		{`1:\x`, "", `1:\x`},
	} {
		vol, rest := winVolume(tc.in)
		if vol != tc.vol || rest != tc.rest {
			t.Errorf("winVolume(%q) = %q, %q; want %q, %q", tc.in, vol, rest, tc.vol, tc.rest)
		}
	}
}

// The host's rules are the ones the pane uses.
func TestResolveHost(t *testing.T) {
	dir, home := nativePath("/cur"), nativePath("/home/u")
	if got, want := ResolvePath("~/x", dir, home), nativePath("/home/u/x"); got != want {
		t.Errorf("%q, want %q", got, want)
	}
	if got, want := ResolvePath("../y", dir, home), nativePath("/y"); got != want {
		t.Errorf("%q, want %q", got, want)
	}
	if got := parentPath(nativePath("/a/b")); got != nativePath("/a") {
		t.Errorf("parent %q", got)
	}
}
