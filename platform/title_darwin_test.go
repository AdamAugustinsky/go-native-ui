//go:build darwin

package platform

import (
	"testing"

	"gonativeui/internal/objc"
)

// SetTitle changes the NSWindow's title at runtime, and SetRepresentedFile and
// SetRepresentedURL give it a proxy icon and take it away again.
func TestSetTitleAndRepresented(t *testing.T) {
	var opened bool
	var fails []string
	onMain(func() {
		w, err := Open(Options{Title: "first", Width: 300, Height: 200, Hidden: true})
		if err != nil {
			return
		}
		defer w.Close()
		opened = true
		get := func(sel string) string { return string(objc.AppendUTF8(nil, objc.Send(w.win, objc.Sel(sel)))) }
		check := func(what, sel, want string) {
			if got := get(sel); got != want {
				fails = append(fails, what+": "+sel+" = "+got+", want "+want)
			}
		}
		check("opened", "title", "first")
		w.SetTitle("Table — 1 000 000 rows")
		check("SetTitle", "title", "Table — 1 000 000 rows")
		w.SetTitle("")
		check("empty", "title", "")

		w.SetRepresentedFile("/tmp/gnui-represented.txt")
		check("file", "representedFilename", "/tmp/gnui-represented.txt")
		w.SetRepresentedFile("")
		check("file cleared", "representedFilename", "")

		w.SetRepresentedURL("https://example.com/page")
		if u := objc.Send(w.win, objc.Sel("representedURL")); u == 0 {
			fails = append(fails, "SetRepresentedURL: no representedURL")
		} else if got := string(objc.AppendUTF8(nil, objc.Send(u, objc.Sel("absoluteString")))); got != "https://example.com/page" {
			fails = append(fails, "SetRepresentedURL: "+got)
		}
		w.SetRepresentedURL("")
		if u := objc.Send(w.win, objc.Sel("representedURL")); u != 0 {
			fails = append(fails, "SetRepresentedURL(\"\") left a representedURL")
		}
	})
	if !opened {
		t.Skip("no window (a locked screen)")
	}
	for _, f := range fails {
		t.Error(f)
	}
}
