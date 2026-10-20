package platform

// focusModifiers keeps EvModifiers true across a change of key window, where
// the keys pressed or released meanwhile go to another window or app and their
// events never arrive. last is the modifiers the app was last told; held is
// what the keyboard has now, for a window that has just gained focus, and 0
// for one that has lost it (nothing is held for it any more: a modifier that
// was reported held is reported released). It queues an EvModifiers when they
// differ.
func focusModifiers(q *eventQueue, last *Mods, held Mods, t int64) {
	if held == *last {
		return
	}
	*last = held
	q.push(Event{Kind: EvModifiers, Mods: held, Time: t})
}
