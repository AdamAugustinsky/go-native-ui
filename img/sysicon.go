package img

import (
	"time"
)

// System icons.
//
// The icons the system shows for files, folders, volumes, applications and
// file types (the Finder's, Explorer's, the desktop's icon theme) are a
// Source like any other: requested with Cache.Request, decoded on the
// workers at the size they are shown, kept in the cache and uploaded within
// the frame budget.
//
//	key := img.Key{Hash: img.HashFileIcon(path, modTime, 0), W: 64, H: 64} // 32 pt at 2×
//	h, _ := cache.Request(key, img.FileIcon(path))
//	k.Image(1, h, ui.FitContain, 0, "")
//
// # Keys
//
// The app chooses Key.Hash, and what the hash must name is what the icon
// depends on:
//
//   - An icon of a file type depends on the type alone: HashTypeIcon(uti),
//     HashTypeIcon(ext), or one hash for FolderIcon. All files of the type
//     share one cache entry, so a list of ten thousand PDFs costs one icon.
//   - The icon of a file depends on the file: its path and its modification
//     time, for custom icons, badges, application bundles and volume labels
//     change (HashFileIcon(path, modTime, variant)). When the file changes, its new
//     hash is a new image and the old one ages out of the cache. Use
//     FileIcon only where the icon can differ from the type's (applications,
//     folders with a custom icon, volumes, documents with a thumbnail-like
//     icon); use ExtensionIcon for the rest.
//
// W and H are the size in pixels, as for every image (points × scale), so
// that a 2× display gets a sharp icon. An icon is square: it is drawn
// into the largest square inside the W×H box (with Cover, the smallest
// square that covers it), or into the one given side when the other is 0;
// with both 0 it is 128 pixels. Where the system keeps sized artwork (macOS,
// Windows, icon themes) the nearest artwork is used and scaled to fit, so
// each size is its own image (a 32 and a 64 pixel icon are not one image
// scaled). Icons are premultiplied sRGB RGBA like every other decode. They
// may be larger than the source art (an icon requested at 2048 px is the
// biggest artwork enlarged), within the usual limits: a side at most 16384
// pixels and 100 megapixels, else ErrTooLarge.
//
// # Appearance
//
// Folders, documents and volumes look the same in light and dark mode, and
// do not follow the accent colour (measured on macOS 27.2: pixel-identical
// icons across AppleInterfaceStyle and AppleAccentColor; Windows and the
// icon themes have one icon per type). Application icons on macOS 26 and
// later follow the user's icon style (Default, Dark, Clear, Tinted; with
// Automatic the system's light or dark): the icons of Calculator, Terminal
// and Finder differ between the styles (AppleIconAppearanceTheme
// RegularLight, RegularDark, TintedDark). NSWorkspace reads that setting, not
// the window's appearance. So a type icon needs no appearance in its key; the
// FileIcon of an application should have one, as HashFileIcon's variant: the
// window's appearance (0 or 1), or a counter the app bumps when the system's
// appearance changes, so that the icon is requested again and the old one
// ages out of the cache.
//
// # Platforms
//
//   - macOS: NSWorkspace iconForFile: and iconForContentType:, the best
//     artwork for the size (NSImage CGImageForProposedRect:context:hints:),
//     drawn into the sRGB bitmap. They run on the cache's workers: the
//     documentation of iconForFile: states it may be called from any thread
//     (see the notes in sysicon_darwin.go for the NSImage side).
//   - Windows: IShellItemImageFactory for a path, at exactly the requested
//     size (large sizes included); the shell image lists (SHGetFileInfo with
//     SHGFI_USEFILEATTRIBUTES, then the image list's icon) for a type
//     without a file, at 16, 32, 48 or 256 pixels scaled to the size. COM is
//     initialised on the worker's thread.
//   - Linux: the freedesktop way, without third-party code: the MIME type
//     of the extension from the shared-mime-info globs, then the icon
//     theme's PNG at the nearest size (the user's GTK theme, its Inherits,
//     hicolor). Icons that exist only as SVG are not rendered: an icon
//     without a PNG fails with ErrUnsupported, and so does a system with no
//     MIME database or icon theme.
//
// Anywhere else every system icon fails with ErrUnsupported. TypeIcon
// takes UTIs, which only macOS knows: elsewhere it understands
// public.folder and public.directory and fails with ErrUnsupported for the
// rest, so portable code asks for ExtensionIcon.
//
// A file or type that cannot be found fails with ErrRead (a path that does
// not exist) or ErrUnsupported (a type the system does not know). Nothing
// runs on the app's main thread: Request hands the work to a worker, the
// same as for an image file.

