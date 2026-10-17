package pilot

// histMax bounds a pane's navigation history; the oldest entries drop out.
const histMax = 64

// history is a pane's back/forward list: the directories visited, and where
// in it the pane is. Entries are only added on navigation (a user action).
type history struct {
	dirs [histMax]string
	n, i int
}

func (h *history) canBack() bool    { return h.i > 0 }
func (h *history) canForward() bool { return h.i+1 < h.n }

// visit records dir as the new current entry, dropping what was forward of it.
func (h *history) visit(dir string) {
	if h.n > 0 {
		if h.dirs[h.i] == dir {
			return
		}
		h.n = h.i + 1
	}
	if h.n == histMax {
		copy(h.dirs[:], h.dirs[1:])
		h.n--
	}
	h.dirs[h.n] = dir
	h.n++
	h.i = h.n - 1
	for j := h.n; j < histMax; j++ {
		h.dirs[j] = ""
	}
}

// back and forward move the position and return the directory there.
func (h *history) back() (string, bool) {
	if !h.canBack() {
		return "", false
	}
	h.i--
	return h.dirs[h.i], true
}

func (h *history) forward() (string, bool) {
	if !h.canForward() {
		return "", false
	}
	h.i++
	return h.dirs[h.i], true
}
