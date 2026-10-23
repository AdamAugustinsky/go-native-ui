package ui

import "testing"

func TestTooltipRemovalWakeAndReuse(t *testing.T) {
	for _, native := range []bool{false, true} {
		name := "drawn"
		if native {
			name = "native"
		}
		t.Run(name, func(t *testing.T) {
			h := newHarness(t, 100, 100)
			h.c.SetNativeTooltips(native)
			// No pointer hover: removal may still request one follow-up,
			// but neither native declarations nor recycled slots may do so.
			h.in.Inside = false
			var owner, replacement ID
			buildOwner := func(c *Context) {
				owner = c.Box(Hoverable, "owner")
				if native {
					c.NativeTooltip(owner, "Tip")
				} else {
					c.TooltipTarget(owner)
				}
			}
			buildReplacement := func(c *Context) {
				replacement = c.Box(Hoverable, "replacement")
			}
			empty := func(c *Context) {}
			cycle := func() {
				h.frame(buildOwner)
				h.frame(empty) // model change removes the owner
				if h.c.Animating() != !native || h.c.WakeAfter() != 0 {
					t.Fatal("removal must request one frame only for a drawn tooltip")
				}
				// Reuse the freed slot during the follow-up (or a model
				// change in the native case); it must not inherit the tip.
				h.frame(buildReplacement)
				if replacement != owner {
					t.Fatal("test did not exercise the freed tooltip slot")
				}
				if h.c.Animating() || h.c.WakeAfter() != 0 {
					t.Fatal("reused slot requested another frame")
				}
				h.frame(empty) // removing the replacement must not wake either
				if h.c.Animating() || h.c.WakeAfter() != 0 {
					t.Fatal("plain replacement retained a tooltip declaration")
				}
			}
			cycle()
			if a := testing.AllocsPerRun(100, cycle); a != 0 {
				t.Fatalf("tooltip removal and slot reuse: %v allocations", a)
			}
		})
	}
}
