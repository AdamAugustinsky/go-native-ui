package pilot

import (
	"path"
	"path/filepath"
	"runtime"
	"strings"
)

// Paths. Two rule sets, both pure string functions so that both are tested on
// every OS: Unix (one root "/", "/" separates) and Windows (volumes: a drive
// "C:" or a UNC share "\\server\share"; "\" and "/" both separate; paths are
// kept with "\"). The host's rules are chosen by runtime.GOOS. No file is
// touched.

var onWindows = runtime.GOOS == "windows"

// ResolvePath is what a path typed in a path field or given on the command
// line means in dir (the folder shown; home is the user's home folder). See
// resolveWith for the rules.
func ResolvePath(text, dir, home string) string { return resolveWith(onWindows, text, dir, home) }

// parentPath is the folder above dir; a root is its own parent.
func parentPath(dir string) string { return parentWith(onWindows, dir) }

// joinPath is dir/name.
func joinPath(dir, name string) string { return filepath.Join(dir, name) }

// resolveWith turns text into an absolute, clean folder path. Both rule sets:
// surrounding blanks go; "" is dir; "~" and "~/x" are the home folder and
// below it (so is "~\x" on Windows); a relative path is taken against dir;
// ".", ".." and repeated separators are resolved, and ".." at a root stays at
// the root.
//
// Windows adds: a path in double quotes (Explorer's "Copy as path") loses them;
// "\x" and "/x" are on the volume of dir (a UNC share included); "D:\x" and
// "D:/x" are on drive D, "D:" is "D:\"; "D:x" is relative to dir when dir is on
// D and to D's root otherwise (a drive's own current folder is not known here);
// "\\server\share\x" is a UNC path, "\\server" alone (no share) is read as
// "\server" on the volume of dir. Results have "\" separators, and a root ends
// with one ("C:\", "\\server\share\").
func resolveWith(win bool, text, dir, home string) string {
	text = strings.TrimSpace(text)
	if win {
		if len(text) >= 2 && text[0] == '"' && text[len(text)-1] == '"' {
			text = strings.TrimSpace(text[1 : len(text)-1])
		}
	}
	if text == "" {
		return dir
	}
	if text == "~" {
		text = home
	} else if strings.HasPrefix(text, "~/") || (win && strings.HasPrefix(text, `~\`)) {
		text = home + string(sepOf(win)) + text[2:]
	}
	if !win {
		if !path.IsAbs(text) {
			text = path.Join(dir, text)
		}
		return path.Clean(text)
	}
	return resolveWin(text, dir)
}

func sepOf(win bool) byte {
	if win {
		return '\\'
	}
	return '/'
}

func isSep(c byte) bool { return c == '\\' || c == '/' }

// winVolume splits a Windows path into its volume ("C:", "\\server\share", or
// "") and the rest.
func winVolume(p string) (vol, rest string) {
	if len(p) >= 2 && p[1] == ':' && (p[0]|0x20 >= 'a' && p[0]|0x20 <= 'z') {
		return p[:2], p[2:]
	}
	if len(p) >= 5 && isSep(p[0]) && isSep(p[1]) && !isSep(p[2]) {
		i := 2
		for i < len(p) && !isSep(p[i]) {
			i++
		}
		if i < len(p) { // \\server\
			j := i + 1
			for j < len(p) && !isSep(p[j]) {
				j++
			}
			if j > i+1 { // a share
				return `\\` + p[2:i] + `\` + p[i+1:j], p[j:]
			}
		}
	}
	return "", p
}

// winElems appends the clean elements of rest (relative to a root when rooted,
// resolving "." and "..") to base.
func winElems(base []string, rest string) []string {
	for len(rest) > 0 {
		i := 0
		for i < len(rest) && !isSep(rest[i]) {
			i++
		}
		switch e := rest[:i]; e {
		case "", ".":
		case "..":
			if len(base) > 0 {
				base = base[:len(base)-1]
			}
		default:
			base = append(base, e)
		}
		if i < len(rest) {
			i++
		}
		rest = rest[i:]
	}
	return base
}

func winJoin(vol string, elems []string) string {
	s := vol + `\`
	for i, e := range elems {
		if i > 0 {
			s += `\`
		}
		s += e
	}
	return s
}

func sameVolume(a, b string) bool { return strings.EqualFold(a, b) }

func resolveWin(text, dir string) string {
	dvol, drest := winVolume(dir)
	vol, rest := winVolume(text)
	switch {
	case vol == "": // "\x", "/x" or relative
		if len(rest) > 0 && isSep(rest[0]) { // on the volume of dir (an incomplete UNC "\\server" lands here)
			return winJoin(dvol, winElems(nil, rest))
		}
		return winJoin(dvol, winElems(winElems(nil, drest), rest))
	case vol[0] == '\\': // UNC
		return winJoin(vol, winElems(nil, rest))
	case len(rest) > 0 && isSep(rest[0]): // "D:\x"
		return winJoin(vol, winElems(nil, rest))
	case rest != "" && sameVolume(vol, dvol): // "D:x" with dir on D
		return winJoin(dvol, winElems(winElems(nil, drest), rest))
	}
	return winJoin(vol, winElems(nil, rest)) // "D:" and "D:x" elsewhere: the root of D
}

// parentWith is the folder above dir, dir itself at a root (the root of a
// drive, of a UNC share, "/"). A path that is not clean is cleaned first.
func parentWith(win bool, dir string) string {
	if !win {
		return path.Dir(dir)
	}
	vol, rest := winVolume(dir)
	elems := winElems(nil, rest)
	if len(elems) > 0 {
		elems = elems[:len(elems)-1]
	}
	return winJoin(vol, elems)
}

// nativePath turns a Unix-style absolute path of the fixtures ("/Users/rex")
// into one of the host: itself, or on Windows "C:\Users\rex".
func nativePath(unix string) string {
	if !onWindows {
		return unix
	}
	return `C:` + strings.ReplaceAll(unix, "/", `\`)
}
