// Package mdview draws Markdown and code in the Rex look: Markdown, a parsed
// markdown.Doc; CodeBlock, a block of code highlighted by package highlight
// on a worker; and CodeLine, one row of a long file in a virtual list (the
// phase 8 plan's D75). It is rexui's, on rexui's tints, strings and
// controls, and a package of its own because the linker keeps goldmark's,
// chroma's and its regexp engine's package-level data in any program that
// imports them (1.0–1.2 MB stripped on the gallery and the notes example):
// an app that shows no Markdown or code imports rexui alone and does not pay
// for them.
//
//	view := &mdview.MarkdownView{} // one per window
//	sig := mdview.Markdown(k, ui.KeyN(c.ParentKey(), id), doc, mdview.MarkdownOpts{Item: id, View: view})
//
// Parse on a worker (markdown.Parser), never in Build; with the document
// parsed and the code highlighted, a frame allocates nothing.
package mdview

import "gonativeui/theme"

// pt is v points in pixels at scale, snapped (rexui's).
func pt(scale, v float32) float32 { return theme.Snap(v * scale) }
