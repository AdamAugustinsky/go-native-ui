package media

import (
	"strconv"

	"gonativeui/icon"
	"gonativeui/rexui"
	"gonativeui/theme"
	"gonativeui/ui"
	"gonativeui/widget/base"
)

// TileKind is what a strip's tile shows.
type TileKind uint8

const (
	// TileImage is a picture's thumbnail.
	TileImage TileKind = iota
	// TileVideo is a dark plate with a play glyph (and Meta, its duration).
	TileVideo
	// TileAudio and TileFile are chips: the kind's symbol, the name and Meta.
	TileAudio
	TileFile
)

// Tile is one of a strip's tiles.
type Tile struct {
	Item
	Kind TileKind
	// Meta is the quiet text after the name (a duration, a size).
	Meta string
}

// StripRecipe is a strip's look, in points (a3-app's media strip: T3 Code's
// h-40 thumbnails).
type StripRecipe struct {
	// Height is a thumbnail's, MaxW its widest; ChipH a chip's height.
	Height, MaxW, ChipH float32
	// Gap is between tiles; Radius rounds them.
	Gap, Radius float32
	// Max is the most tiles shown: more show Max-1 and a "+N" tile.
	Max int
	// Plate is a video tile's fill; More the "+N" tile's.
	Plate, More rexui.Tint
}

// StripLook is the strips' recipe.
var StripLook = StripRecipe{
	Height: 160, MaxW: 280, ChipH: 40, Gap: 8, Radius: 10, Max: 4,
	Plate: rexui.OfRGBA(0x1C1A18FF), More: rexui.OfAlpha(theme.Label, 0.06, 0.08),
}

// StripOpts are a strip's symbols: Play on a video's plate, Audio and File on
// the chips (0: none).
type StripOpts struct {
	Play, Audio, File icon.ID
}

// Strip builds tiles in a row that wraps: thumbnails at the recipe's height,
// videos as plates, audios and files as chips; past Max tiles the last place
// is "+N". It returns the index of the tile clicked (the "+N" tile is the
// first it hides), -1 for none.
func Strip(k *base.Kit, label string, tiles []Tile, o StripOpts) int {
	c, r := k.Context(), k.Theme()
	rc := &StripLook
	sc := k.Scale()
	clicked := -1
	shown := len(tiles)
	if rc.Max > 0 && shown > rc.Max {
		shown = rc.Max - 1
	}
	display, ident := ui.SplitLabel(label)
	c.NextSize(ui.Pct(1), ui.Children(1))
	c.NextRole(ui.RoleGroup)
	c.NextSemName(display)
	c.Push(c.BoxKeyed(0, ui.HashString(c.ParentKey(), ident), nil))
	c.NextSize(ui.Pct(1), ui.Children(1))
	c.BeginFlow("tiles", ui.FlowOpts{GapX: rc.Gap * sc, GapY: rc.Gap * sc, Cross: ui.CrossEnd})
	for i := 0; i < shown; i++ {
		t := &tiles[i]
		switch t.Kind {
		case TileImage:
			if Picture(k, uint64(i), t.Item, PictureOpts{H: rc.Height, MaxW: rc.MaxW, Radius: rc.Radius, Hairline: true}).Clicked {
				clicked = i
			}
		case TileVideo:
			if plate(k, uint64(i), t, o.Play) {
				clicked = i
			}
		default:
			ic := o.File
			if t.Kind == TileAudio {
				ic = o.Audio
			}
			if chip(k, uint64(i), t, ic) {
				clicked = i
			}
		}
	}
	if shown < len(tiles) {
		h := theme.Snap(rc.Height * sc)
		c.NextSize(ui.Px(h*3/4), ui.Px(h))
		c.NextRadius(rc.Radius * sc)
		c.NextBG(rc.More.RGBA(r))
		c.NextBorder(r.Colors[theme.Separator], r.Lines.Hairline)
		c.NextAxis(ui.AxisX)
		c.NextJustify(ui.JustifyCenter)
		c.NextCross(ui.CrossCenter)
		c.NextCursor(ui.CursorPointer)
		c.NextRole(ui.RoleButton)
		more := "+" + strconv.Itoa(len(tiles)-shown)
		c.NextSemName(more)
		id := c.BoxKeyed(ui.Clickable|ui.DrawBackground|ui.DrawBorder, ui.KeyN(c.ParentKey(), 7999), nil)
		c.Push(id)
		rexui.Label(k, more+"###more", rexui.LabelOpts{Style: rexui.TextTitle3, Color: rexui.Of(theme.Label2)})
		c.Pop()
		if c.Signal(id).Clicked {
			clicked = shown
		}
	}
	c.EndFlow()
	c.Pop()
	return clicked
}

