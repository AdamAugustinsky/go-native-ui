package pilot

import (
	"strconv"
	"strings"
	"sync/atomic"
	"time"

	"gonativeui/fs"
)

// Synthetic directories: every entry derives from its index, so any size is
// available at once without a disk. They serve the benchmark through the real
// fs.Service (fs.NewServiceWith), so a "refresh" takes the same path through
// the pool, the ring, Release and the reconcile as a watched directory.

func mix64(r uint64) uint64 {
	z := r*0x9E3779B97F4A7C15 + 0xD1B54A32D192ED03
	z = (z ^ z>>30) * 0xBF58476D1CE4E5B9
	z = (z ^ z>>27) * 0x94D049BB133111EB
	return z ^ z>>31
}

var (
	synthExts  = [...]string{".txt", ".go", ".png", ".md", ".dat", ".json", ".zip", ".pdf"}
	synthNames = [...]string{"日本語のメモ", "Ünïcödé", "файл", "résumé", "😀 emoji", "Ελληνικά", "文件"}
)

// synthDir is a synthetic directory path of n entries: "/synth/<n>".
func synthDir(n int) string { return "/synth/" + strconv.Itoa(n) }

// SynthSource serves synthetic directories ("/synth/<n>") for fs.NewServiceWith.
// Bump changes what a later listing contains (some entries gone, some new), so
// a relist differs from the last one as a changed directory would.
type SynthSource struct{ variant atomic.Int32 }

// Bump makes the next listings differ from the previous ones.
func (s *SynthSource) Bump() { s.variant.Add(1) }

// List fills l; it is the list function of the Service.
func (s *SynthSource) List(path string, l *fs.Listing) error {
	n, err := strconv.Atoi(strings.TrimPrefix(path, "/synth/"))
	if err != nil || n < 0 {
		return fs.ErrUnsupported
	}
	FillSynth(l, n, int(s.variant.Load()))
	return nil
}

// FillSynth appends n synthetic entries to l (variant 0) or a changed version of them.
// Once l's buffers have grown it allocates nothing.
func FillSynth(l *fs.Listing, n, variant int) {
	base := time.Date(2026, 10, 17, 12, 0, 0, 0, time.UTC).UnixNano()
	skip := -1
	if variant > 0 {
		skip = variant % 997
	}
	for i := 0; i < n; i++ {
		if skip >= 0 && i%997 == skip {
			continue // deleted since the last listing
		}
		h := mix64(uint64(i))
		e := fs.Entry{MTime: base - int64(h>>20%(7*365*24*3600))*1e9}
		kind := h >> 56 % 100
		var p1, p2, ext string
		switch {
		case kind < 10: // folders
			e.Kind, p1 = fs.KindDir, "dir_"
		case kind < 12: // symlinks, a third of them to folders
			e.Kind, p1 = fs.KindSymlink, "link_"
			if h>>8%3 == 0 {
				e.Flags |= fs.FlagSymlinkDir
			}
			e.Size = int64(h >> 40 % 200)
		case kind < 15: // executables
			e.Flags |= fs.FlagExecutable
			e.Size = int64(h >> 30 % (8 << 20))
			p1 = "run_"
		case kind == 15:
			e.Kind, p1 = fs.KindOther, "dev_"
		default:
			e.Size = int64((h >> 34) >> (h % 22))
			ext = synthExts[h>>16%uint64(len(synthExts))]
			p1 = "file_"
			if h>>24%20 == 0 { // some names are not ASCII
				p1, p2 = synthNames[h>>28%uint64(len(synthNames))], "_"
			}
		}
		if h>>48%64 == 0 {
			e.Flags |= fs.FlagHidden
		}
		off := len(l.Names)
		if e.Flags&fs.FlagHidden != 0 {
			l.Names = append(l.Names, '.')
		}
		l.Names = append(l.Names, p1...)
		l.Names = append(l.Names, p2...)
		l.Names = appendPadded(l.Names, i, 7)
		l.Names = append(l.Names, ext...)
		e.NameOff, e.NameLen = uint32(off), uint16(len(l.Names)-off)
		l.Entries = append(l.Entries, e)
	}
	if variant > 0 { // a few new files
		for j := 0; j < 5; j++ {
			off := len(l.Names)
			l.Names = append(l.Names, "new_"...)
			l.Names = strconv.AppendInt(l.Names, int64(variant), 10)
			l.Names = append(l.Names, '_')
			l.Names = strconv.AppendInt(l.Names, int64(j), 10)
			l.Entries = append(l.Entries, fs.Entry{NameOff: uint32(off), NameLen: uint16(len(l.Names) - off),
				Size: int64(1000 * (j + 1)), MTime: base})
		}
	}
}

// appendPadded appends i with at least width digits.
func appendPadded(b []byte, i, width int) []byte {
	n := 1
	for x := i; x >= 10; x /= 10 {
		n++
	}
	for ; n < width; n++ {
		b = append(b, '0')
	}
	return strconv.AppendInt(b, int64(i), 10)
}
