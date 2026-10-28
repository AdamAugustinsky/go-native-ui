// Command webdemo is the hosted browser view's demo (app/webview, the phase 8
// plan's H1): a Rex toolbar with back, forward, reload or stop and the
// address field, over a rexui.WebView pane with a persistent profile of its
// own. Pages open in place; downloads go to ~/Downloads; camera and
// microphone are denied.
//
//	webdemo                         run it (https://example.com)
//	webdemo -url http://localhost:5173/
//	webdemo -o out.png [-dark]      render the chrome offscreen (no view there) and exit
//	webdemo -selftest camera        load a page that asks for the camera, print what
//	                                the policy did and exit (run it bundled, with
//	                                gnui run, for WebKit to ask: gnui.json's usage)
package main

import (
	"errors"
	"flag"
	"fmt"
	"image/png"
	"os"
	"path/filepath"
	"strings"

	"gonativeui/app"
	"gonativeui/app/webview"
	"gonativeui/icon"
	"gonativeui/platform"
	"gonativeui/rexui"
	"gonativeui/theme"
	"gonativeui/theme/rex"
	"gonativeui/ui"
	"gonativeui/widget"
)

const style = theme.ToolbarRex

func main() {
	start := flag.String("url", "https://example.com/", "the first page")
	profile := flag.String("profile", "webdemo", "the persistent profile (\"\": a non-persistent session)")
	out := flag.String("o", "", "render the window offscreen at 2× into this PNG and exit")
	dark := flag.Bool("dark", false, "with -o: in dark mode")
	selftest := flag.String("selftest", "", "camera: ask a loopback page for the camera, print the outcome and exit")
	flag.Parse()
	d := &demo{start: *start, profile: *profile}
	var err error
	switch {
	case *out != "":
		err = d.render(*out, *dark)
	case *selftest == "camera":
		err = d.cameraSelfTest()
	case *selftest != "":
		err = fmt.Errorf("unknown self-test %q", *selftest)
	default:
		err = app.Main(app.AppConfig{ID: "dev.gonativeui.webdemo"}, d)
	}
	if err != nil {
		fmt.Fprintln(os.Stderr, "webdemo:", err)
		os.Exit(1)
	}
}

// config is the window: the Rex toolbar's titlebar, the system's material
// behind the field (Rex's transparency).
func config() app.Config {
	cfg := app.Config{Title: "Browser", Width: 1000, Height: 700, MinWidth: 520, MinHeight: 360, Backdrop: platform.BackdropGlass}
	cfg.Titlebar, cfg.TrafficLights = rexui.TitlebarFor(style)
	return cfg
}

// demo is the app: one window, one view.
type demo struct {
	app.DefaultHandler
	start, profile string

	a        *app.App // the window's
	k        *widget.Kit
	v        *webview.View
	addr     ui.TextBuffer
	addrKey  ui.Key
	shownURL string // the URL the address field last took from the view
	notice   string
	danger   bool
	backdrop bool
	icBack   icon.ID
	icFwd    icon.ID
	icReload icon.ID
	icStop   icon.ID

	onEvent func(ev webview.Event) // the self-test's
}

func (d *demo) Start(a *app.App) error {
	_, err := a.OpenWindow(config(), d)
	return err
}

func (d *demo) Init(a *app.App) error {
	d.a = a
	d.k = widget.NewKit(a.UI, a.Text, rex.Theme())
	pt := rexui.ToolbarSymbol
	d.icBack = d.k.AddIcon(pt("chevron.left", '‹'))
	d.icFwd = d.k.AddIcon(pt("chevron.right", '›'))
	d.icReload = d.k.AddIcon(pt("arrow.clockwise", '↻'))
	d.icStop = d.k.AddIcon(pt("xmark", '✕'))
	d.addr.Max = 8192
	d.addr.Reserve(8192)
	if a.Win == nil {
		return nil // offscreen: the pane says there is no view
	}
	d.backdrop = a.Win.Backdrop() != platform.BackdropNone
	home, _ := os.UserHomeDir()
	v, err := webview.New(a.Win, webview.Options{
		Profile:      d.profile,
		NewWindows:   webview.NewWindowInPlace,
		DownloadDir:  filepath.Join(home, "Downloads"),
		CornerRadius: rexui.WebViewLook.ContentRadius,
	})
	switch {
	case errors.Is(err, webview.ErrUnavailable), errors.Is(err, errors.ErrUnsupported):
		d.notice = "No browser view on this system: pages open in your browser."
		return nil
	case err != nil:
		return err
	}
	d.v = v
	d.addr.SetBytes([]byte(d.start))
	return v.Navigate(d.start)
}

