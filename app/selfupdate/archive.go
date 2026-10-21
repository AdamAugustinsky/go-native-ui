package selfupdate

import (
	"archive/zip"
	"fmt"
	"io"
	"io/fs"
	"path"
	"strings"
	"unicode/utf8"
)

// Limits on what an archive may unpack to.
const (
	maxEntries     = 200_000
	maxTotalSize   = 8 << 30 // bytes, uncompressed
	maxLinkTarget  = 1024    // bytes
	maxNameLength  = 1024    // bytes, a whole entry name
	maxNameSegment = 255     // bytes, one component
)

// checkArchive reads the zip's directory and returns the name of its one
// top-level .app. It refuses, before anything is unpacked, every entry that
// could make ditto write outside the destination: absolute names, "." and
// ".." components, any entry whose path goes through a link, and any entry
// with a link's name. With those gone ditto never writes through a link, so
// every write lands at the entry's own name under the destination. Links
// whose target leaves the bundle by name are refused here too; the unpacked
// tree is checked again by resolving every link for real (checkTree).
//
// Paths are compared through foldKey, which errs on the side of a match, so a
// name that only differs in case or Unicode form from a link's (macOS volumes
// ignore both) still counts as the link.
func checkArchive(r io.ReaderAt, size int64) (string, error) {
	zr, err := zip.NewReader(r, size)
	if err != nil {
		return "", fmt.Errorf("%w: not a zip archive: %v", ErrRejected, err)
	}
	if len(zr.File) == 0 {
		return "", fmt.Errorf("%w: the archive is empty", ErrRejected)
	}
	if len(zr.File) > maxEntries {
		return "", fmt.Errorf("%w: the archive has %d entries (at most %d)", ErrRejected, len(zr.File), maxEntries)
	}
	var (
		top   string
		total uint64
		seen  = make(map[string]bool, len(zr.File))
		links = map[string]bool{} // foldKey of every link entry
		names = make([]string, 0, len(zr.File))
	)
	for _, f := range zr.File {
		name := strings.TrimSuffix(f.Name, "/")
		if err := checkName(f.Name); err != nil {
			return "", err
		}
		mode := f.Mode()
		switch mode.Type() {
		case 0, fs.ModeDir, fs.ModeSymlink:
		default:
			return "", fmt.Errorf("%w: %q is a %v, not a file, folder or link", ErrRejected, f.Name, mode.Type())
		}
		if strings.HasSuffix(f.Name, "/") != mode.IsDir() {
			return "", fmt.Errorf("%w: %q: the name and the type disagree", ErrRejected, f.Name)
		}
		if seen[name] {
			return "", fmt.Errorf("%w: %q appears twice", ErrRejected, name)
		}
		seen[name] = true
		first, _, _ := strings.Cut(name, "/")
		if top == "" {
			top = first
		}
		if first != top {
			return "", fmt.Errorf("%w: more than one item at the top (%q and %q)", ErrRejected, top, first)
		}
		if total += f.UncompressedSize64; total > maxTotalSize || f.UncompressedSize64 > maxTotalSize {
			return "", fmt.Errorf("%w: the archive unpacks to more than %d bytes", ErrRejected, uint64(maxTotalSize))
		}
		if mode.Type() == fs.ModeSymlink {
			if name == top {
				return "", fmt.Errorf("%w: the top-level item is a link", ErrRejected)
			}
			target, err := readLink(f)
			if err != nil {
				return "", err
			}
			if err := checkLink(top, name, target); err != nil {
				return "", err
			}
			links[foldKey(name)] = true
		}
		names = append(names, name)
	}
	if !strings.HasSuffix(top, ".app") || len(top) == len(".app") {
		return "", fmt.Errorf("%w: the top-level item %q is not an .app", ErrRejected, top)
	}
	if !seen[top] {
		// ditto writes the folder entry first; without it the bundle's own
		// folder would be made implicitly, with modes nobody chose.
		return "", fmt.Errorf("%w: the archive has no entry for %q itself", ErrRejected, top)
	}
	if len(links) > 0 {
		keys := make(map[string]int, len(names))
		for _, name := range names {
			keys[foldKey(name)]++
			for i := 0; i < len(name); i++ {
				if name[i] == '/' && links[foldKey(name[:i])] {
					return "", fmt.Errorf("%w: %q is reached through the link %q", ErrRejected, name, name[:i])
				}
			}
		}
		// A file or folder whose name differs from a link's only in case or
		// form would be opened through the link.
		for k := range links {
			if keys[k] > 1 {
				return "", fmt.Errorf("%w: a link shares its name with another entry", ErrRejected)
			}
		}
	}
	return top, nil
}

