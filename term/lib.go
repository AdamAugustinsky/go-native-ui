package term

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"sync"
	"unsafe"

	"gonativeui/clib"
	"gonativeui/internal/ghosttyvt"
)

// ErrUnavailable is returned by [New] and [Available] when libghostty-vt
// cannot be loaded: the library is not where [LibraryPaths] looks, it fails
// to load, it lacks a function term calls, or its type manifest does not
// match the layouts term was written for (an API change upstream). The
// wrapped error says which, with the paths tried.
var ErrUnavailable = errors.New("term: libghostty-vt is not available")

// EnvLibrary is the environment variable that points term at a
// libghostty-vt for development: the library file, or a directory holding
// it (such as tools/libghostty's .cache/libghostty-vt/<commit>/<goos>-<goarch>).
const EnvLibrary = "GNUI_LIBGHOSTTY_VT"

// LibraryName is the library's file name on this system: what an app ships
// beside its executable (Windows, Linux) or in its bundle's
// Contents/Frameworks (macOS; gnui.json's nativeLibs).
var LibraryName = ghosttyvt.FileName(runtime.GOOS)

// lib is the process's one load of libghostty-vt.
var lib struct {
	once   sync.Once
	err    error
	path   string
	h      *clib.Lib
	fn     [ghosttyvt.NumFuncs]clib.Func
	cell   cellLayout
	cbs    callbacks
	libVer string
}

// cellLayout is where each field of a raw GhosttyCell lies, read from the
// library's type manifest: the header leaves the bit positions out of the
// ABI, so a renderer that reads a whole row at once (as rexui.Terminal
// does) must take them from the library it loaded.
type cellLayout struct {
	tag, cp, palette, r, g, b, style, wide, link bitField
}

type bitField struct{ lsb, width uint8 }

func (f bitField) get(v uint64) uint64 { return v >> f.lsb & (1<<f.width - 1) }

// LibraryPaths are the places term looks for libghostty-vt, in order: beside
// the executable, in the app bundle's library folder (macOS
// Contents/Frameworks, Linux ../lib), then $GNUI_LIBGHOSTTY_VT.
func LibraryPaths() []string {
	var ps []string
	if exe, err := os.Executable(); err == nil {
		if r, err := filepath.EvalSymlinks(exe); err == nil {
			exe = r
		}
		ps = append(ps, filepath.Join(filepath.Dir(exe), LibraryName))
	}
	if b := clib.BundleFramework(LibraryName); filepath.IsAbs(b) && (len(ps) == 0 || b != ps[0]) {
		ps = append(ps, b)
	}
	if e := os.Getenv(EnvLibrary); e != "" {
		if st, err := os.Stat(e); err == nil && st.IsDir() {
			e = filepath.Join(e, LibraryName)
		}
		if a, err := filepath.Abs(e); err == nil {
			e = a
		}
		ps = append(ps, e)
	}
	return ps
}

// Available loads libghostty-vt if it is not loaded yet and reports whether
// it is usable: nil, or an error wrapping [ErrUnavailable]. Only the first
// call looks; later calls return the same answer.
func Available() error {
	lib.once.Do(load)
	return lib.err
}

// LibraryPath is the file libghostty-vt was loaded from ("" before a
// successful [Available] or [New]).
func LibraryPath() string {
	if Available() != nil {
		return ""
	}
	return lib.path
}

func load() {
	var tried []string
	var last error
	for _, p := range LibraryPaths() {
		if _, err := os.Stat(p); err != nil {
			tried = append(tried, p)
			continue
		}
		h, err := clib.Open(p)
		if err != nil {
			tried, last = append(tried, p), err
			continue
		}
		if err := bind(h); err != nil {
			lib.err = fmt.Errorf("%w: %s: %v", ErrUnavailable, p, err)
			return
		}
		lib.h, lib.path = h, p
		return
	}
	if last == nil {
		last = errors.New("not found")
	}
	lib.err = fmt.Errorf("%w (%s): looked for %s in %s: %v", ErrUnavailable, ghosttyvt.Short, LibraryName, strings.Join(tried, ", "), last)
}

// bind looks every function up and checks the library's type manifest.
func bind(h *clib.Lib) error {
	for i, n := range ghosttyvt.Names {
		f, err := h.Func(n)
		if err != nil {
			return err
		}
		lib.fn[i] = f
	}
	m, err := manifest()
	if err != nil {
		return fmt.Errorf("type manifest: %v", err)
	}
	if err := checkSizes(m); err != nil {
		return err
	}
	cl, err := m.cellLayout()
	if err != nil {
		return err
	}
	lib.cell = cl
	lib.libVer = m.Version
	return newCallbacks(&lib.cbs)
}