// Event follows the view: the notice line, and a frame for what changed.
func (d *demo) Event(a *app.App, e *app.Event) {
	if ev, ok := webview.EventOf(e); ok && d.v != nil && ev.View == d.v.ID() {
		switch ev.Kind {
		case webview.LoadStarted:
			d.notice, d.danger = "", false
		case webview.LoadFailed:
			d.notice, d.danger = "The page could not be loaded.", true
		case webview.ProcessFailed:
			d.notice, d.danger = "The page stopped working. Reload to try again.", true
		case webview.NavigationBlocked:
			d.notice, d.danger = "Blocked a link to "+d.v.Blocked(), true
		case webview.PermissionDenied:
			d.notice, d.danger = "This page asked for the camera or microphone, which the browser does not allow.", false
		case webview.DownloadEnded:
			if dl, ok := d.v.Download(ev.Download); ok && dl.State == webview.DownloadDone {
				d.notice, d.danger = "Downloaded "+filepath.Base(dl.Path), false
			} else {
				d.notice, d.danger = "A download failed.", true
			}
		}
		if d.onEvent != nil {
			d.onEvent(ev)
		}
	}
	a.DefaultEvent(e)
}

func (d *demo) Build(c *ui.Context) {
	k := d.k
	k.Begin(c)
	k.Theme().SetBackdrop(d.backdrop)
	var st webview.State
	var host platform.HostID
	if d.v != nil {
		st, host = d.v.State(), d.v.Host()
		if st.URL != d.shownURL && c.FocusKey() != d.addrKey {
			d.shownURL = st.URL
			d.addr.SetBytes([]byte(st.URL))
		}
	}

	bandPt, _ := rexui.TitlebarFor(style)
	k.NextPad(10)
	k.AddNextPadTop(bandPt - 10)
	k.BeginWindow()
	rexui.WebView(k.Kit, "Page", host, rexui.WebViewOpts{
		Loading: st.Loading, Progress: st.Progress,
		Notice: d.notice, Danger: d.danger,
		Empty: "No page: the browser view is not available here.",
	})
	k.EndWindow()

	tb := rexui.BeginToolbar(k.Kit, "Toolbar", rexui.ToolbarOpts{Style: style})
	back, fwd := tb.Nav(d.icBack, d.icFwd, st.CanGoBack, st.CanGoForward)
	reload := d.icReload
	if st.Loading {
		reload = d.icStop
	}
	reloadSig := tb.Item(reload, "Reload")
	tb.BeginCustom("Address", ui.Pct(1))
	c.NextSize(ui.Pct(1), ui.Pct(1))
	c.NextAxis(ui.AxisX)
	c.NextCross(ui.CrossCenter)
	c.NextPadding(0, 0, c.Pt(10), 0)
	c.Push(c.Box(0, "address row"))
	f := rexui.TextField(k.Kit, "Address", &d.addr, "Search or enter an address", rexui.FieldOpts{})
	c.Pop()
	d.addrKey = f.Key
	tb.EndCustom()
	tb.End()
	k.End()

	if d.v == nil {
		return
	}
	switch {
	case back.Clicked:
		d.v.Back()
	case fwd.Clicked:
		d.v.Forward()
	case reloadSig.Clicked && st.Loading:
		d.v.Stop()
	case reloadSig.Clicked:
		d.v.Reload()
	case f.Submitted:
		if err := d.v.Navigate(addressURL(d.addr.String())); err != nil {
			d.notice, d.danger = "That address cannot be opened here.", true
		}
		c.SetFocus(0)
	}
}

// addressURL is what the address field's text opens: as typed with a
// scheme; http:// for localhost and loopback addresses; https:// otherwise.
func addressURL(s string) string {
	s = strings.TrimSpace(s)
	if strings.Contains(s, "://") || strings.HasPrefix(s, "about:") || strings.HasPrefix(s, "data:") {
		return s
	}
	if strings.HasPrefix(s, "localhost") || strings.HasPrefix(s, "127.") || strings.HasPrefix(s, "[::1]") {
		return "http://" + s
	}
	return "https://" + s
}

// render draws the window offscreen at 2× into path.
func (d *demo) render(path string, dark bool) error {
	cfg := config()
	o, err := app.NewOffscreen(cfg, int(2*cfg.Width), int(2*cfg.Height), 2, d)
	if err != nil {
		return err
	}
	defer o.Close()
	if dark {
		d.k.SetMode(theme.Dark)
	} else {
		d.k.SetMode(theme.Light)
	}
	for range 3 {
		o.Frame()
	}
	img, _ := o.Frame()
	f, err := os.Create(path)
	if err != nil {
		return err
	}
	defer f.Close()
	return png.Encode(f, img)
}
