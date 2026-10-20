package img

import (
	"io"
	"os"
	"path/filepath"
	"slices"
	"strconv"
	"strings"
	"sync"
)

// Freedesktop icon lookup: the shared-mime-info type of a name, then the
// icon theme specification's search for a PNG at the nearest size, with no
// third-party code and no SVG rendering. It reads the files of the XDG
// directories, so it is plain Go and builds everywhere; Linux uses it
// (sysicon_linux.go) and the tests run it on every OS against a theme they
// make.
//
// What it follows from the specifications:
//
//   - MIME types: mime/globs2 of every data directory (weight, type, glob,
//     case-sensitivity flag): literal names first, else the longest "*.ext"
//     suffix, then the heaviest. Other glob shapes are not matched. The
//     parents of a type come from mime/subclasses and its generic icon from
//     mime/generic-icons.
//   - Icon names: the type with "/" as "-" (application-pdf), its generic
//     icon (x-office-document), the same for the parent types, then
//     "<media>-x-generic". A directory is inode/directory, icon "folder".
//   - Themes: the GTK setting gtk-icon-theme-name of gtk-3.0 and gtk-4.0
//     (the file settings.ini; the desktop's dconf is not read), then
//     Adwaita, each with its Inherits, and hicolor last. A theme is the
//     index.theme found first in ~/.icons, $XDG_DATA_HOME/icons and each
//     $XDG_DATA_DIRS/icons; its directories are searched in all of them.
//   - Size: the directory (of Type Fixed, Scalable or Threshold) whose size
//     is nearest to the one asked for, the larger on a tie; PNG files only.
type xdgDB struct {
	sig   string
	home  string
	data  []string // data directories, user first
	icons []string // icon base directories, in search order

	globs   []xdgGlob
	generic map[string]string
	parent  map[string][]string
	config  []string // gtk settings files

	mu     sync.Mutex
	themes map[string]*xdgTheme
	chain  []string
}

// xdgGlob is one line of globs2.
type xdgGlob struct {
	weight  int
	mime    string
	pattern string // the literal name, or the suffix of "*suffix"
	literal bool
	cs      bool
}

// xdgDir is a directory of a theme.
type xdgDir struct {
	path             string
	size, scale      int
	minSize, maxSize int
	threshold        int
	fixed, scalable  bool
	pixelSize        int // size × scale
}

// xdgTheme is a parsed index.theme and the base directories that hold it.
type xdgTheme struct {
	name     string
	dirs     []xdgDir
	inherits []string
	bases    []string
}

var xdgCache struct {
	mu sync.Mutex
	db *xdgDB
}

// xdgSystem returns the database of the current environment, loading it
// again when the XDG variables changed.
func xdgSystem() *xdgDB {
	home, _ := os.UserHomeDir()
	dataHome := os.Getenv("XDG_DATA_HOME")
	if dataHome == "" {
		dataHome = filepath.Join(home, ".local", "share")
	}
	dataDirs := os.Getenv("XDG_DATA_DIRS")
	if dataDirs == "" {
		dataDirs = "/usr/local/share:/usr/share"
	}
	configHome := os.Getenv("XDG_CONFIG_HOME")
	if configHome == "" {
		configHome = filepath.Join(home, ".config")
	}
	sig := home + "\x00" + dataHome + "\x00" + dataDirs + "\x00" + configHome
	xdgCache.mu.Lock()
	defer xdgCache.mu.Unlock()
	if xdgCache.db == nil || xdgCache.db.sig != sig {
		xdgCache.db = newXDG(sig, home, dataHome, filepath.SplitList(dataDirs), configHome)
	}
	return xdgCache.db
}

// Limits on what the lookup reads from the system's files, which it does not
// trust to be well formed: a file over its byte limit is ignored as a whole,
// and a parser keeps no more than its record limit.
const (
	maxGlobsBytes    = 4 << 20
	maxGlobRecords   = 100000
	maxSubclassBytes = 1 << 20
	maxSubclassLines = 20000
	maxParentsOf     = 16 // parents kept per type
	maxGenericBytes  = 1 << 20
	maxGenericLines  = 20000
	maxThemeBytes    = 1 << 20
	maxThemeDirs     = 2000
	maxSettingsBytes = 64 << 10
	maxMimeWalk      = 64 // types visited when expanding one type's ancestry
)

