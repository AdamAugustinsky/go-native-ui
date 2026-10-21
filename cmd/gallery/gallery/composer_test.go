package gallery

import (
	"testing"

	"gonativeui/theme"
	"gonativeui/ui"
	"gonativeui/widget/widgettest"
)

// The composer's samples are on the appkit-d7a page, which the audit and the
// name lint run in light with every other page (audit_test.go, a11y_test.go);
// here in dark too.
func TestD7bPageInDark(t *testing.T) {
	p := AppKitPage("appkit-d7a")
	if p < 0 {
		t.Fatal("no appkit-d7a page")
	}
	off, err := runAppKit(p, theme.ForceDark, 2, nil)
	if err != nil {
		t.Fatal(err)
	}
	defer off.Close()
	widgettest.AuditLayout(t, "appkit-d7a, dark", off.UI)
	var tree ui.SemTree
	off.UI.Semantics(&tree)
	widgettest.LintNames(t, "appkit-d7a, dark", &tree)
	found := 0
	for _, n := range tree.Nodes {
		if n.Role == ui.RoleButton && (string(tree.Text(n.Name)) == "Send" || string(tree.Text(n.Name)) == "Sending") {
			found++
		}
	}
	if found != 7 {
		t.Errorf("%d send buttons on the page, want one per composer sample (7)", found)
	}
}