const (
	srcIconFile   uint8 = iota + srcLease + 1 // the icon of the file at path
	srcIconType                               // the icon of the UTI in path
	srcIconExt                                // the icon of the file extension in path
	srcIconFolder                             // the generic folder icon
)

// isIcon reports whether the source is a system icon.
func (s Source) isIcon() bool { return s.kind >= srcIconFile }

// FileIcon is the system icon of the file, folder, volume or application at
// path: the icon the file manager shows for it, with its custom icon or
// badge if it has one. A path that does not exist fails with ErrRead.
// Key.Hash should name the path and the modification time: HashFileIcon.
func FileIcon(path string) Source { return Source{path: path, kind: srcIconFile} }

// TypeIcon is the system icon of a file type named by its uniform type
// identifier (public.folder, com.adobe.pdf, public.plain-text), for a file
// that has no icon of its own. Only macOS knows UTIs (see the package notes
// on other systems; use ExtensionIcon there). A type the system does not
// know fails with ErrUnsupported. Key.Hash should be HashTypeIcon(uti).
func TypeIcon(uti string) Source { return Source{path: uti, kind: srcIconType} }

// ExtensionIcon is the system icon of files with the filename extension ext
// (".pdf" or "pdf", any case), without a file to look at. An extension the
// system has no icon for gets the generic document icon. Key.Hash should be
// HashTypeIcon(ext) with the extension spelled one way, lower case.
func ExtensionIcon(ext string) Source { return Source{path: ext, kind: srcIconExt} }

// FolderIcon is the system's generic folder icon. Key.Hash should be
// HashTypeIcon("public.folder") or any constant the app keeps for it.
func FolderIcon() Source { return Source{kind: srcIconFolder} }

// Domains that keep the hashes of icons apart from the hashes of images and
// from each other.
const (
	hashSeedType = 0x73797369636f6e74 // "sysicont"
	hashSeedFile = 0x73797369636f6e66 // "sysiconf"
)

// HashTypeIcon is a Key.Hash for the icon of a file type: a UTI or an
// extension (always spelled the same way: ".pdf" and "pdf" are two keys).
// It is not HashString of the same text, so type icons and other images keep
// apart; like any hash, it is 64 bits.
func HashTypeIcon(typ string) uint64 { return fnvString(hashSeedType, typ) }

// HashFileIcon is a Key.Hash for the icon of the file at path last modified
// at mod: a new modification time (a changed custom icon, a rewritten
// bundle) is a new image. Pass the zero time for an icon that does not
// change while the path stays. variant is 0, or what else the icon depends
// on, for the app's own use: the appearance for application icons (see
// Appearance).
func HashFileIcon(path string, mod time.Time, variant uint64) uint64 {
	h := fnvString(hashSeedFile, path)
	ns := uint64(0)
	if !mod.IsZero() {
		ns = uint64(mod.UnixNano())
	}
	for _, v := range [2]uint64{ns, variant} {
		for i := 0; i < 8; i++ {
			h ^= (v >> (8 * i)) & 0xff
			h *= 1099511628211
		}
	}
	return h
}

// fnvString continues an FNV-1a hash from seed (nonzero) over s.
func fnvString(seed uint64, s string) uint64 {
	h := uint64(14695981039346656037) ^ seed
	for i := 0; i < len(s); i++ {
		h ^= uint64(s[i])
		h *= 1099511628211
	}
	return h
}

// iconSide is the side of the square the icon is drawn in for the box w×h
// of a key (see the package notes on sizes), or the error code.
func iconSide(w, h int32, flags Flags) (int32, uint8) {
	side := int32(0)
	switch {
	case w > 0 && h > 0:
		side = min(w, h)
		if flags&Cover != 0 {
			side = max(w, h)
		}
	case w > 0:
		side = w
	case h > 0:
		side = h
	default:
		side = 128
	}
	if side > maxSide || int64(side)*int64(side) > maxPixels {
		return 0, errTooLarge
	}
	return side, errNone
}
