package base

import (
	"gonativeui/gpu"
	"gonativeui/img"
	"gonativeui/ui"
)

// SetImages gives the kit the window's image cache, which Image draws from.
func (k *Kit) SetImages(c *img.Cache) { k.imgs = c }

// Images is the window's image cache (nil until SetImages).
func (k *Kit) Images() *img.Cache { return k.imgs }

// Image builds a box showing image h of the kit's cache (SetImages), placed
// by fit (ui.FitFill for avatars and grid thumbnails, ui.FitContain for media)
// with corner radius radius (pixels). Until the image is Ready, and when it
// failed, the box shows a skeleton: the theme's track fill in the same shape.
// alt is the image's accessible name (RoleImage); empty for decoration. The
// box's size is the caller's: NextSize on the kit or the context. n tells the
// images of one parent apart.
//
// It marks the image in use this frame (img.Cache.Use), so the cache does
// not evict it while it shows. An animated image (img.Animate) plays: the
// frame showing is drawn, and the box asks the context to wake the app at
// the next frame's deadline (Context.WakeIn), never every frame. It plays
// only while it shows: the window is not hidden (Input.Hidden), and the
// box, as the last layout placed it, is not hidden by a flow and has some of
// it inside the window and the clip rectangles around it (Context.ShownRect).
// Reduce Motion (Kit.ReduceMotion or the system's) and the system's
// "Auto-play animated images" hold it on the first frame until
// Cache.Animation(h).Play. A styled recipe (an avatar with initials while
// loading) reads the state itself with ImageState and draws its own
// placeholder.
func (k *Kit) Image(n uint64, h img.Handle, fit ui.ImageFit, radius float32, alt string) ui.ID {
	c := k.c
	k.syncSize()
	if k.hasW || k.hasH {
		k.ApplySize(ui.Size{}, ui.Size{})
	}
	c.NextRadius(radius)
	c.NextRole(ui.RoleImage)
	if alt != "" {
		c.NextSemName(alt)
	}
	key := ui.KeyN(c.ParentKey(), 9040+n)
	tex, uv, st := k.imageTexKeyed(h, key)
	if st == img.Ready {
		c.NextImageFit(tex, uv, fit)
		c.NextTextColor(0xFFFFFFFF) // the image at full opacity (the box's opacity still applies)
		return c.BoxKeyed(ui.DrawImage, key, nil)
	}
	c.NextBG(k.r.Pal.TrackFill)
	return c.BoxKeyed(ui.DrawBackground, key, nil)
}

// ExternalImage builds a box showing texture tex, which the app made and
// fills itself rather than the kit's cache: a video frame, as a YUV texture
// (gpu.Renderer.NewYUVTexture and StageYUV, a call's frames) or a pixel
// buffer imported for this frame (ImportPixelBuffer, media.Video), or an
// RGBA texture of its own. w and h are the image's size in texels (for a
// turned frame, its turned size); fit, radius and alt are as for Image, and
// without a texture (tex 0, or w or h 0) the box shows Image's skeleton. n
// tells the images of one parent apart, in the same keys as Image: ask
// ImageShown(n) before pulling a frame for it.
func (k *Kit) ExternalImage(n uint64, tex gpu.TextureID, w, h int, fit ui.ImageFit, radius float32, alt string) ui.ID {
	c := k.c
	k.syncSize()
	if k.hasW || k.hasH {
		k.ApplySize(ui.Size{}, ui.Size{})
	}
	c.NextRadius(radius)
	c.NextRole(ui.RoleImage)
	if alt != "" {
		c.NextSemName(alt)
	}
	key := ui.KeyN(c.ParentKey(), 9040+n)
	if tex != 0 && w > 0 && h > 0 {
		c.NextImageFit(tex, [4]float32{0, 0, float32(w), float32(h)}, fit)
		c.NextTextColor(0xFFFFFFFF)
		return c.BoxKeyed(ui.DrawImage, key, nil)
	}
	c.NextBG(k.r.Pal.TrackFill)
	return c.BoxKeyed(ui.DrawBackground, key, nil)
}

// ImageShown reports whether image box n of the current parent (Image or
// ExternalImage) shows: the window is not hidden, and the box, as the last
// layout placed it, is not hidden by a flow and has some of it inside the
// window and the clip rectangles around it; a box not laid out yet shows.
// It watches the box, so a layout that reveals or hides it asks for another
// frame. A video pulls frames and asks for the next (Context.WakeIn) only
// while its box shows, so one scrolled away or in a hidden window costs
// nothing.
func (k *Kit) ImageShown(n uint64) bool {
	return k.shown(ui.KeyN(k.c.ParentKey(), 9040+n))
}

// shown is ImageShown for the box keyed key.
func (k *Kit) shown(key ui.Key) bool {
	c := k.c
	if c.Input().Hidden {
		return false
	}
	vis := true
	if id, ok := c.BoxOf(key); ok {
		r := c.ShownRect(id)
		vis = r[2] > r[0] && r[3] > r[1]
	}
	c.WatchShown(key, vis)
	return vis
}

// ImageState reports the state of image h without building anything, and
// marks it in use like Image (for recipes that draw their own placeholder).
func (k *Kit) ImageState(h img.Handle) img.State {
	_, _, st := k.imageTex(h)
	return st
}

func (k *Kit) imageTex(h img.Handle) (tex gpu.TextureID, uv [4]float32, st img.State) {
	if k.imgs == nil || h == 0 {
		return 0, uv, img.Pending
	}
	return k.imgs.Use(h)
}

// imageTexKeyed is imageTex for the image box keyed key: an animated image is
// advanced by the frame's clock if the box shows, and its next deadline is
// asked for.
func (k *Kit) imageTexKeyed(h img.Handle, key ui.Key) (tex gpu.TextureID, uv [4]float32, st img.State) {
	if k.imgs == nil || h == 0 {
		return 0, uv, img.Pending
	}
	if !k.imgs.Animated(h) {
		return k.imgs.Use(h)
	}
	c := k.c
	p := img.Present{
		Now:          c.Input().Time,
		Visible:      k.shown(key),
		ReduceMotion: k.ReduceMotion || k.Theme().Settings.ReduceMotion,
	}
	tex, uv, st, wake := k.imgs.UseAnimated(h, p)
	if wake > 0 {
		c.WakeIn(wake)
	}
	return tex, uv, st
}
