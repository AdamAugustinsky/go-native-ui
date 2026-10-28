package platform

import (
	"encoding/binary"
	"strconv"
	"strings"
	"unicode/utf16"
)

// The parts of drag and drop that no OS owns (the contract is drag.go). They
// have no build tag so that they are tested on every OS: the Windows (OLE),
// X11 (XDND) and Wayland (data device) back ends use them, and macOS shares the
// answer and the helpers for a drag of its own session.

// answerDrag is what the window answers to the system about a drag at (x, y):
// the handler's answer, held to what the drag offers and the source allows. A
// window without a handler accepts every kind with the first operation the
// source allows.
func answerDrag(h DragHandler, x, y float32, offered DragKinds, allowed DragOp) DragAnswer {
	if h == nil {
		return DragAnswer{Op: Pick(DragOpAll, allowed), Accept: DragAny}
	}
	a := h(x, y, offered, allowed)
	if a.Accept&offered == 0 {
		return DragAnswer{}
	}
	if a.Op&^allowed != 0 || a.Op&(a.Op-1) != 0 { // not one of the allowed operations
		a.Op = Pick(a.Op, allowed)
	}
	return a
}

// dropKind is the one kind a drop delivers: the first of files, image and text
// in take.
func dropKind(take DragKinds) DragKinds {
	switch {
	case take&DragFiles != 0:
		return DragFiles
	case take&DragImage != 0:
		return DragImage
	}
	return DragText
}

// pushDropData queues the EvDrop e with data (a failed drop when there is
// none, or when it is over MaxPaste); n, if not zero, is the number of items.
func (q *eventQueue) pushDropData(e Event, data []byte, n int) {
	if n != 0 {
		e.SelStart = int32(n)
	}
	if len(data) == 0 || len(data) > MaxPaste {
		e.Flags |= FlagFailed
	} else {
		off := len(q.text)
		q.text = append(q.text, data...)
		e.Text = TextRef{uint32(off), uint32(len(data))}
	}
	q.push(e)
}

// maxDropWorkers bounds the image conversions in flight; a drop past it fails.
const maxDropWorkers = 4

// specHasPromise reports whether a spec has a file promise among its items.
func specHasPromise(s *DragSpec) bool {
	for i := range s.Items {
		if s.Items[i].Promise != nil {
			return true
		}
	}
	return false
}

// ownPaths and ownText are what a drag of our own session carries, from its
// spec: the paths (each ended by a NUL) and how many, and the first text.
func ownPaths(items []DragItem, limit int) (out []byte, n int) {
	for i := range items {
		if p := items[i].Path; p != "" {
			if len(out)+len(p)+1 > limit {
				return nil, 0
			}
			out = append(append(out, p...), 0)
			n++
		}
	}
	return out, n
}

func ownText(items []DragItem, limit int) []byte {
	for i := range items {
		if t := items[i].Text; t != "" {
			if len(t) > limit {
				return nil
			}
			return []byte(t)
		}
	}
	return nil
}

// ownPNG is the first image of a session's items, nil if it has none.
func ownPNG(items []DragItem) []byte {
	for i := range items {
		if len(items[i].PNG) > 0 {
			return items[i].PNG
		}
	}
	return nil
}

// specKinds is the kinds a spec's items make: what a drop target can ask for.
func specKinds(items []DragItem) (k DragKinds) {
	for i := range items {
		switch it := &items[i]; {
		case it.Path != "":
			k |= DragFiles
		case it.Text != "":
			k |= DragText
		case len(it.PNG) > 0:
			k |= DragImage
		}
	}
	return k
}

// ---- operations ----

// The OLE drop effects (DROPEFFECT_*), which Windows reports as a set.
const (
	effectNone = 0
	effectCopy = 1
	effectMove = 2
	effectLink = 4
)

// opsOfEffects is the set of operations the DROPEFFECT_* bits in e allow.
func opsOfEffects(e uint32) (op DragOp) {
	if e&effectCopy != 0 {
		op |= DragOpCopy
	}
	if e&effectLink != 0 {
		op |= DragOpLink
	}
	if e&effectMove != 0 {
		op |= DragOpMove
	}
	return op
}