// readLimited reads the file at path, or nothing if it is missing or larger
// than max bytes.
func readLimited(path string, max int64) ([]byte, bool) {
	f, err := os.Open(path)
	if err != nil {
		return nil, false
	}
	defer f.Close()
	b, err := io.ReadAll(io.LimitReader(f, max+1))
	if err != nil || int64(len(b)) > max {
		return nil, false
	}
	return b, true
}

func newXDG(sig, home, dataHome string, dataDirs []string, configHome string) *xdgDB {
	db := &xdgDB{sig: sig, home: home, themes: map[string]*xdgTheme{}}
	db.data = append(db.data, dataHome)
	for _, d := range dataDirs {
		if d != "" {
			db.data = append(db.data, d)
		}
	}
	db.icons = append(db.icons, filepath.Join(home, ".icons"))
	for _, d := range db.data {
		db.icons = append(db.icons, filepath.Join(d, "icons"))
	}
	db.config = []string{filepath.Join(configHome, "gtk-3.0", "settings.ini"), filepath.Join(configHome, "gtk-4.0", "settings.ini")}
	db.generic = map[string]string{}
	db.parent = map[string][]string{}
	for _, d := range db.data {
		if b, ok := readLimited(filepath.Join(d, "mime", "globs2"), maxGlobsBytes); ok {
			db.globs = parseGlobs2(db.globs, string(b))
		}
		if b, ok := readLimited(filepath.Join(d, "mime", "generic-icons"), maxGenericBytes); ok {
			parseGenericIcons(db.generic, string(b))
		}
		if b, ok := readLimited(filepath.Join(d, "mime", "subclasses"), maxSubclassBytes); ok {
			parseSubclasses(db.parent, string(b))
		}
	}
	return db
}

func parseGlobs2(dst []xdgGlob, s string) []xdgGlob {
	for _, line := range strings.Split(s, "\n") {
		if line == "" || line[0] == '#' {
			continue
		}
		f := strings.SplitN(line, ":", 4)
		if len(f) < 3 {
			continue
		}
		weight, err := strconv.Atoi(f[0])
		if err != nil {
			continue
		}
		g := xdgGlob{weight: weight, mime: f[1], pattern: f[2]}
		if len(f) == 4 {
			g.cs = strings.Contains(f[3], "cs")
		}
		switch {
		case strings.HasPrefix(g.pattern, "*") && !strings.ContainsAny(g.pattern[1:], "*?["):
			g.pattern = g.pattern[1:]
		case !strings.ContainsAny(g.pattern, "*?["):
			g.literal = true
		default:
			continue // other shapes are not matched
		}
		if g.pattern == "" {
			continue
		}
		if len(dst) >= maxGlobRecords {
			break
		}
		dst = append(dst, g)
	}
	return dst
}

func parseGenericIcons(dst map[string]string, s string) {
	for i, line := range strings.Split(s, "\n") {
		if i >= maxGenericLines {
			break
		}
		if mime, icon, ok := strings.Cut(line, ":"); ok && line[0] != '#' {
			if _, have := dst[mime]; !have {
				dst[mime] = icon
			}
		}
	}
}

func parseSubclasses(dst map[string][]string, s string) {
	for i, line := range strings.Split(s, "\n") {
		if i >= maxSubclassLines {
			break
		}
		child, parent, ok := strings.Cut(line, " ")
		parent = strings.TrimSpace(parent)
		if !ok || line[0] == '#' || parent == "" || parent == child {
			continue
		}
		have := dst[child]
		if len(have) >= maxParentsOf || slices.Contains(have, parent) {
			continue
		}
		dst[child] = append(have, parent)
	}
}

// mimeFor is the MIME type of a file named name (a base name), or "". As
// shared-mime-info orders them: a literal name beats any suffix, then the
// heavier glob wins, then the longer suffix; of equals the earlier (the
// user's directory before the system's).
func (db *xdgDB) mimeFor(name string) string {
	lower := strings.ToLower(name)
	best, bestLiteral, bestWeight, bestLen := "", false, -1, -1
	for i := range db.globs {
		g := &db.globs[i]
		n, p := name, g.pattern
		if !g.cs {
			n, p = lower, strings.ToLower(p)
		}
		if g.literal && n != p || !g.literal && !strings.HasSuffix(n, p) {
			continue
		}
		l := len(p)
		better := best == "" ||
			g.literal != bestLiteral && g.literal ||
			g.literal == bestLiteral && (g.weight > bestWeight || g.weight == bestWeight && l > bestLen)
		if better {
			best, bestLiteral, bestWeight, bestLen = g.mime, g.literal, g.weight, l
		}
	}
	return best
}

