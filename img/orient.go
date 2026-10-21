package img

import (
	"image"
	"image/color"
)

// Orient returns src as shown with EXIF orientation o (1 to 8, as Orientation
// returns it): turned or mirrored so that its top left is the picture's top
// left. A swapped orientation (5 to 8) has the width and height of src
// exchanged. An orientation of 1 or outside 1 to 8 returns src itself; the
// others return a new image of src's own kind (*image.RGBA or *image.NRGBA),
// or an *image.NRGBA for any other, with its bounds at the origin. src is not
// changed.
func Orient(src image.Image, o int) image.Image {
	if o < 2 || o > 8 {
		return src
	}
	b := src.Bounds()
	sw, sh := b.Dx(), b.Dy()
	ow, oh := sw, sh
	if o >= 5 {
		ow, oh = sh, sw
	}
	r := image.Rect(0, 0, ow, oh)
	switch m := src.(type) {
	case *image.RGBA:
		dst := image.NewRGBA(r)
		for y := 0; y < oh; y++ {
			for x := 0; x < ow; x++ {
				sx, sy := orientedToStored(x, y, sw, sh, o)
				copy(dst.Pix[dst.PixOffset(x, y):][:4], m.Pix[m.PixOffset(b.Min.X+sx, b.Min.Y+sy):])
			}
		}
		return dst
	case *image.NRGBA:
		dst := image.NewNRGBA(r)
		for y := 0; y < oh; y++ {
			for x := 0; x < ow; x++ {
				sx, sy := orientedToStored(x, y, sw, sh, o)
				copy(dst.Pix[dst.PixOffset(x, y):][:4], m.Pix[m.PixOffset(b.Min.X+sx, b.Min.Y+sy):])
			}
		}
		return dst
	}
	dst := image.NewNRGBA(r)
	for y := 0; y < oh; y++ {
		for x := 0; x < ow; x++ {
			sx, sy := orientedToStored(x, y, sw, sh, o)
			dst.SetNRGBA(x, y, color.NRGBAModel.Convert(src.At(b.Min.X+sx, b.Min.Y+sy)).(color.NRGBA))
		}
	}
	return dst
}
