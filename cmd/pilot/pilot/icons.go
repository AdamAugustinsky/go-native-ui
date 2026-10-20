package pilot

import (
	"time"

	"gonativeui/fs"
	"gonativeui/icon"
	"gonativeui/img"
	"gonativeui/theme"
	"gonativeui/ui"
	"gonativeui/widget/base"
)

// The icons of the file list are the system's: what the file manager shows
// for the file's type (img.ExtensionIcon, one image for every file of an
// extension), the generic folder (img.FolderIcon), and, where the icon belongs
// to the item and not to its type, the item's own (img.FileIcon of an
// application bundle, keyed by HashFileIcon with its path and modification
// time). They decode on the image cache's workers at the size shown, so a
// frame only asks for them (Request allocates nothing for a known image) and
// draws what is ready; until then the 16 pt square is left empty, and a type
// the system cannot draw gets the kit's symbol.

const (
	fileIconPt    = 16  // the list's icon, as Finder's image view
	maxExtIcons   = 512 // distinct extensions kept; the rest share the generic document
	maxExtLen     = 24
	maxBundleIcon = 512
)

// iconRec is one icon: its key at the scale shown and where it comes from.
type iconRec struct {
	key img.Key
	src img.Source
}

// extRec is the icon of an extension (the key is the extension's: a type icon).
type extRec struct{ iconRec }

// bundleRec is the icon of one application bundle in the folder dir.
type bundleRec struct {
	iconRec
	dir   string
	path  string
	mtime int64
}

type bundleID struct {
	pane uint8
	name uint64
}

// sysIcons is the window's file icons: the cache and what is asked of it.
type sysIcons struct {
	cache *img.Cache
	px    int32  // the icon's side in pixels at the current scale
	style uint64 // img.IconStyle(): the variant of application icons

	folder, generic, exec iconRec
	exts                  map[string]*extRec
	bundles               map[bundleID]*bundleRec
}

func newSysIcons(cache *img.Cache) *sysIcons {
	ic := &sysIcons{cache: cache, style: img.IconStyle()}
	ic.exts = make(map[string]*extRec, 64)
	ic.bundles = make(map[bundleID]*bundleRec, 16)
	return ic
}

// refreshStyle asks the system for the icon style again; a change makes every
// application icon a new image (the variant is part of its key).
func (ic *sysIcons) refreshStyle() {
	if s := img.IconStyle(); s != ic.style {
		ic.style = s
		clear(ic.bundles)
	}
}

// setScale sizes the icons for scale: a new size is a new set of keys.
func (ic *sysIcons) setScale(scale float32) {
	px := int32(fileIconPt*scale + 0.5)
	if px == ic.px {
		return
	}
	ic.px = px
	clear(ic.exts)
	clear(ic.bundles)
	key := func(h uint64) img.Key { return img.Key{Hash: h, W: px, H: px} }
	ic.folder = iconRec{key(img.HashTypeIcon("public.folder")), img.FolderIcon()}
	ic.generic = iconRec{key(img.HashTypeIcon("")), img.ExtensionIcon("")}
	ic.exec = iconRec{key(img.HashTypeIcon("public.unix-executable")), img.TypeIcon("public.unix-executable")}
}

// isBundle reports whether name is an application bundle (a folder the system
// draws with the application's own icon).
func isBundle(name []byte) bool {
	n := len(name)
	return n > 4 && name[n-4] == '.' && lower(name[n-3]) == 'a' && lower(name[n-2]) == 'p' && lower(name[n-1]) == 'p'
}

func lower(b byte) byte {
	if b >= 'A' && b <= 'Z' {
		return b + 'a' - 'A'
	}
	return b
}

// forEntry is the icon of entry e (named name) of pane p's folder: asked of the
// cache (so it is kept while it shows) and its state.
func (ic *sysIcons) forEntry(p *pane, pi int, e *fs.Entry, name []byte) (img.Handle, img.State) {
	dir := e.Kind == fs.KindDir || e.Flags&fs.FlagSymlinkDir != 0
	var rec *iconRec
	switch {
	case dir && isBundle(name):
		rec = ic.bundle(p, pi, e, name)
	case dir:
		rec = &ic.folder
	case e.Kind == fs.KindOther:
		rec = &ic.generic
	default:
		rec = ic.byName(e, name)
	}
	return ic.cache.Request(rec.key, rec.src)
}