// effectsOfOps is the DROPEFFECT_* bits of a set of operations.
func effectsOfOps(op DragOp) (e uint32) {
	if op&DragOpCopy != 0 {
		e |= effectCopy
	}
	if op&DragOpLink != 0 {
		e |= effectLink
	}
	if op&DragOpMove != 0 {
		e |= effectMove
	}
	return e
}

// ---- CF_HDROP and text/uri-list, built ----

// buildDropFiles builds the DROPFILES block (CF_HDROP) for paths: the 20-byte
// header, then each path as UTF-16 ended by a NUL unit, and an empty path to
// end the list. A path with a NUL inside is skipped.
func buildDropFiles(paths []string) []byte {
	b := make([]byte, 20, 20+64*len(paths))
	binary.LittleEndian.PutUint32(b, 20) // pFiles: the list follows the header
	binary.LittleEndian.PutUint32(b[16:], 1)
	put := func(u uint16) { b = binary.LittleEndian.AppendUint16(b, u) }
	n := 0
	for _, p := range paths {
		if p == "" || containsNUL(p) {
			continue
		}
		for _, u := range utf16.Encode([]rune(p)) {
			put(u)
		}
		put(0)
		n++
	}
	put(0)
	if n == 0 {
		put(0) // an empty list is two NUL units
	}
	return b
}

func containsNUL(s string) bool {
	for i := 0; i < len(s); i++ {
		if s[i] == 0 {
			return true
		}
	}
	return false
}

// appendURIList appends the text/uri-list of the file paths (RFC 2483: one
// file: URI per line, CRLF-ended, the path percent-escaped).
func appendURIList(dst []byte, paths []string) []byte {
	const hex = "0123456789ABCDEF"
	for _, p := range paths {
		if p == "" || containsNUL(p) {
			continue
		}
		dst = append(dst, "file://"...)
		for i := 0; i < len(p); i++ {
			switch c := p[i]; {
			case c >= 'a' && c <= 'z', c >= 'A' && c <= 'Z', c >= '0' && c <= '9',
				c == '/', c == '-', c == '.', c == '_', c == '~':
				dst = append(dst, c)
			default:
				dst = append(dst, '%', hex[c>>4], hex[c&15])
			}
		}
		dst = append(dst, '\r', '\n')
	}
	return dst
}

// ownFilePaths are the paths of a session's file items.
func ownFilePaths(items []DragItem) (paths []string) {
	for i := range items {
		if p := items[i].Path; p != "" {
			paths = append(paths, p)
		}
	}
	return paths
}

// dndActionFor is the operation a drag out proposes, of the ops its source
// allows, from the modifier keys held: Shift moves, Control copies, both link
// (as GTK and Qt do), and with none the first of copy, link and move.
func dndActionFor(ops DragOp, mods Mods) DragOp {
	switch {
	case mods&ModShift != 0 && mods&ModCtrl != 0 && ops&DragOpLink != 0:
		return DragOpLink
	case mods&ModShift != 0 && ops&DragOpMove != 0:
		return DragOpMove
	case mods&ModCtrl != 0 && ops&DragOpCopy != 0:
		return DragOpCopy
	}
	return Pick(ops, ops)
}

// gtkSettingInt reads an integer setting of the [Settings] group of a GTK
// settings.ini (key=value lines); ok is false when it is not there or not a
// number.
func gtkSettingInt(ini, key string) (n int, ok bool) {
	in := false
	for _, line := range strings.Split(ini, "\n") {
		line = strings.TrimSpace(line)
		switch {
		case line == "" || line[0] == '#' || line[0] == ';':
		case line[0] == '[':
			in = line == "[Settings]"
		case in:
			if k, v, found := strings.Cut(line, "="); found && strings.TrimSpace(k) == key {
				n, err := strconv.Atoi(strings.TrimSpace(v))
				return n, err == nil
			}
		}
	}
	return 0, false
}
