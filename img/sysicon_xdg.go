package img

import (
	"os"
	"path/filepath"
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
		xdgCache.db = newXDG(sig, home, dataHome, strings.Split(dataDirs, ":"), configHome)
	}
	return xdgCache.db
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
		if b, err := os.ReadFile(filepath.Join(d, "mime", "globs2")); err == nil {
			db.globs = parseGlobs2(db.globs, string(b))
		}
		if b, err := os.ReadFile(filepath.Join(d, "mime", "generic-icons")); err == nil {
			parseGenericIcons(db.generic, string(b))
		}
		if b, err := os.ReadFile(filepath.Join(d, "mime", "subclasses")); err == nil {
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
		dst = append(dst, g)
	}
	return dst
}

func parseGenericIcons(dst map[string]string, s string) {
	for _, line := range strings.Split(s, "\n") {
		if mime, icon, ok := strings.Cut(line, ":"); ok && line[0] != '#' {
			if _, have := dst[mime]; !have {
				dst[mime] = icon
			}
		}
	}
}

func parseSubclasses(dst map[string][]string, s string) {
	for _, line := range strings.Split(s, "\n") {
		if child, parent, ok := strings.Cut(line, " "); ok && line[0] != '#' {
			dst[child] = append(dst[child], strings.TrimSpace(parent))
		}
	}
}

// mimeFor is the MIME type of a file named name (a base name), or "".
func (db *xdgDB) mimeFor(name string) string {
	lower := strings.ToLower(name)
	best, bestScore, bestWeight := "", -1, -1
	for i := range db.globs {
		g := &db.globs[i]
		n, p := name, g.pattern
		if !g.cs {
			n, p = lower, strings.ToLower(p)
		}
		score := 1 << 20 // a literal name beats any suffix
		if g.literal {
			if n != p {
				continue
			}
		} else {
			if !strings.HasSuffix(n, p) {
				continue
			}
			score = len(p) // the longest suffix wins
		}
		if score > bestScore || score == bestScore && g.weight > bestWeight {
			best, bestScore, bestWeight = g.mime, score, g.weight
		}
	}
	return best
}

// iconNames are the icon names to try for a MIME type, most specific first.
func (db *xdgDB) iconNames(mime string) []string {
	var names []string
	add := func(n string) {
		for _, have := range names {
			if have == n {
				return
			}
		}
		names = append(names, n)
	}
	queue := []string{mime}
	for depth := 0; len(queue) > 0 && depth < 5; depth++ {
		var next []string
		for _, m := range queue {
			add(strings.ReplaceAll(m, "/", "-"))
			if g := db.generic[m]; g != "" {
				add(g)
			}
			next = append(next, db.parent[m]...)
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
		if n = strings.TrimSpace(n); n != "" {
			t.inherits = append(t.inherits, n)
		}
	}
	for _, list := range [...]string{head["Directories"], head["ScaledDirectories"]} {
		for _, d := range strings.Split(list, ",") {
			d = strings.TrimSpace(d)
			s := sec[d]
			if d == "" || s == nil {
				continue
			}
			size := atoiOr(s["Size"], 0)
			if size <= 0 {
				continue
			}
			x := xdgDir{path: d, size: size, scale: max(atoiOr(s["Scale"], 1), 1), threshold: atoiOr(s["Threshold"], 2)}
			x.minSize, x.maxSize = atoiOr(s["MinSize"], size), atoiOr(s["MaxSize"], size)
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

// theme returns the named theme (nil if no base directory has it).
func (db *xdgDB) theme(name string) *xdgTheme {
	db.mu.Lock()
	defer db.mu.Unlock()
	if t, ok := db.themes[name]; ok {
		return t
	}
	var t *xdgTheme
	for _, base := range db.icons {
		b, err := os.ReadFile(filepath.Join(base, name, "index.theme"))
		if err != nil {
			continue
		}
		if t == nil {
			if t = parseTheme(name, string(b)); t == nil {
				continue
			}
		}
		t.bases = append(t.bases, base)
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
		if b, err := os.ReadFile(f); err == nil {
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
