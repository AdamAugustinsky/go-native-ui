package rexui

import (
	"gonativeui/icon"
	"gonativeui/theme"
	"gonativeui/ui"
	"gonativeui/widget/base"
)

// BannerLevel is what a banner or a toast reports.
type BannerLevel uint8

const (
	// BannerInfo is neutral news, in the accent colour.
	BannerInfo BannerLevel = iota
	// BannerSuccess is something that worked, in green.
	BannerSuccess
	// BannerWarning is something to look at, in orange.
	BannerWarning
	// BannerError is something that failed, in red.
	BannerError

	// NumBannerLevels is the number of levels.
	NumBannerLevels
)

// BannerOpts are a banner's options, passed by value.
type BannerOpts struct {
	// Level is what it reports.
	Level BannerLevel
	// Message is the line or lines below the title.
	Message string
	// Action is the label of a button at the end ("Retry", "Undo").
	Action string
	// Dismissible adds a close button.
	Dismissible bool
}

// BannerRecipe is the look of banners and toasts. There is no AppKit control
// for either, so the recipe is designed: the level's colour as a tint over
// the surface, a hairline of the colour, the symbol in the colour, and the
// text in the label colours so that the contrast is the text's, not the
// level's.
type BannerRecipe struct {
	// Radius, Pad and Gap are the banner's corner radius, padding and the
	// space between its parts, in points; IconW is the symbol's column.
	Radius, Pad, Gap, IconW float32
	// Level is the colour of each level; Tint and Edge the opacity (light,
	// dark) of the level's colour as the banner's fill and as its hairline.
	Level      [NumBannerLevels]theme.Role
	Icon       [NumBannerLevels]icon.ID
	Tint, Edge [2]float32
	// Title and Message are the text colours.
	Title, Message Tint
}

// BannerLook is the banners' recipe.
var BannerLook = BannerRecipe{
	Radius: 10, Pad: 12, Gap: 10, IconW: 18,
	Level: [NumBannerLevels]theme.Role{theme.Accent, theme.Green, theme.Orange, theme.Red},
	Icon:  [NumBannerLevels]icon.ID{icon.Info, icon.Check, icon.Warning, icon.Error},
	Tint:  [2]float32{0.10, 0.16}, Edge: [2]float32{0.28, 0.32},
	Title: Of(theme.Label), Message: Of(theme.Label2),
}

// BannerSignal is what a banner or a toast reports.
type BannerSignal struct {
	// ID is the banner's root.
	ID ui.ID
	// Action: the action button was clicked.
	Action bool
	// Dismissed: the close button was clicked (for a toast also: its time ran
	// out); the app stops building it.
	Dismissed bool
}

// Banner builds an inline banner: the level's symbol, a title and a message
// and, at its end, an action and a close button. It fills the parent's width
// (the kit's NextWidth says otherwise) and is as high as its text needs. The
// app owns whether it shows. Its title is the accessible name of the group,
// prefixed by the level ("Error: …").
//
//	b := rexui.Banner(k, "Upload failed", rexui.BannerOpts{Level: rexui.BannerError, Message: "The file is too large.", Action: "Retry", Dismissible: true})
//	if b.Action { v.retry() }
//	if b.Dismissed { v.hideBanner = true }
func Banner(k *base.Kit, title string, o BannerOpts) BannerSignal {
	return banner(k, title, o, false, 0)
}

// ToastLook is the toasts' own geometry, in points: a toast is a banner that
// floats at the bottom centre of its parent, over what is there, on the
// raised surface.
var ToastLook = struct {
	// MaxW is its widest, Margin its distance from the parent's bottom and
	// sides, and Duration how long it stays when ToastOpts.Duration is 0.
	MaxW, Margin, Duration float32
}{MaxW: 420, Margin: 16, Duration: 4}

// ToastOpts are a toast's options: a banner's, and how long it stays.
type ToastOpts struct {
	BannerOpts
	// Duration is how long the toast stays, in seconds (0: ToastLook.Duration;
	// a negative one stays until it is dismissed). The time stands still while
	// the pointer is over the toast or a window is not showing.
	Duration float32
}

// Toast builds a banner that floats at the bottom centre of the current
// parent and goes away by itself: build it last in the window's root, so that
// it is drawn over the content. The app owns whether it shows, and builds it
// for as long as it does; Dismissed says that its time ran out or that the
// close button was clicked, and the app stops building it. A toast that is not
// built in a frame is forgotten, so showing another later starts its time
// afresh.
//
// The toast fades in over the theme's Fade rate (at once under Reduce Motion)
// and asks for a frame only to start that and to end the toast's time:
// while it waits it draws none.
//
//	if v.toast != "" {
//		if rexui.Toast(k, v.toast, rexui.ToastOpts{BannerOpts: rexui.BannerOpts{Level: rexui.BannerSuccess}}).Dismissed {
//			v.toast = ""
//		}
//	}
func Toast(k *base.Kit, title string, o ToastOpts) BannerSignal {
	c := k.Context()
	_, ident := ui.SplitLabel(title)
	tkey := ui.HashString(ui.HashString(c.ParentKey(), "toast"), ident)
	dur := o.Duration
	if dur == 0 {
		dur = ToastLook.Duration
	}
	// the seconds left, kept from frame to frame in the context
	left := c.KeepValue(ui.KeyN(tkey, 70), dur, dur)
	sig := banner(k, title, o.BannerOpts, true, tkey)
	if dur > 0 && !c.Input().Hidden {
		hot := c.Hot(sig.ID)
		if !hot && c.Input().Time != 0 {
			left -= c.DT()
		}
		c.KeepValue(ui.KeyN(tkey, 70), dur, left)
		if left <= 0 {
			sig.Dismissed = true
		} else if !hot {
			c.WakeIn(int64(left * 1e9))
		}
	}
	return sig
}