// iconNames are the icon names to try for a MIME type, most specific first:
// the type, its generic icon, then its ancestors' (breadth first, to five
// generations, each type once, at most maxMimeWalk types: a subclasses file
// with cycles or a fan of edges costs no more than that).
func (db *xdgDB) iconNames(mime string) []string {
	var names []string
	add := func(n string) {
		if !slices.Contains(names, n) {
			names = append(names, n)
		}
	}
	visited := []string{mime}
	queue := []string{mime}
	for depth := 0; len(queue) > 0 && depth < 5; depth++ {
		var next []string
		for _, m := range queue {
			add(strings.ReplaceAll(m, "/", "-"))
			if g := db.generic[m]; g != "" {
				add(g)
			}
			for _, p := range db.parent[m] {
				if len(visited) >= maxMimeWalk {
					break
				}
				if !slices.Contains(visited, p) {
					visited = append(visited, p)
					next = append(next, p)
				}
			}
		}
		queue = next
	}
	if media, _, ok := strings.Cut(mime, "/"); ok {
		add(media + "-x-generic")
	}
	return names
}

// ---- themes ---------------------------------------------------------------

// iniSections reads a desktop-style INI file into sections of key=value
// (keys with a locale are dropped).
func iniSections(s string) map[string]map[string]string {
	out := map[string]map[string]string{}
	var cur map[string]string
	for _, line := range strings.Split(s, "\n") {
		line = strings.TrimSpace(line)
		switch {
		case line == "" || line[0] == '#' || line[0] == ';':
		case line[0] == '[' && line[len(line)-1] == ']':
			cur = map[string]string{}
			out[line[1:len(line)-1]] = cur
		case cur != nil:
			if k, v, ok := strings.Cut(line, "="); ok && !strings.Contains(k, "[") {
				cur[strings.TrimSpace(k)] = strings.TrimSpace(v)
			}
		}
	}
	return out
}

func atoiOr(s string, def int) int {
	if n, err := strconv.Atoi(s); err == nil {
		return n
	}
	return def
}

// parseTheme reads an index.theme.
func parseTheme(name, index string) *xdgTheme {
	sec := iniSections(index)
	head := sec["Icon Theme"]
	if head == nil {
		return nil
	}
	t := &xdgTheme{name: name}
	for _, n := range strings.Split(head["Inherits"], ",") {
		if n = strings.TrimSpace(n); n != "" && len(t.inherits) < 16 {
			t.inherits = append(t.inherits, n)
		}
	}
	for _, list := range [...]string{head["Directories"], head["ScaledDirectories"]} {
		for _, d := range strings.Split(list, ",") {
			d = strings.TrimSpace(d)
			s := sec[d]
			if d == "" || s == nil || !filepath.IsLocal(d) || len(t.dirs) >= maxThemeDirs {
				continue
			}
			size := atoiOr(s["Size"], 0)
			scale := atoiOr(s["Scale"], 1)
			if size <= 0 || size > 8192 || scale < 1 || scale > 8 {
				continue
			}
			x := xdgDir{path: d, size: size, scale: scale, threshold: min(max(atoiOr(s["Threshold"], 2), 0), 8192)}
			x.minSize, x.maxSize = min(max(atoiOr(s["MinSize"], size), 0), 8192), min(max(atoiOr(s["MaxSize"], size), 0), 8192)
			switch s["Type"] {
			case "Fixed":
				x.fixed = true
			case "Scalable":
				x.scalable = true
			}
			x.pixelSize = x.size * x.scale
			t.dirs = append(t.dirs, x)
		}
	}
	return t
}

// distance is how far a directory's icons are from size pixels (the icon
// theme specification's DirectorySizeDistance, in pixels).
func (d *xdgDir) distance(size int) int {
	abs := func(n int) int {
		if n < 0 {
			return -n
		}
		return n
	}
	switch {
	case d.fixed:
		return abs(d.pixelSize - size)
	case d.scalable:
		if size < d.minSize*d.scale {
			return d.minSize*d.scale - size
		}
		if size > d.maxSize*d.scale {
			return size - d.maxSize*d.scale
		}
		return 0
	}
	if size < (d.size-d.threshold)*d.scale {
		return (d.size-d.threshold)*d.scale - size
	}
	if size > (d.size+d.threshold)*d.scale {
		return size - (d.size+d.threshold)*d.scale
	}
	return 0
}

