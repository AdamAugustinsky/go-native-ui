package widget

import (
	"slices"
	"testing"

	"gonativeui/gpu"
	"gonativeui/icon"
	"gonativeui/rexui"
)

// Each legacy kind draws its icon button in the matching rexui variant:
// primary keeps the accent and danger the destructive red.
func TestIconButtonKinds(t *testing.T) {
	want := map[ButtonKind]rexui.IconButtonVariant{
		ButtonDefault: rexui.IconBordered,
		ButtonPrimary: rexui.IconPrimary,
		ButtonPlain:   rexui.IconBorderless,
		ButtonDanger:  rexui.IconDestructive,
	}
	draw := func(build func(k *Kit)) []gpu.Quad {
		g := newRig(t, 200, 100, build)
		return slices.Clone(g.list.Quads)
	}
	seen := map[uint32]ButtonKind{}
	for kind, v := range want {
		got := draw(func(k *Kit) { k.IconButton(icon.Plus, "Add", kind) })
		ref := draw(func(k *Kit) { rexui.IconButton(k.Kit, icon.Plus, "Add", rexui.IconButtonOpts{Variant: v}) })
		if !slices.Equal(got, ref) {
			t.Errorf("kind %d is not drawn as icon-button variant %d", kind, v)
		}
		if len(got) > 0 && kind != ButtonPlain {
			if other, ok := seen[got[0].Color[0]]; ok {
				t.Errorf("kinds %d and %d share the fill %08X", other, kind, got[0].Color[0])
			}
			seen[got[0].Color[0]] = kind
		}
	}
}
