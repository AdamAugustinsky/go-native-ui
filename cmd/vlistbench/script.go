package main

import "gonativeui/ui"

// benchScript is the synthetic input of -bench, a session of period frames
// that repeats (each measured window holds the same work, D27). The pointer
// rests over the timeline; the trackpad sends precise deltas.
//
//	0-119    stuck to the bottom; a message arrives every 8 frames
//	120-299  scroll up 70 px a frame through unmeasured history
//	200      2 messages above the top one are deleted for everyone, and 30
//	         older ones are backfilled further up (the view must not move)
//	300      jump to a reply in the middle of the million (centred)
//	300-419  scroll down 50 px a frame
//	330      a distant move (a quarter down to three quarters down): rebuilt
//	         in slices while the scrolling goes on
//	420      jump to the top: history pages load as the view nears it
//	420-509  scroll up 60 px a frame, loading more history (no jumps)
//	510      jump to the end: stuck to the bottom again
//	510-599  messages arrive every 8 frames, the view pinned
//
// The messages are selectable (a ui.SelectionScope over the list): a click
// at 2 focuses the scope and ⌘A at 4 selects all million, so the session
// scrolls, edits and draws a selection that spans the list; at 60 a press
// and a drag above the list's top auto-scrolls up through history for 20
// frames, and ⌘A at 84 selects everything again.
//
// The script ends at total.
func (ch *chat) benchScript(warm, period, total int) func(frame int, in *ui.Input) bool {
	return func(f int, in *ui.Input) bool {
		if f >= total {
			return false
		}
		if f >= warm {
			ch.counting = true
		}
		ev := f % period
		in.Inside, in.Focused = true, true
		in.MouseX, in.MouseY = 200, 400
		prim := ui.PrimaryMod()
		switch {
		case ev == 2, ev == 60:
			in.SetButton(ui.ButtonLeft, true, 1)
		case ev == 3, ev == 81:
			in.SetButton(ui.ButtonLeft, false, 0)
		case ev == 4, ev == 84:
			in.PushKey(ui.KeyEvent{Code: 'A', Mods: prim, Down: true})
		case ev > 60 && ev < 81:
			in.MouseY = 2 // above the list: it scrolls up by itself
		}
		switch {
		case ev >= 120 && ev < 300:
			in.AddScroll(0, 70, true)
		case ev >= 300 && ev < 420:
			in.AddScroll(0, -50, true)
		case ev >= 420 && ev < 510:
			in.AddScroll(0, 60, true)
		}
		switch {
		case ev < 120 && ev%8 == 7, ev >= 510 && ev%8 == 7:
			ch.pre = (*chat).arrive
		case ev == 200:
			ch.pre = (*chat).editAbove
		case ev == 300:
			ch.jumpTo = ch.st.Len() / 2
		case ev == 330:
			ch.pre = (*chat).distantMove
		case ev == 420:
			ch.jumpTop = true
		case ev == 510:
			ch.jumpEnd = true
		}
		return true
	}
}

// editAbove deletes two messages just above the one at the top and backfills
// older ones further up, watching that the view stays.
func (ch *chat) editAbove() {
	id, _ := ch.st.Anchor()
	i, ok := ch.st.IndexOf(id)
	if !ok || i < 60 {
		return
	}
	ch.watch(ch.a.UI)
	ch.st.Splice(i-3, 2, nil)
	for k := 0; k < 30; k++ {
		ch.backfill++
		ch.buf[k] = 1<<62 + ch.backfill
	}
	ch.st.Splice(i-50, 0, ch.buf[:30])
}

// distantMove moves the message a quarter of the way down to three quarters
// of the way down: on a million items, rebuilt in slices.
func (ch *chat) distantMove() {
	n := ch.st.Len()
	ch.st.Move(n/4, 1, 3*n/4)
}