// checkName refuses entry names that are not plain relative paths.
func checkName(n string) error {
	bad := func(why string) error { return fmt.Errorf("%w: entry %q: %s", ErrRejected, n, why) }
	switch {
	case n == "" || n == "/":
		return bad("empty name")
	case len(n) > maxNameLength:
		return bad("name too long")
	case !utf8.ValidString(n):
		return bad("not UTF-8")
	case strings.HasPrefix(n, "/"):
		return bad("absolute name")
	case strings.ContainsAny(n, "\x00\\"):
		return bad("NUL or backslash in the name")
	}
	for _, c := range strings.Split(strings.TrimSuffix(n, "/"), "/") {
		switch {
		case c == "":
			return bad("empty component")
		case c == "." || c == "..":
			return bad(`"." or ".." component`)
		case len(c) > maxNameSegment:
			return bad("component too long")
		}
		for _, r := range c {
			if r < 0x20 || r == 0x7f {
				return bad("control character")
			}
		}
	}
	return nil
}

func readLink(f *zip.File) (string, error) {
	if f.UncompressedSize64 == 0 || f.UncompressedSize64 > maxLinkTarget {
		return "", fmt.Errorf("%w: link %q has a target of %d bytes", ErrRejected, f.Name, f.UncompressedSize64)
	}
	rc, err := f.Open()
	if err != nil {
		return "", fmt.Errorf("%w: link %q: %v", ErrRejected, f.Name, err)
	}
	defer rc.Close()
	b, err := io.ReadAll(io.LimitReader(rc, maxLinkTarget+1))
	if err != nil {
		return "", fmt.Errorf("%w: link %q: %v", ErrRejected, f.Name, err)
	}
	if uint64(len(b)) != f.UncompressedSize64 {
		return "", fmt.Errorf("%w: link %q: the target is not the size the archive says", ErrRejected, f.Name)
	}
	return string(b), nil
}

// checkLink requires a link at name (inside the bundle top) to point, by
// name, inside top: a relative target that does not climb out of it. top
// itself is inside.
func checkLink(top, name, target string) error {
	switch {
	case target == "" || !utf8.ValidString(target) || strings.ContainsRune(target, 0):
		return fmt.Errorf("%w: link %q has an unusable target", ErrRejected, name)
	case strings.HasPrefix(target, "/"):
		return fmt.Errorf("%w: link %q points to the absolute path %q", ErrRejected, name, target)
	}
	resolved := path.Join(path.Dir(name), target) // Clean: ".." folds into the folders before it
	if resolved != top && !strings.HasPrefix(resolved, top+"/") {
		return fmt.Errorf("%w: link %q points outside the bundle (%q)", ErrRejected, name, target)
	}
	return nil
}

// foldKey maps a path to a key under which every pair of names that a macOS
// volume might treat as the same (case-insensitive, and insensitive to or
// normalizing Unicode form) gets the same key. ASCII components are
// lowercased. The few non-ASCII letters that fold or decompose to ASCII (the
// Kelvin sign, the long s, the dotted and dotless i, the Greek question mark
// and the Greek varia) are mapped to it. The code points that HFS+ skips when
// it compares names (hfsIgnorable) are dropped, anywhere in a component, on
// every volume: APFS skips none, so this only ever matches more. Any other
// component with a non-ASCII character becomes one shared key, so all such
// components match each other: a false match only refuses an archive, a
// missed one could not be allowed.
func foldKey(p string) string {
	var b strings.Builder
	b.Grow(len(p))
	for i, c := range strings.Split(p, "/") {
		if i > 0 {
			b.WriteByte('/')
		}
		b.WriteString(foldComponent(c))
	}
	return b.String()
}

func foldComponent(c string) string {
	var b strings.Builder
	b.Grow(len(c))
	for _, r := range c {
		switch {
		case r < utf8.RuneSelf:
			if 'A' <= r && r <= 'Z' {
				r += 'a' - 'A'
			}
		case r == '\u212A': // KELVIN SIGN, canonically K
			r = 'k'
		case r == '\u017F': // LATIN SMALL LETTER LONG S, folds to s
			r = 's'
		case r == '\u0130' || r == '\u0131': // dotted capital and dotless small i
			r = 'i'
		case r == '\u037E': // GREEK QUESTION MARK, canonically ;
			r = ';'
		case r == '\u1FEF': // GREEK VARIA, canonically `
			r = '`'
		case hfsIgnorable(r):
			continue
		default:
			return "\x00"
		}
		b.WriteRune(r)
	}
	return b.String()
}

// hfsIgnorable reports whether HFS+ (and case-insensitive HFSX) skips r when
// it compares names, so that "a" and "a\u200c" are one name there (TN1150,
// "Case-Insensitive String Comparison Algorithm": ignorable characters map to
// zero in its folding table, which the note does not print). The set is the
// formatting characters U+200C-U+200F (zero-width non-joiner and joiner, the
// two direction marks), U+202A-U+202E (embeddings and overrides), U+206A-U+206F
// (the deprecated format characters) and U+FEFF (the byte order mark). It was
// measured, not recalled: TestFoldKeyIgnoresWhatHFSPlusIgnores looks up every
// code point on a real HFS+ volume.
func hfsIgnorable(r rune) bool {
	return r >= 0x200C && r <= 0x200F || r >= 0x202A && r <= 0x202E || r >= 0x206A && r <= 0x206F || r == 0xFEFF
}
