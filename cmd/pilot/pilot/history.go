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

// at is the directory at position i (ok false outside the list). The position
// itself moves only when the pane has shown that directory (goTo), so a failed
// Back or Forward leaves the list as it was.
func (h *history) at(i int) (string, bool) {
	if i < 0 || i >= h.n {
		return "", false
	}
	return h.dirs[i], true
}

// goTo makes position i the current one.
func (h *history) goTo(i int) {
	if i >= 0 && i < h.n {
		h.i = i
	}
}

// replaceCurrent puts dir in place of the current entry (the folder it names is
// gone and the pane shows an ancestor instead).
func (h *history) replaceCurrent(dir string) {
	if h.n > 0 {
		h.dirs[h.i] = dir
	}
}
