package base

import "gonativeui/ui"

// DefaultTooltipDelayNs is how long the pointer rests on a component before
// its tooltip shows.
const DefaultTooltipDelayNs = 500e6

// tipState is the hover timer of the tooltip: the component the pointer rests
// on and since when.
type tipState struct {
	key   ui.Key
	since int64
}

// TooltipKeyBase keys the tooltip overlays.
var TooltipKeyBase = ui.HashString(KeyRoot, "tooltip")

// TooltipDue reports whether the tooltip of component id should show in this
// frame: the pointer has rested on it for TooltipDelayNs, no button is down and
// no popup is open. Until then it asks to be woken when the delay passes. id
// must have been built in this frame and take pointer input. key is id's key,
// which keys the tooltip's overlay. Disabled components show tooltips too,
// without becoming hot or taking input. A hovered descendant counts too;
// when tooltips nest, the innermost declared tooltip wins.
func (k *Kit) TooltipDue(id ui.ID) (key ui.Key, due bool) {
	c := k.c
	if id == 0 {
		return 0, false
	}
	key = c.KeyOf(id)
	now := c.Input().Time
	if c.Input().FocusLost || !c.Input().Focused {
		k.tip.key = 0
		return key, false
	}
	if !c.TooltipTarget(id) {
		if k.tip.key == key {
			k.tip.key = 0
		}
		return key, false
	}
	pressed := false
	for i, down := range c.Input().Down {
		pressed = pressed || down || c.Input().Pressed[i]
	}
	if pressed || c.ActiveKey() != 0 || k.pop.IsOpen() { // pressed, or a menu is open: start over
		k.tip.key, k.tip.since = key, now
		return key, false
	}
	if k.tip.key != key {
		k.tip.key, k.tip.since = key, now
	}
	delay := k.TooltipDelayNs
	if delay == 0 {
		delay = DefaultTooltipDelayNs
	}
	if now == 0 {
		return key, false // without a clock a delay cannot pass
	}
	if el := now - k.tip.since; el < delay {
		c.WakeIn(delay - el)
		return key, false
	}
	return key, true
}