// byName is the icon of a file by its extension, or an executable without one.
func (ic *sysIcons) byName(e *fs.Entry, name []byte) *iconRec {
	dot := -1
	for i := len(name) - 1; i > 0; i-- { // a name that starts with a dot has no extension
		if name[i] == '.' {
			dot = i
			break
		}
	}
	if dot < 0 || dot == len(name)-1 {
		if e.Flags&fs.FlagExecutable != 0 {
			return &ic.exec
		}
		return &ic.generic
	}
	ext := name[dot+1:]
	if len(ext) > maxExtLen {
		return &ic.generic
	}
	var buf [maxExtLen]byte
	for i, b := range ext {
		buf[i] = lower(b)
	}
	lo := buf[:len(ext)]
	if r, ok := ic.exts[string(lo)]; ok { // (the conversion in a lookup does not allocate)
		return &r.iconRec
	}
	if len(ic.exts) >= maxExtIcons {
		return &ic.generic
	}
	s := string(lo)
	r := &extRec{iconRec{
		key: img.Key{Hash: img.HashTypeIcon(s), W: ic.px, H: ic.px},
		src: img.ExtensionIcon(s),
	}}
	ic.exts[s] = r
	return &r.iconRec
}

// bundle is the icon of the application bundle name in pane p's folder: its
// own, keyed by path and modification time.
func (ic *sysIcons) bundle(p *pane, pi int, e *fs.Entry, name []byte) *iconRec {
	id := bundleID{uint8(pi), nameHash(name)}
	r := ic.bundles[id]
	if r != nil && r.dir == p.dir && r.mtime == e.MTime {
		return &r.iconRec
	}
	if r == nil {
		if len(ic.bundles) >= maxBundleIcon {
			return &ic.folder
		}
		r = new(bundleRec)
		ic.bundles[id] = r
	}
	r.dir, r.mtime = p.dir, e.MTime
	r.path = joinPath(p.dir, string(name))
	r.key = img.Key{Hash: img.HashFileIcon(r.path, time.Unix(0, e.MTime), ic.style), W: ic.px, H: ic.px}
	r.src = img.FileIcon(r.path)
	return &r.iconRec
}

// close stops the workers and frees the textures.
func (ic *sysIcons) close() { ic.cache.Close(time.Second) }

// fileIcon builds the icon of an entry in the current parent, a row rowH
// high: the system's once decoded, the kit's symbol when the system has none,
// empty while it decodes. hidden entries are drawn at half strength, as
// Finder's.
func (pl *Pilot) fileIcon(c *ui.Context, pi int, ent *fs.Entry, name []byte, side, rowH float32) {
	k := pl.bk
	h, st := pl.sys.forEntry(pl.panes[pi], pi, ent, name)
	key := c.ParentKey()
	hidden := ent.Flags&fs.FlagHidden != 0
	if hidden {
		c.PushOpacity(0.5)
	}
	switch st {
	case img.Ready:
		theme.At(c, 0, theme.Snap((rowH-side)/2), side, side)
		c.NextFlags(base.Floating)
		k.Image(1, h, ui.FitContain, 0, "")
	case img.Failed:
		id := icon.File
		if ent.Kind == fs.KindDir || ent.Flags&fs.FlagSymlinkDir != 0 {
			id = icon.Folder
		}
		if f, disp := k.Icons().Prepare(c, id, k.Theme().Colors[theme.Label2]); f != 0 {
			iw, ih := k.IconSize(id)
			theme.At(c, theme.Snap((side-iw)/2), theme.Snap((rowH-ih)/2), iw, ih)
			c.BoxKeyed(f|base.Floating, ui.KeyN(key, 2), disp)
		}
	}
	if hidden {
		c.PopOpacity()
	}
}

// IconsPending reports whether system icons are still being decoded: a picture
// taken now would lack them (a window is woken by the workers when they finish;
// an offscreen scene has to wait).
func (pl *Pilot) IconsPending() bool {
	return pl.sys != nil && pl.sys.cache.Stats().Pending > 0
}