// typeManifest is the part of ghostty_type_json term reads.
type typeManifest struct {
	Version string                     `json:"library_version"`
	Types   map[string]json.RawMessage `json:"types"`
}

type typeDesc struct {
	Kind   string                     `json:"kind"`
	Size   uintptr                    `json:"size"`
	Fields map[string]json.RawMessage `json:"fields"`
	Bits   map[string]json.RawMessage `json:"bits"`
	Values map[string]int64           `json:"values"`
}

type bitDesc struct {
	LSB   uint8                      `json:"lsb"`
	Width uint8                      `json:"width"`
	Arms  map[string]json.RawMessage `json:"arms"`
	Bits  map[string]json.RawMessage `json:"bits"`
}

// manifest reads the library's type manifest (once, at load).
func manifest() (*typeManifest, error) {
	p := clib.NewCall().Do(lib.fn[ghosttyvt.TypeJSON]).Ptr()
	if p == nil {
		return nil, errors.New("ghostty_type_json returned NULL")
	}
	n := 0
	for *(*byte)(unsafe.Add(p, n)) != 0 {
		n++
	}
	var m typeManifest
	if err := json.Unmarshal(unsafe.Slice((*byte)(p), n), &m); err != nil {
		return nil, err
	}
	return &m, nil
}

func (m *typeManifest) desc(name string) (*typeDesc, error) {
	raw, ok := m.Types[name]
	if !ok {
		return nil, fmt.Errorf("the library has no type %s", name)
	}
	var d typeDesc
	if err := json.Unmarshal(raw, &d); err != nil {
		return nil, fmt.Errorf("%s: %v", name, err)
	}
	return &d, nil
}

// checkSizes compares the size of every mirrored struct with the library's.
func checkSizes(m *typeManifest) error {
	for name, size := range abiStructs {
		d, err := m.desc(name)
		if err != nil {
			return err
		}
		if d.Size != size {
			return fmt.Errorf("%s is %d bytes in the library, %d in term (API changed: rebuild at the pinned commit %s)", name, d.Size, size, ghosttyvt.Short)
		}
	}
	return nil
}

// cellLayout reads GhosttyCell's bit fields.
func (m *typeManifest) cellLayout() (cellLayout, error) {
	d, err := m.desc("GhosttyCell")
	if err != nil {
		return cellLayout{}, err
	}
	field := func(bits map[string]json.RawMessage, path ...string) (bitDesc, error) {
		var b bitDesc
		for i, p := range path {
			raw, ok := bits[p]
			if !ok {
				return b, fmt.Errorf("GhosttyCell has no field %s", strings.Join(path[:i+1], "."))
			}
			if err := json.Unmarshal(raw, &b); err != nil {
				return b, err
			}
			if i+1 < len(path) {
				if b.Arms != nil {
					bits = b.Arms
				} else {
					bits = b.Bits
				}
			}
		}
		return b, nil
	}
	var l cellLayout
	var content bitDesc
	for _, f := range []struct {
		dst  *bitField
		path []string
	}{
		{&l.tag, []string{"content_tag"}},
		{&l.style, []string{"style_id"}},
		{&l.wide, []string{"wide"}},
		{&l.link, []string{"hyperlink"}},
	} {
		b, err := field(d.Bits, f.path...)
		if err != nil {
			return l, err
		}
		*f.dst = bitField{b.LSB, b.Width}
	}
	content, err = field(d.Bits, "content")
	if err != nil {
		return l, err
	}
	for _, f := range []struct {
		dst  *bitField
		path []string
	}{
		{&l.cp, []string{"CODEPOINT", "codepoint"}},
		{&l.palette, []string{"BG_COLOR_PALETTE", "index"}},
		{&l.r, []string{"BG_COLOR_RGB", "r"}},
		{&l.g, []string{"BG_COLOR_RGB", "g"}},
		{&l.b, []string{"BG_COLOR_RGB", "b"}},
	} {
		b, err := field(content.Arms, f.path...)
		if err != nil {
			return l, err
		}
		*f.dst = bitField{content.LSB + b.LSB, b.Width}
	}
	return l, nil
}
