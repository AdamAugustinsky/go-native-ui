package media_test

import (
	"fmt"

	"gonativeui/gpu"
	"gonativeui/internal/fakeui"
	"gonativeui/rexui/media"
	"gonativeui/theme/rex"
	"gonativeui/ui"
	"gonativeui/widget/base"
)

// A tool's results as a strip: pictures and a video as thumbnails (a 16:9 frame
// while their bytes load), an audio and a file as chips; past StripLook.Max
// tiles the last place is a "+N" tile. Strip reports the tile clicked (-1 none),
// which the app opens in a Lightbox.
func Example() {
	ft := fakeui.New()
	c := ui.New(ft)
	c.AtlasSync(&fakeui.Uploader{})
	k := base.NewKit(c, ft, rex.Theme())
	var in ui.Input
	var list gpu.DrawList
	in.Inside, in.Focused = true, true
	in.MouseX, in.MouseY = -100, -100

	tiles := []media.Tile{
		{Item: media.Item{Hash: 1, Name: "farol.png"}},
		{Item: media.Item{Hash: 2, Name: "teaser.mp4"}, Kind: media.TileVideo, Meta: "0:12"},
		{Item: media.Item{Name: "locução.mp3"}, Kind: media.TileAudio, Meta: "1:04"},
		{Item: media.Item{Name: "a.png"}},
		{Item: media.Item{Name: "b.png"}},
	}
	for range 2 {
		c.Begin(&in, 900, 400, 1, 1.0/60)
		k.Begin(c)
		if i := media.Strip(k, "Mídia", tiles, media.StripOpts{}); i >= 0 {
			fmt.Println("open", tiles[i].Name)
		}
		k.End()
		c.End(&list)
		in.ClearFrame()
	}

	var tree ui.SemTree
	c.Semantics(&tree)
	for _, n := range tree.Nodes {
		if n.Role == ui.RoleButton {
			fmt.Printf("%q\n", tree.Text(n.Name))
		}
	}
	// Output:
	// "farol.png"
	// "teaser.mp4"
	// "locução.mp3"
	// "+2"
}
