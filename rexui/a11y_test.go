package rexui_test

import (
	"testing"

	"gonativeui/theme"
	"gonativeui/ui"
	"gonativeui/widget/widgettest"
)

// An outline as VoiceOver sees it through the accessibility bridge: an
// AXOutline of AXOutlineRows with their levels, the expanded ones marked, the
// selection, and the disclosure action; then with A expanded.
func TestOutlineAccessibilitySnapshot(t *testing.T) {
	v := newOutlineView(smallSize, smallDepth)
	v.names = [][]byte{[]byte("A"), []byte("A1"), []byte("A1a"), []byte("A2"), []byte("B"), []byte("B1"), []byte("C")}
	g := newRig(t, theme.Light, nil, v.build)
	g.click(200, 24) // select A
	g.settle()
	g.c.Semantics(&g.sem)
	widgettest.AssertAX(t, "ax-outline", &g.sem)
	widgettest.LintNames(t, "outline", &g.sem)
	g.key(ui.KeyRight, 0)
	g.settle()
	g.c.Semantics(&g.sem)
	widgettest.AssertAX(t, "ax-outline-expanded", &g.sem)

	// Unnamed rows take their text's name (Outline.TextBytes).
	v.names = nil
	g.settle()
	g.c.Semantics(&g.sem)
	widgettest.LintNames(t, "outline, rows named by their text", &g.sem)
}
