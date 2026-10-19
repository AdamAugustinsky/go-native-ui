//go:build darwin

package platform

import (
	"testing"

	"gonativeui/internal/objc"
)

// The screenshot hook releases every accessibility reference it owns (the
// application element, the bar, its children, the item's position and
// size), on each way out, and none it does not own (the bar item).
func TestOpenBarMenuReleasesWhatItOwns(t *testing.T) {
	type fake struct {
		missing string // the attribute that is not there
		kids    int    // children of the bar
		opens   bool   // the press opens the menu
	}
	cases := []struct {
		name string
		f    fake
		i    int
		want bool
	}{
		{"no menu bar", fake{missing: "AXMenuBar", kids: 3}, 0, false},
		{"no children", fake{missing: "AXChildren", kids: 3}, 0, false},
		{"no such menu", fake{kids: 3}, 5, false},
		{"the press opens it", fake{kids: 3, opens: true}, 1, true},
		{"no position", fake{missing: "AXPosition", kids: 3}, 1, false},
		{"no point in the value", fake{kids: 3}, 1, false},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			next := objc.ID(1000)
			owned := map[objc.ID]int{} // reference → times released
			borrowed := map[objc.ID]bool{}
			var stray []objc.ID
			ref := func() objc.ID { next++; owned[next] = 0; return next }
			ax := &axAPI{
				createApp: func(int) objc.ID { return ref() },
				copyAttr: func(el objc.ID, name string) objc.ID {
					if name == c.f.missing {
						return 0
					}
					return ref()
				},
				count: func(objc.ID) int { return c.f.kids },
				at: func(_ objc.ID, i int) objc.ID {
					id := objc.ID(5000 + i)
					borrowed[id] = true
					return id
				},
				press: func(objc.ID) {
					if c.f.opens {
						menuOpened++
					}
				},
				value: func(objc.ID, bool) (float64, float64, bool) { return 0, 0, false },
				release: func(r objc.ID) {
					if _, ok := owned[r]; !ok {
						stray = append(stray, r)
						return
					}
					owned[r]++
				},
				wait: func() {},
			}
			var got bool
			onMain(func() { got = (&Window{}).openBarMenu(ax, c.i) })
			if got != c.want {
				t.Errorf("returned %v, want %v", got, c.want)
			}
			if len(owned) == 0 {
				t.Fatal("nothing was created")
			}
			for r, n := range owned {
				if n != 1 {
					t.Errorf("owned reference %d released %d times, want once", r, n)
				}
			}
			for _, r := range stray {
				if borrowed[r] {
					t.Errorf("released the borrowed bar item %d", r)
				} else {
					t.Errorf("released %d, which it never had", r)
				}
			}
		})
	}
}
