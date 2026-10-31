package gpu

import "math"

// Cover is a part of the target, in whole pixels, that the quads from At on
// paint opaque, every pixel of it, before the list's next backdrop: nothing
// drawn before At shows there. A pane whose fill is opaque declares one
// (ui.Context.NextCover), and the shadows, field and tints drawn under it
// need not be shaded where it lies.
type Cover struct {
	Rect [4]float32 // x0, y0, x1, y1 in whole pixels
	At   int        // the index in Quads of the first quad that paints it
}

// coverPieces is the most pieces a quad is cut into around covers; a quad
// that would need more is kept whole.
const coverPieces = 8

// CullCovered rewrites l.Quads without what l.Covers hide: a quad wholly
// under covers is dropped, and one partly under them is cut into the same
// quad clipped to the pieces outside them, along whole pixels, so no pixel
// that shows is computed otherwise (an integer clip edge has a coverage of
// exactly 0 or 1). A backdrop reads what was drawn before it, so a cover
// whose quads begin after a backdrop hides nothing drawn before that
// backdrop. scratch is reused between frames; l.Covers must be in order of
// At, as ui adds them, and their At are moved to the rewritten list. It
// returns the quads dropped and cut.
func CullCovered(l *DrawList, scratch *[]Quad) (dropped, cut int) {
	if len(l.Covers) == 0 {
		return 0, 0
	}
	qs, cs := l.Quads, l.Covers
	out := (*scratch)[:0]
	first := 0   // the first cover with At > i
	nextBd := -1 // the first backdrop after i, len(qs) for none
	for i := range qs {
		q := &qs[i]
		for first < len(cs) && cs[first].At <= i {
			cs[first].At = len(out) // where its quads begin in the list it leaves
			first++
		}
		if nextBd <= i {
			nextBd = i + 1
			for nextBd < len(qs) && qs[nextBd].Mode != ModeBackdrop {
				nextBd++
			}
		}
		if q.Mode == ModeBackdrop || first == len(cs) {
			out = append(out, *q)
			continue
		}
		b := coverBounds(q)
		var ps [coverPieces + 3][4]float32
		ps[0] = b
		n := 1
		hit, over := false, false
		for k := first; k < len(cs) && cs[k].At <= nextBd && !over; k++ {
			c := cs[k].Rect
			for j := 0; j < n && !over; {
				p := ps[j]
				if p[0] >= c[2] || p[2] <= c[0] || p[1] >= c[3] || p[3] <= c[1] {
					j++
					continue
				}
				hit = true
				// p less c: above, below, left, right of it
				var add [4][4]float32
				m := 0
				if p[1] < c[1] {
					add[m], m = [4]float32{p[0], p[1], p[2], c[1]}, m+1
				}
				if p[3] > c[3] {
					add[m], m = [4]float32{p[0], c[3], p[2], p[3]}, m+1
				}
				y0, y1 := max(p[1], c[1]), min(p[3], c[3])
				if p[0] < c[0] {
					add[m], m = [4]float32{p[0], y0, c[0], y1}, m+1
				}
				if p[2] > c[2] {
					add[m], m = [4]float32{c[2], y0, p[2], y1}, m+1
				}
				if n-1+m > coverPieces {
					over = true
					break
				}
				ps[j] = ps[n-1] // p is replaced by its pieces, which no earlier cover meets
				n--
				for _, a := range add[:m] {
					ps[n] = a
					n++
				}
				// ps[j] is now another piece: look at it before moving on;
				// the new pieces at the end stay outside c
			}
		}
		switch {
		case !hit || over:
			out = append(out, *q)
		case n == 0:
			dropped++
		default:
			cut++
			for _, p := range ps[:n] {
				// the edges on a cover are whole pixels (hard clip edges);
				// the others are the quad's own: its clip's, or beyond its
				// geometry
				clip := p
				if k := q.Clip; k != [4]float32{} {
					clip = [4]float32{max(clip[0], k[0]), max(clip[1], k[1]), min(clip[2], k[2]), min(clip[3], k[3])}
				} else {
					if p[0] == b[0] {
						clip[0] = b[0] - 1
					}
					if p[1] == b[1] {
						clip[1] = b[1] - 1
					}
					if p[2] == b[2] {
						clip[2] = b[2] + 1
					}
					if p[3] == b[3] {
						clip[3] = b[3] + 1
					}
				}
				q2 := *q
				q2.Clip = clip
				out = append(out, q2)
			}
		}
	}
	for ; first < len(cs); first++ { // covers after the last quad
		cs[first].At = len(out)
	}
	*scratch = l.Quads
	l.Quads = out
	return dropped, cut
}

// coverBounds is the part of the target q's geometry reaches, as the vertex
// stage builds it (vs_main): its rect grown by the fringe or the shadow's
// tail, inside its clip and before its fade's end, rounded out to whole
// pixels.
func coverBounds(q *Quad) [4]float32 {
	pad := float32(0)
	switch {
	case q.Mode == ModeShape && q.Soft > 0:
		pad = float32(math.Ceil(3*float64(q.Soft))) + 1
	case q.Mode == ModeShape || q.Radii != [4]float32{}:
		pad = 1
	}
	x0, y0, x1, y1 := q.Rect[0]-pad, q.Rect[1]-pad, q.Rect[2]+pad, q.Rect[3]+pad
	if c := q.Clip; c != [4]float32{} {
		x0, y0, x1, y1 = max(x0, c[0]-0.5), max(y0, c[1]-0.5), min(x1, c[2]+0.5), min(y1, c[3]+0.5)
	}
	if f := q.FadeX; f != [2]float32{} {
		x1 = min(x1, f[1]+0.5)
	}
	return [4]float32{float32(math.Floor(float64(x0))), float32(math.Floor(float64(y0))),
		float32(math.Ceil(float64(x1))), float32(math.Ceil(float64(y1)))}
}