func banner(k *base.Kit, title string, o BannerOpts, floating bool, tkey ui.Key) BannerSignal {
	c, r := k.Context(), k.Theme()
	rc := &BannerLook
	sc := k.Scale()
	lv := o.Level % NumBannerLevels
	display, ident := ui.SplitLabel(title)
	key := ui.HashString(c.ParentKey(), ident)
	if floating {
		key = tkey
	}
	col := r.Colors[rc.Level[lv]]
	t := r.T
	fill := theme.WithAlpha(col, theme.Lerp(rc.Tint[0], rc.Tint[1], t))
	edge := theme.WithAlpha(col, theme.Lerp(rc.Edge[0], rc.Edge[1], t))
	pad, gap := pt(sc, rc.Pad), pt(sc, rc.Gap)
	radius := pt(sc, rc.Radius)

	if floating {
		pr := c.Rect(c.Parent())
		pw, ph := pr[2]-pr[0], pr[3]-pr[1]
		w := min(pt(sc, ToastLook.MaxW), pw-2*pt(sc, ToastLook.Margin))
		sr := c.RectOf(key)
		h := sr[3] - sr[1]
		theme.At(c, theme.Snap((pw-w)/2), ph-h-pt(sc, ToastLook.Margin), w, 0)
		c.NextSize(ui.Px(w), ui.Children(1))
		// opaque: the raised surface under the tint, so that content does not show through
		fill = theme.Over(fill, r.Colors[theme.Raised]|0xFF)
		el := &r.Elevations[theme.ElevRaised]
		sh := el.Shadows[0]
		c.NextShadow(ui.Shadow{OffY: sh.Y, Sigma: sh.Blur, Color: sh.Color(r.Colors[theme.Shadow], r.T)})
		fade := c.AnimateValueFrom(ui.KeyN(key, 71), 0, 1, RateFor(r.Motion.Dismiss))
		if reduced(k) {
			fade = 1
		}
		c.NextOpacity(fade)
		if h == 0 { // laid out at the bottom from the second frame
			c.RequestFrame()
		}
	} else {
		k.ApplySize(ui.Pct(1), ui.Children(1))
	}
	c.NextBG(fill)
	c.NextBorder(edge, r.Lines.Hairline)
	c.NextRadius(radius)
	c.NextPad(pad)
	c.NextGap(gap)
	c.NextAxis(ui.AxisX)
	c.NextCross(ui.CrossCenter)
	c.NextRole(ui.RoleGroup)
	c.NextSemName(bannerName(c, lv, display))
	flags := ui.DrawBackground | ui.DrawBorder | disabledFlag(k)
	if floating {
		flags |= ui.DrawShadow | ui.Hoverable | base.Floating
	}
	root := c.BoxKeyed(flags, key, nil)
	c.Push(root)
	sig := BannerSignal{ID: root}

	if f, disp := k.Icons().Prepare(c, rc.Icon[lv], col); f != 0 {
		_, ih := k.IconSize(rc.Icon[lv])
		c.NextSize(ui.Px(pt(sc, rc.IconW)), ui.Px(ih))
		c.NextAlign(ui.AlignCenter)
		c.BoxKeyed(f, ui.KeyN(key, 1), disp)
	}
	// the text column takes the room the icon and the buttons leave
	c.NextAxis(ui.AxisY)
	c.NextGap(pt(sc, 2))
	c.NextSize(ui.Pct(1), ui.Children(1))
	col1 := c.BoxKeyed(0, ui.KeyN(key, 2), nil)
	c.Push(col1)
	ts := &r.Styles[theme.StyleHeadline]
	c.NextSize(ui.Pct(1), ui.Px(ts.Line))
	c.NextFont(ts.Font)
	c.NextTextColor(rc.Title.RGBA(r))
	c.BoxKeyed(ui.DrawText|ui.FadeOverflowX, ui.KeyN(key, 3), base.StrBytes(display))
	if o.Message != "" {
		ms := &r.Styles[theme.StyleCallout]
		c.NextSize(ui.Pct(1), ui.TextSize(0))
		c.NextFont(ms.Font)
		c.NextTextColor(rc.Message.RGBA(r))
		c.BoxKeyed(ui.DrawText|ui.WrapText, ui.KeyN(key, 4), base.StrBytes(o.Message))
	}
	c.Pop()
	if o.Action != "" {
		sig.Action = Button(k, o.Action, ButtonOpts{Size: theme.SizeSmall}).Clicked
	}
	if o.Dismissible || floating {
		if IconButton(k, icon.Close, "Dismiss", IconButtonOpts{Variant: IconBorderless}).Clicked {
			sig.Dismissed = true
		}
	}
	c.Pop()
	return sig
}

// bannerName is "Level: title", built for the accessibility tree.
func bannerName(c *ui.Context, lv BannerLevel, title string) string {
	return c.Fmt().Str([...]string{"Info", "Success", "Warning", "Error"}[lv]).Str(": ").Str(title).String()
}
