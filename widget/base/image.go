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
// not evict it while it shows. A styled recipe (an avatar with initials while
// loading) reads the state itself with ImageState and draws its own
// placeholder.
func (k *Kit) Image(n uint64, h img.Handle, fit ui.ImageFit, radius float32, alt string) ui.ID {
	c := k.c
	if k.hasW || k.hasH {
		k.ApplySize(ui.Size{}, ui.Size{})
	}
	c.NextRadius(radius)
	c.NextRole(ui.RoleImage)
	if alt != "" {
		c.NextSemName(alt)
	}
	key := ui.KeyN(c.ParentKey(), 9040+n)
	tex, uv, st := k.imageTex(h)
	if st == img.Ready {
		c.NextImageFit(tex, uv, fit)
		c.NextTextColor(0xFFFFFFFF) // the image at full opacity (the box's opacity still applies)
		return c.BoxKeyed(ui.DrawImage, key, nil)
	}
	c.NextBG(k.r.Pal.TrackFill)
	return c.BoxKeyed(ui.DrawBackground, key, nil)
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
