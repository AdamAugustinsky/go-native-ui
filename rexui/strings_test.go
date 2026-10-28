package rexui_test

import (
	"testing"

	"gonativeui/menu"
	"gonativeui/rexui"
	"gonativeui/rexui/mdview"
	"gonativeui/theme"
	"gonativeui/ui"
	"gonativeui/widget/base"
)

// usePtBR switches the components to StringsPtBR for one test.
func usePtBR(t *testing.T) {
	rexui.SetStrings(rexui.StringsPtBR)
	t.Cleanup(func() { rexui.SetStrings(rexui.StringsEnglish) })
}

// In Brazilian Portuguese a code block's button reads Copiar, and the names
// the components give a code block, a warning, a sent message, the sidebar's
// disclosures and a scroll bar are Portuguese in the semantic tree; the
// English ones are gone.
func TestStringsPtBR(t *testing.T) {
	usePtBR(t)
	g := newRig(t, theme.Light, nil, column(func(g *rig) {
		k := g.k
		key := k.Context().ParentKey()
		mdview.CodeBlock(k, ui.KeyN(key, 1), []byte("go test ./..."), mdview.CodeOpts{})
		rexui.Banner(k, "Disco cheio", rexui.BannerOpts{Level: rexui.BannerWarning})
		rexui.MessageBubble(k, ui.KeyN(key, 2), []byte("Oi"), nil, rexui.MessageOpts{BubbleOpts: rexui.BubbleOpts{Variant: rexui.BubbleOutgoing, ColumnW: 300}})
		k.NextSize(ui.Px(300), ui.Px(120))
		rexui.WebView(k, "Página", 0, rexui.WebViewOpts{Loading: true, Progress: 0.4})
	}))
	g.h = 800
	g.settle()
	g.node(ui.RoleButton, "Copiar") // the button's label is its name
	g.node(ui.RoleGroup, "Código")
	g.node(ui.RoleGroup, "Aviso: Disco cheio")
	g.node(ui.RoleGroup, "Mensagem enviada")
	g.node(ui.RoleProgressBar, "Carregando") // the web view's loading line
	for _, n := range []string{"Copy", "Code", "Warning: Disco cheio", "Sent message", "Loading"} {
		if g.has(ui.RoleButton, n) || g.has(ui.RoleGroup, n) || g.has(ui.RoleProgressBar, n) {
			t.Errorf("%q is still in the semantic tree", n)
		}
	}

	s, _ := newSidebarRig(t)
	s.node(ui.RoleButton, "Mostrar ou Ocultar")
	s.node(ui.RoleButton, "Expandir ou Recolher")
	if s.has(ui.RoleButton, "Show or Hide") {
		t.Error("the header's disclosure is still Show or Hide")
	}
	if base.CurrentStrings().VerticalScrollBar != "Barra de rolagem vertical" {
		t.Error("SetStrings did not set the primitives' names")
	}
	if menu.RoleTitle(menu.RoleQuit) != "Encerrar" || menu.CurrentStrings().QuitApp != "Encerrar %s" {
		t.Error("SetStrings did not set the menus' titles")
	}
}

// A Strings of the app's own keeps English in the fields it leaves empty.
func TestStringsEmptyFieldsStayEnglish(t *testing.T) {
	rexui.SetStrings(rexui.Strings{Copy: "Kopieren"})
	t.Cleanup(func() { rexui.SetStrings(rexui.StringsEnglish) })
	s := rexui.CurrentStrings()
	if s.Copy != "Kopieren" || s.SentMessage != "Sent message" || s.Colors[11] != "Brown" || s.Base.Divider != "Divider" {
		t.Errorf("got %+v", s)
	}
}
