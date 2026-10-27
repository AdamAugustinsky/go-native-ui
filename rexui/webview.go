package rexui

import (
	"gonativeui/platform"
	"gonativeui/theme"
	"gonativeui/ui"
	"gonativeui/widget/base"
)

// WebViewRecipe is the look of the pane a hosted browser view sits in
// (app/webview, the phase 8 plan's H1): a rounded pane with a hairline edge
// whose top edge is the loading line, the page below it, and a notice line
// under the page.
type WebViewRecipe struct {
	// Fill is the pane under the page (seen before the page draws, and when
	// there is no view), Edge its hairline, EdgeW wide in points.
	Fill, Edge Tint
	EdgeW      float32
	// Radius is the pane's corner radius in points. ContentRadius is the
	// view's own (webview.Options.CornerRadius), inside the edge.
	Radius, ContentRadius float32
	// Line is the loading line's height in points, Track its colour at rest
	// and Bar the progress over it.
	Line       float32
	Track, Bar Tint
	// Notice colours a notice; Danger one that reports a failure. NoticePad
	// is the space around it, in points.
	Notice, Danger Tint
	NoticePad      float32
	// Empty colours the text of a pane with no view.
	Empty Tint
}

// WebViewLook is the browser panes' recipe.
var WebViewLook = WebViewRecipe{
	Fill: Of(theme.Window), Edge: Of(theme.Separator), EdgeW: 1,
	Radius: 10.5, ContentRadius: 9.5,
	Line: 2, Track: None, Bar: Of(theme.Accent),
	Notice: Of(theme.Label2), Danger: DangerText, NoticePad: 6,
	Empty: Of(theme.Label2),
}

// WebViewOpts are a browser pane's options, passed by value. Loading and
// Progress are the view's (webview.State).
type WebViewOpts struct {
	// Loading shows Progress (0 to 1) along the pane's top edge.
	Loading  bool
	Progress float32
	// Notice is a line under the page: a navigation the policy blocked, a
	// page that failed to load. Danger sets it in DangerText.
	Notice string
	Danger bool
	// Empty is what a pane with no view says (host 0: no browser view on
	// this system, or none yet), centred.
	Empty string
}

// WebViewSignal is what a browser pane reports.
type WebViewSignal struct {
	base.Signal
	// Host is the view's box (ui.Context.Host): focusable, so Tab reaches
	// the page.
	Host ui.ID
}

// WebView builds the pane of hosted browser view host (webview.View.Host;
// 0 for none) labelled label. Its size is the kit's NextSize, the parent's
// by default. Make the view with webview.Options.CornerRadius set to
// WebViewLook.ContentRadius so that its corners follow the pane's. The page
// is a native view above the window's pixels (D41): a drawn popup or sheet
// over the pane hides it while it is open.
//
//	st := b.view.State()
//	rexui.WebView(k, "Browser", b.view.Host(), rexui.WebViewOpts{Loading: st.Loading, Progress: st.Progress, Notice: b.notice, Danger: b.failed})
func WebView(k *base.Kit, label string, host platform.HostID, o WebViewOpts) WebViewSignal {
	c, r := k.Context(), k.Theme()
	rc := &WebViewLook
	sc := k.Scale()
	display, ident := ui.SplitLabel(label)
	key := ui.HashString(c.ParentKey(), ident)
	bw := pt(sc, rc.EdgeW)

	c.NextBG(rc.Fill.RGBA(r))
	c.NextBorder(rc.Edge.RGBA(r), bw)
	c.NextRadius(pt(sc, rc.Radius))
	c.NextPadding(bw, 0, bw, bw)
	c.NextAxis(ui.AxisY)
	k.ApplySize(ui.Pct(1), ui.Pct(1))
	c.NextRole(ui.RoleGroup)
	c.NextSemName(display)
	pane := c.BoxKeyed(ui.DrawBackground|ui.DrawBorder|ui.Clip, key, nil)
	sig := WebViewSignal{Signal: k.SignalOf(pane)}
	c.Push(pane)

	// the top edge: the loading line, inset by the corners
	rad := pt(sc, rc.Radius)
	c.NextPadding(rad, 0, rad, 0)
	c.NextSize(ui.Pct(1), ui.Px(pt(sc, rc.Line)))
	c.Push(c.BoxKeyed(0, ui.KeyN(key, 1), nil))
	if o.Loading {
		c.NextBG(rc.Bar.RGBA(r))
		c.NextRadius(pt(sc, rc.Line) / 2)
		c.NextSize(ui.Pct(min(max(o.Progress, 0.05), 1)), ui.Pct(1))
		c.NextRole(ui.RoleProgressBar)
		c.NextSemName(str.Loading)
		c.BoxKeyed(ui.DrawBackground, ui.KeyN(key, 2), nil)
	}
	c.Pop()

	// the page
	c.NextSize(ui.Pct(1), ui.Grow(1))
	sig.Host = c.Host(uint32(host), ui.KeyN(key, 3), ui.HostFocusable)
	if host == 0 && o.Empty != "" {
		c.Push(sig.Host)
		c.NextCenter()
		Label(k, o.Empty, LabelOpts{Color: rc.Empty})
		c.Pop()
	}

	// the notice
	if o.Notice != "" {
		p := pt(sc, rc.NoticePad)
		c.NextPadding(p+rad/2, p, p+rad/2, p)
		c.NextSize(ui.Pct(1), ui.Children(1))
		c.Push(c.BoxKeyed(0, ui.KeyN(key, 4), nil))
		col := rc.Notice
		if o.Danger {
			col = rc.Danger
		}
		Label(k, o.Notice, LabelOpts{Style: TextCallout, Color: col})
		c.Pop()
	}
	c.Pop()
	return sig
}