// plate is a video's tile: a dark plate the thumbnail's height, its play glyph in the middle and
// its name (and Meta) along the bottom.
func plate(k *base.Kit, n uint64, t *Tile, play icon.ID) bool {
	c, r := k.Context(), k.Theme()
	rc := &StripLook
	sc := k.Scale()
	h := theme.Snap(rc.Height * sc)
	c.NextSize(ui.Px(theme.Snap(h*16/9)), ui.Px(h))
	if w := theme.Snap(rc.MaxW * sc); h*16/9 > w {
		c.NextSize(ui.Px(w), ui.Px(h))
	}
	c.NextRadius(rc.Radius * sc)
	c.NextBG(rc.Plate.RGBA(r))
	c.NextAxis(ui.AxisY)
	c.NextJustify(ui.JustifyCenter)
	c.NextCross(ui.CrossCenter)
	c.NextCursor(ui.CursorPointer)
	c.NextRole(ui.RoleButton)
	c.NextSemName(t.Name)
	id := c.BoxKeyed(ui.Clickable|ui.DrawBackground, ui.KeyN(c.ParentKey(), 7200+n), nil)
	c.Push(id)
	if play != 0 {
		k.Icon(play, 0xFFFFFFE6)
	}
	c.NextAnchor(ui.AnchorBottomLeft, 10*sc, 8*sc)
	c.NextSize(ui.Px(theme.Snap(h*16/9)-20*sc), ui.Children(1))
	c.NextAxis(ui.AxisX)
	c.NextGap(6 * sc)
	c.Push(c.Box(base.Floating, "caption"))
	c.NextShrinkFirst()
	rexui.Label(k, t.Name+"###name", rexui.LabelOpts{Style: rexui.TextSubheadline, Color: rexui.OfRGBA(0xFFFFFFE6), Truncate: true})
	if t.Meta != "" {
		rexui.Label(k, t.Meta+"###meta", rexui.LabelOpts{Style: rexui.TextSubheadline, Color: rexui.OfRGBA(0xFFFFFFA6)})
	}
	c.Pop()
	c.Pop()
	_ = r
	return c.Signal(id).Clicked
}

// chip is an audio's or a file's tile: its symbol, its name and Meta in a rounded box.
func chip(k *base.Kit, n uint64, t *Tile, ic icon.ID) bool {
	c, r := k.Context(), k.Theme()
	rc := &StripLook
	sc := k.Scale()
	c.NextSize(ui.Children(1), ui.Px(theme.Snap(rc.ChipH*sc)))
	c.NextMaxSize(theme.Snap(rc.MaxW*sc), 0)
	c.NextRadius(rc.Radius * sc)
	c.NextBorder(r.Colors[theme.Separator], r.Lines.Hairline)
	c.NextPadding(12*sc, 0, 12*sc, 0)
	c.NextGap(8 * sc)
	c.NextAxis(ui.AxisX)
	c.NextCross(ui.CrossCenter)
	c.NextCursor(ui.CursorPointer)
	c.NextRole(ui.RoleButton)
	c.NextSemName(t.Name)
	id := c.BoxKeyed(ui.Clickable|ui.DrawBorder, ui.KeyN(c.ParentKey(), 7300+n), nil)
	c.Push(id)
	if ic != 0 {
		k.Icon(ic, r.Colors[theme.Label2])
	}
	c.NextShrinkFirst()
	rexui.Label(k, t.Name+"###name", rexui.LabelOpts{Truncate: true})
	if t.Meta != "" {
		rexui.Label(k, t.Meta+"###meta", rexui.LabelOpts{Color: rexui.Of(theme.Label2), MetaPt: theme.MetaPt})
	}
	c.Pop()
	return c.Signal(id).Clicked
}