// theme returns the named theme (nil if no base directory has it). The
// first index.theme found describes it; its directories are then searched in
// every base directory that has a directory of the theme's name, whether or
// not that one has an index of its own (the specification lets a user add
// icons to a system theme from ~/.icons).
func (db *xdgDB) theme(name string) *xdgTheme {
	db.mu.Lock()
	defer db.mu.Unlock()
	if t, ok := db.themes[name]; ok {
		return t
	}
	var t *xdgTheme
	var bases []string
	if filepath.IsLocal(name) && !strings.ContainsAny(name, `/\`) {
		for _, base := range db.icons {
			dir := filepath.Join(base, name)
			if st, err := os.Stat(dir); err != nil || !st.IsDir() {
				continue
			}
			bases = append(bases, base)
			if t == nil {
				if b, ok := readLimited(filepath.Join(dir, "index.theme"), maxThemeBytes); ok {
					t = parseTheme(name, string(b))
				}
			}
		}
	}
	if t != nil {
		t.bases = bases
	}
	db.themes[name] = t
	return t
}

// themeChain is the themes to search, in order: the configured one with
// what it inherits, Adwaita, hicolor.
func (db *xdgDB) themeChain() []string {
	db.mu.Lock()
	if db.chain != nil {
		c := db.chain
		db.mu.Unlock()
		return c
	}
	db.mu.Unlock()
	var roots []string
	for _, f := range db.config {
		if b, ok := readLimited(f, maxSettingsBytes); ok {
			if n := iniSections(string(b))["Settings"]["gtk-icon-theme-name"]; n != "" {
				roots = append(roots, strings.Trim(n, `"'`))
				break
			}
		}
	}
	roots = append(roots, "Adwaita")
	var chain []string
	seen := map[string]bool{}
	var walk func(name string, depth int)
	walk = func(name string, depth int) {
		if seen[name] || depth > 8 {
			return
		}
		seen[name] = true
		if len(seen) > 32 {
			return
		}
		t := db.theme(name)
		if t == nil {
			return
		}
		chain = append(chain, name)
		for _, in := range t.inherits {
			walk(in, depth+1)
		}
	}
	for _, r := range roots {
		walk(r, 0)
	}
	walk("hicolor", 0)
	db.mu.Lock()
	db.chain = chain
	db.mu.Unlock()
	return chain
}

// findPNG is the PNG file of the icon called name nearest to size pixels in
// the theme chain ("" if there is none).
func (db *xdgDB) findPNG(name string, size int) string {
	for _, tn := range db.themeChain() {
		t := db.theme(tn)
		best, bestDist, bestSize := "", 0, 0
		for i := range t.dirs {
			d := &t.dirs[i]
			dist := d.distance(size)
			if best != "" && (dist > bestDist || dist == bestDist && d.pixelSize <= bestSize) {
				continue
			}
			for _, base := range t.bases {
				p := filepath.Join(base, t.name, d.path, name+".png")
				if st, err := os.Stat(p); err == nil && st.Mode().IsRegular() {
					best, bestDist, bestSize = p, dist, d.pixelSize
					break
				}
			}
		}
		if best != "" {
			return best
		}
	}
	return ""
}

// iconPNG is the PNG file for the system icon src names, at about size
// pixels, or the error code. It never reads an image.
func (db *xdgDB) iconPNG(src Source, size int) (string, uint8) {
	mime := ""
	switch src.kind {
	case srcIconFolder:
		mime = "inode/directory"
	case srcIconType:
		if src.path != "public.folder" && src.path != "public.directory" {
			return "", errUnsupported // UTIs are Apple's
		}
		mime = "inode/directory"
	case srcIconExt:
		ext := strings.TrimPrefix(src.path, ".")
		if ext == "" || strings.ContainsAny(ext, "/\\") {
			return "", errUnsupported
		}
		mime = db.mimeFor("x." + ext)
	case srcIconFile:
		st, err := os.Stat(src.path)
		if err != nil {
			return "", errRead
		}
		if st.IsDir() {
			mime = "inode/directory"
		} else {
			mime = db.mimeFor(filepath.Base(src.path))
		}
	default:
		return "", errUnsupported
	}
	if mime == "" {
		mime = "application/octet-stream"
	}
	if len(db.globs) == 0 && mime != "inode/directory" {
		return "", errUnsupported // no MIME database
	}
	for _, n := range db.iconNames(mime) {
		if p := db.findPNG(n, size); p != "" {
			return p, errNone
		}
	}
	if mime == "inode/directory" {
		if p := db.findPNG("folder", size); p != "" {
			return p, errNone
		}
	}
	return "", errUnsupported
}
