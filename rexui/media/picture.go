// Package media is Rex's pictures in a document (a3-app's docs/design/media-files.md,
// T3 Code's ChatMarkdownMedia and ZoomableImage): a picture shown at the size
// its aspect gives it inside a box, a strip of thumbnails, and a lightbox that
// shows one picture of a gallery over the window, fitted or zoomed. The app
// owns the bytes (it fetches them) and hands them in with a hash that names
// them; the pictures are decoded by the kit's image cache at the size they
// show.
//
// It is a package of its own, outside rexui, so that a binary that shows no
// pictures (the gallery, with its size budget) links none of it.
package media

import (
	"gonativeui/img"
	"gonativeui/rexui"
	"gonativeui/theme"
	"gonativeui/ui"
	"gonativeui/widget/base"
)

// Item is a picture as the app has it.
type Item struct {
	// Hash names the content (img.Key.Hash): the app computes it once, when
	// the bytes arrive (img.HashBytes), never in a frame. 0 with Data set
	// hashes Data each frame.
	Hash uint64
	// Data are the encoded bytes (PNG, JPEG, WebP, GIF…); nil while they
	// load.
	Data []byte
	// Err says why the picture cannot show ("" none): a fetch that failed.
	Err string
	// Name is its accessible name (the alt text, the file's name).
	Name string
}

func (it *Item) key() uint64 {
	if it.Hash == 0 && it.Data != nil {
		return img.HashBytes(it.Data)
	}
	return it.Hash
}

// PictureOpts size a picture, in points. With W and H both set the picture
// covers that box (cropped about its centre: a collage's cell); with H alone
// it is that tall and as wide as its aspect makes it, up to MaxW (a strip's
// thumbnail, cropped when wider); with neither it is as large as its aspect
// lets it inside MaxW × MaxH, never larger than its pixels (an inline image).
type PictureOpts struct {
	W, H, MaxW, MaxH float32
	// Radius rounds it; Hairline draws the theme's separator round it.
	Radius   float32
	Hairline bool
}

// PictureSignal is what a picture reports.
type PictureSignal struct {
	// ID is its box; Clicked a click on it.
	ID      ui.ID
	Clicked bool
	// Ready: decoded and shown; Failed: the bytes are not a picture (or
	// Item.Err is set). Loading is neither.
	Ready, Failed bool
	// Aspect is its width over its height once decoded (0 before).
	Aspect float32
}

// PictureLook is the pictures' recipe.
var PictureLook = struct {
	// Placeholder is the fill while a picture loads (and of one that failed).
	Placeholder rexui.Tint
}{Placeholder: rexui.OfAlpha(theme.Label, 0.06, 0.08)}

// Picture builds item it as o sizes it. n tells the pictures of one parent
// apart. While its bytes load (or decode) it is a muted frame of the shape it
// will likely have (16:9, or 4:3 at a fixed height).
//
//	sig := media.Picture(k, 1, media.Item{Hash: m.Hash, Data: m.Bytes, Name: "foto.png"},
//		media.PictureOpts{MaxW: 480, MaxH: 480, Radius: 10, Hairline: true})
//	if sig.Clicked { v.lightbox.Show(0) }
func Picture(k *base.Kit, n uint64, it Item, o PictureOpts) PictureSignal {
	c, r := k.Context(), k.Theme()
	sc := k.Scale()
	var sig PictureSignal
	var hd img.Handle
	key := img.Key{}
	switch {
	case o.W > 0 && o.H > 0:
		key = img.Key{W: px(sc, o.W), H: px(sc, o.H), Flags: img.Cover}
	case o.H > 0:
		key = img.Key{H: px(sc, o.H)}
	default:
		key = img.Key{W: px(sc, o.MaxW), H: px(sc, o.MaxH)}
	}
	var info img.Info
	ready := false
	if ic := k.Images(); ic != nil && it.Data != nil && it.Err == "" {
		key.Hash = it.key()
		var st img.State
		hd, st = ic.Request(key, img.Bytes(it.Data))
		switch st {
		case img.Failed:
			sig.Failed = true
		default:
			info, ready = ic.Info(hd)
		}
	}
	sig.Failed = sig.Failed || it.Err != ""
	if ready && info.H > 0 {
		sig.Aspect = float32(info.W) / float32(info.H)
	}
	// the box in points
	var w, h float32
	switch {
	case o.W > 0 && o.H > 0:
		w, h = o.W, o.H
	case o.H > 0:
		h = o.H
		w = h * 4 / 3
		if sig.Aspect > 0 {
			w = h * sig.Aspect
		}
		if o.MaxW > 0 {
			w = min(w, o.MaxW)
		}
	default:
		w = o.MaxW
		h = w * 9 / 16
		if sig.Aspect > 0 {
			w = min(float32(info.W), o.MaxW) // its pixels at most (a browser's CSS pixels)
			h = w / sig.Aspect
			if o.MaxH > 0 && h > o.MaxH {
				h = o.MaxH
				w = h * sig.Aspect
			}
		}
	}
	w, h = theme.Snap(w*sc), theme.Snap(h*sc)
	radius := o.Radius * sc
	hair := float32(0)
	flags := ui.Clickable | ui.DrawBackground
	if o.Hairline {
		hair = r.Lines.Hairline
		c.NextBorder(r.Colors[theme.Separator], hair)
		flags |= ui.DrawBorder
	}
	c.NextSize(ui.Px(w), ui.Px(h))
	c.NextRadius(radius)
	c.NextPadding(hair, hair, hair, hair)
	c.NextBG(PictureLook.Placeholder.RGBA(r))
	c.NextCursor(ui.CursorPointer)
	c.NextRole(ui.RoleButton)
	if it.Name != "" {
		c.NextSemName(it.Name)
	}
	sig.ID = c.BoxKeyed(flags, ui.KeyN(c.ParentKey(), 7100+n), nil)
	if ready {
		c.Push(sig.ID)
		k.NextWidth(ui.Pct(1))
		k.NextHeight(ui.Pct(1))
		k.Image(1, hd, ui.FitFill, max(radius-hair, 0), "") // the button holds the name
		c.Pop()
		sig.Ready = true
	}
	sig.Clicked = c.Signal(sig.ID).Clicked
	return sig
}

func px(sc, v float32) int32 { return int32(v*sc + 0.5) }
