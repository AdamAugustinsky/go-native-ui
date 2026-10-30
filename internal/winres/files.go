package winres

import (
	"fmt"
	"strings"
)

// FilesKey is the version string in which gnui package names the files that
// ship beside a Windows executable: the DLLs of gnui.json's nativeLibs and
// their licences, separated by "|" (a character no Windows file name holds).
// app/install copies exactly these with the executable, and app/selfupdate
// requires exactly these beside it in an update; an executable without the
// string has none.
const FilesKey = "GnuiFiles"

// Files returns the companion files v declares (FilesKey), checked with
// CheckFileName; none when v has no such string.
func Files(v Version) ([]string, error) {
	s := v.String(FilesKey)
	if s == "" {
		return nil, nil
	}
	names := strings.Split(s, "|")
	seen := make(map[string]bool, len(names))
	for _, n := range names {
		if err := CheckFileName(n); err != nil {
			return nil, err
		}
		k := strings.ToLower(n)
		if seen[k] {
			return nil, fmt.Errorf("winres: %s names %q twice", FilesKey, n)
		}
		seen[k] = true
	}
	return names, nil
}

// CheckFileName accepts the name of a file that may ship beside an
// executable: a plain file name (no folder, no character Windows refuses in
// a name, no trailing dot or space) ending in .dll or .txt.
func CheckFileName(n string) error {
	bad := func(why string) error { return fmt.Errorf("winres: %q cannot ship beside the executable: %s", n, why) }
	l := strings.ToLower(n)
	switch {
	case n == "" || len(n) > 255:
		return bad("empty or too long")
	case strings.ContainsAny(n, `\/:*?"<>|`):
		return bad("not a plain file name")
	case strings.HasSuffix(n, ".") || strings.HasSuffix(n, " ") || strings.HasPrefix(n, " "):
		return bad("a leading space or a trailing dot or space")
	case !strings.HasSuffix(l, ".dll") && !strings.HasSuffix(l, ".txt"):
		return bad("only .dll files and .txt licences")
	case len(n) == 4:
		return bad("no name before the extension")
	}
	for _, r := range n {
		if r < 0x20 || r == 0x7f {
			return bad("a control character")
		}
	}
	return nil
}
