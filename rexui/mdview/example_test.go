package mdview_test

import (
	"fmt"

	"gonativeui/gpu"
	"gonativeui/internal/fakeui"
	"gonativeui/markdown"
	"gonativeui/rexui/mdview"
	"gonativeui/text"
	"gonativeui/theme/rex"
	"gonativeui/ui"
	"gonativeui/widget/base"
)

// A message parsed once and drawn every frame: the heading is a heading to
// assistive technologies, the code a group with its Copy button, and a click
// on the link reports its tag (Doc.LinkDest is its destination). The view
// keeps the window's style tables and highlights the code on a worker; the
// block is drawn in one colour until its tokens are ready.
func Example() {
	ft := fakeui.New()
	ts := layText{ft, text.NewBasicLayouter(ft)}
	c := ui.New(ts)
	c.AtlasSync(&fakeui.Uploader{})
	k := base.NewKit(c, ts, rex.Theme())
	var in ui.Input
	var list gpu.DrawList
	in.Inside, in.Focused = true, true
	in.MouseX, in.MouseY = -100, -100

	var p markdown.Parser
	var doc markdown.Doc
	p.Parse([]byte("# Release notes\n\nSee [the changelog](https://example.com/changes).\n\n```go\nfmt.Println(\"hi\")\n```\n")).CopyTo(&doc)
	view := &mdview.MarkdownView{} // one per window
	for range 3 {
		c.Begin(&in, 600, 400, 1, 1.0/60)
		k.Begin(c)
		sig := mdview.Markdown(k, ui.KeyN(c.ParentKey(), 1), &doc, mdview.MarkdownOpts{View: view})
		if sig.Link != 0 {
			fmt.Println("open", doc.LinkDest(sig.Link))
		}
		k.End()
		c.End(&list)
		in.ClearFrame()
	}

	var tree ui.SemTree
	c.Semantics(&tree)
	roles := map[ui.Role]string{ui.RoleHeading: "heading", ui.RoleLink: "link", ui.RoleGroup: "group", ui.RoleButton: "button"}
	for _, n := range tree.Nodes {
		if r, ok := roles[n.Role]; ok && len(tree.Text(n.Name)) > 0 {
			fmt.Printf("%s %q\n", r, tree.Text(n.Name))
		}
	}
	// Output:
	// heading "Release notes"
	// link "the changelog"
	// group "Code"
	// button "Copy"
}
