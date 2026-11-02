// Package webview hosts the operating system's own browser view inside a
// window, as content (the phase 8 plan's H1, H2, D64 and D65): a pane that
// shows web pages and the user's dev servers. It is not a way to build UI:
// there is no injected script and no message channel from the page to the
// app, and the framework's own interface never uses it (DECISIONS D2,
// amended by D64). The app may run a script of its own in the page and take
// an image of it (Eval, Capture: D94), so that something acting for the
// user, such as an agent, can read and drive the page the user sees.
//
//	v, err := webview.New(a.Win, webview.Options{
//		Profile:     "a3-browser",        // "" : a non-persistent session
//		DownloadDir: downloads,           // "" refuses downloads
//		NewWindows:  webview.NewWindowInPlace,
//	})
//	if errors.Is(err, webview.ErrUnavailable) {
//		// no runtime (WebView2 on some Windows 10 images): open in the browser instead
//	}
//	v.Navigate("http://localhost:5173/")
//
//	// in Build: the view is a hosted native view in a box
//	c.Host(v.Host(), ...)
//	st := v.State() // title, URL, loading, progress, back/forward: no allocation
//
//	func (b *browser) Event(a *app.App, e *app.Event) {
//		if ev, ok := webview.EventOf(e); ok && ev.View == v.ID() {
//			switch ev.Kind {
//			case webview.TitleChanged: // v.State().Title
//			case webview.NavigationBlocked: // v.Blocked(): a scheme not allowed
//			case webview.DownloadEnded: // v.Download(ev.Download).Path
//			}
//		}
//		a.DefaultEvent(e)
//	}
//
// # The contract
//
//   - Navigation: Navigate, Reload, Stop, Back, Forward; the title, URL,
//     loading state and progress as events and in State.
//   - The policy is the app's, given in Options and applied by the view
//     without a round trip: schemes outside Options.Schemes are blocked
//     (NavigationBlocked); a page's request for a new window loads in place
//     or is refused (Options.NewWindows).
//   - Permissions (camera, microphone, location, notifications, clipboard
//     reading) are denied unless Options.Allow names them; a denial is a
//     PermissionDenied event.
//   - Downloads go into Options.DownloadDir with the server's file name made
//     unique, reported with progress; none when DownloadDir is "".
//   - The session is non-persistent by default (no cookie or storage
//     outlives the view); Options.Profile names a persistent profile kept
//     in the app's data folder, isolated from the app and from other
//     profiles.
//   - The engine's context menu and web inspector are on unless
//     Options.NoContextMenu, and only with Options.Inspectable.
//   - Eval runs the app's script in the main frame and the page's world,
//     awaited, and gives its value as JSON; Capture gives a PNG of what the
//     view shows. Both answer later, with EvalEnded and CaptureEnded, and
//     Result takes the answer.
//
// # Threads and cost
//
// Main thread only, between frames. The engine loads on the first New,
// never at startup; its processes (WebKit's WebContent, WebView2's browser
// and renderers) are the system's, reported but not budgeted. An idle view
// adds 0 presents to the app; a hidden view is suspended (WebView2's
// TrySuspend).
//
// # What each OS does
//
//   - macOS: WKWebView (WebKit) as a hosted view (D41), with a fresh
//     nonPersistentDataStore per view, or dataStoreForIdentifier: for a
//     profile (macOS 14; the identifier is derived from the profile's name,
//     and WebKit keeps the data under ~/Library/WebKit/<app>/), and its
//     navigation, UI and download delegates (H1). Camera and microphone
//     requests reach the policy only in an app whose Info.plist has
//     NSCameraUsageDescription or NSMicrophoneUsageDescription (gnui.json's
//     usage); without them WebKit hides navigator.mediaDevices. WebKit has no
//     public delegate for location, web notifications or clipboard reads on
//     macOS: they stay denied whatever Allow says. A profile's cookies reach
//     the disk a moment (about 2 s) after they change.
//   - Windows: WebView2 (the Evergreen runtime, a Windows component) as a
//     child window the window hosts (D66), loaded without WebView2Loader.dll
//     (D65), in one environment per process whose data is in
//     Options.DataDir (H2). A non-persistent session is InPrivate in a
//     profile of its own, deleted with the view; a named profile is a
//     WebView2 profile of that name. The engine and each view start
//     asynchronously: New returns at once, and a URL given to Navigate
//     before the view is ready loads when it is. WebView2 reports no load
//     progress: Progress steps through 0.1, 0.4 and 0.7 to 1 (start, content,
//     DOMContentLoaded, done). Downloads report their bytes about every
//     500 ms (Chromium's cadence). Location, notifications and clipboard
//     reads reach the policy too. A missing runtime is ErrUnavailable, with
//     RuntimeURL for the app to offer. A hidden view (its host not built) is
//     suspended (TrySuspend) and resumed when shown. Tab out of the page
//     comes back as the host's EvHostCommand. The window needs no
//     app.Config.ChildWindows: WebView2 presents with DirectComposition.
//   - Linux: ErrUnsupported (WebKitGTK would bring GTK, D2).
package webview

import (
	"errors"
	"fmt"
	"net/url"
	"os"
	"path/filepath"
	"strings"

	"gonativeui/app"
	"gonativeui/platform"
)

var (
	// ErrUnsupported: no hosted browser view here (yet). It matches
	// errors.ErrUnsupported.
	ErrUnsupported = fmt.Errorf("webview: %w", errors.ErrUnsupported)
	// ErrUnavailable: the OS supports a view but its runtime is missing
	// (WebView2 not installed); the app offers RuntimeURL or opens pages in
	// the system browser.
	ErrUnavailable = errors.New("webview: the browser runtime is not installed")
	// ErrRuntime: the runtime is there but could not be started (D65: the
	// export our loader calls changed, or the runtime failed); the error
	// carries the reason.
	ErrRuntime = errors.New("webview: the browser runtime failed to start")
	// ErrNoWindow: New was given no window (an offscreen render).
	ErrNoWindow = errors.New("webview: no window to host the view")
	// ErrClosed: the view was closed.
	ErrClosed = errors.New("webview: the view is closed")
	// ErrBadURL: the URL does not parse, or its scheme is not allowed.
	ErrBadURL = errors.New("webview: URL not allowed")
	// ErrProfile: the profile name is not a plain name (letters, digits,
	// '-', '_' and '.', at most 64 bytes).
	ErrProfile = errors.New("webview: invalid profile name")
)

// RuntimeURL is where the WebView2 Evergreen runtime's bootstrapper is
// downloaded from, for an app that offers to install it.
const RuntimeURL = "https://go.microsoft.com/fwlink/p/?LinkId=2124703"

// NewWindowPolicy is what happens when a page asks for a new window
// (window.open, target=_blank).
type NewWindowPolicy uint8

// The new-window policies.
const (
	// NewWindowInPlace loads the new window's URL in this view (if its
	// scheme is allowed).
	NewWindowInPlace NewWindowPolicy = iota
	// NewWindowRefuse refuses it; a NewWindowRefused event reports it, and
	// Blocked returns its URL.
	NewWindowRefuse
)

// Permission is a set of permissions a page may be granted.
type Permission uint16

// The permissions, denied unless Options.Allow names them.
const (
	// PermCamera is the camera.
	PermCamera Permission = 1 << iota
	// PermMicrophone is the microphone.
	PermMicrophone
	// PermLocation is the location.
	PermLocation
	// PermNotifications is web notifications.
	PermNotifications
	// PermClipboardRead is reading the clipboard from script.
	PermClipboardRead
)

// DefaultSchemes are the schemes a view navigates to when Options.Schemes
// is empty.
var DefaultSchemes = []string{"http", "https", "about", "data", "blob"}

// Options configure a view (New).
type Options struct {
	// Profile names a persistent profile ("a3-browser"); "" is a
	// non-persistent session, the default.
	Profile string
	// Schemes are the URL schemes the view may navigate to, in lower case
	// (nil: DefaultSchemes). Others are blocked.
	Schemes []string
	// NewWindows is the new-window policy.
	NewWindows NewWindowPolicy
	// Allow are the permissions granted to every page (none by default).
	Allow Permission
	// DownloadDir is where downloads are written; "" refuses them.
	DownloadDir string
	// NoContextMenu turns off the engine's own context menu.
	NoContextMenu bool
	// Inspectable allows the engine's web inspector (a development build's
	// choice).
	Inspectable bool
	// ApplicationName is appended to the engine's user agent
	// ("a3-app/1.4").
	ApplicationName string
	// CornerRadius rounds the view's corners, in points, so that it sits in
	// a rounded pane (rexui.WebView's: rexui.WebViewLook.ContentRadius).
	CornerRadius float32
	// DataDir is where the engine keeps its data on Windows (WebView2's user
	// data folder, shared by the process's views: the first New's is used);
	// "" is WebView2 in the cache folder named for the executable
	// (app/paths.Cache). An app passes its own, filepath.Join of
	// paths.Cache(a.ID()) and "WebView2". macOS ignores it: WebKit keeps a
	// profile's data under ~/Library/WebKit/<bundle id>/.
	DataDir string
}

// State is what the view shows now. Its strings are valid until the next
// event of the view.
type State struct {
	// Title is the page's title.
	Title string
	// URL is the URL shown (after redirects).
	URL string
	// Loading is a navigation in progress, Progress its estimate in [0, 1].
	Loading  bool
	Progress float32
	// CanGoBack and CanGoForward say whether Back and Forward do anything.
	CanGoBack, CanGoForward bool
}

// DownloadState is where a download is.
type DownloadState uint8

// The download states.
const (
	// DownloadRunning is in progress.
	DownloadRunning DownloadState = iota + 1
	// DownloadDone is complete, at Download.Path.
	DownloadDone
	// DownloadFailed failed or was cancelled; nothing is left at Path.
	DownloadFailed
)

// Download is one download of a view.
type Download struct {
	// ID names it in events (Event.Download).
	ID uint32
	// Path is the file it is written to.
	Path string
	// URL is where it comes from.
	URL string
	// Received and Total are bytes so far and the announced length (-1:
	// unknown).
	Received, Total int64
	// State is where it is.
	State DownloadState
}

// EventKind is what happened to a view.
type EventKind uint8

// The view events.
const (
	// TitleChanged: State().Title changed.
	TitleChanged EventKind = iota + 1
	// URLChanged: State().URL changed (and with it the back/forward state).
	URLChanged
	// LoadStarted, LoadFinished and LoadFailed bracket a navigation;
	// Progress reports its estimate in between (coalesced: the latest
	// value).
	LoadStarted
	LoadFinished
	LoadFailed
	Progress
	// NavigationBlocked: a navigation to a scheme outside Options.Schemes
	// was blocked (Blocked returns its URL).
	NavigationBlocked
	// NewWindowRefused: a page's new window was refused (NewWindowRefuse).
	NewWindowRefused
	// PermissionDenied: a page asked for a permission Options.Allow does not
	// grant; Event.Permission says which.
	PermissionDenied
	// DownloadStarted, DownloadProgress and DownloadEnded follow a download
	// (Event.Download; View.Download reads it).
	DownloadStarted
	DownloadProgress
	DownloadEnded
	// ProcessFailed: the engine's content process ended; the view shows
	// nothing until Reload.
	ProcessFailed
	// EvalEnded and CaptureEnded: a script run by Eval, or a Capture, is done;
	// Event.Request is its ID, and View.Result takes what it gave.
	EvalEnded
	CaptureEnded
)

// Event is a decoded view event.
type Event struct {
	// Kind is what happened.
	Kind EventKind
	// View is the view's ID.
	View uint32
	// Download is the download of a download event.
	Download uint32
	// Request is the Eval or Capture of an EvalEnded or CaptureEnded event.
	Request uint32
	// Permission is the permission of a PermissionDenied event.
	Permission Permission
}

// EventOf decodes a view event (an app.EvService from
// platform.SourceWebView: ID is the view, A the kind and the permission, B
// the download or the request). It allocates nothing.
func EventOf(e *app.Event) (Event, bool) {
	if e == nil || e.Kind != app.EvService || e.Source != platform.SourceWebView {
		return Event{}, false
	}
	ev := Event{Kind: EventKind(uint8(e.A)), Permission: Permission(e.A >> 8), View: uint32(e.ID)}
	switch ev.Kind {
	case EvalEnded, CaptureEnded:
		ev.Request = uint32(e.B)
	default:
		ev.Download = uint32(e.B)
	}
	return ev, true
}

// Result is what an Eval or a Capture gave.
type Result struct {
	// JSON is an Eval's value, as JSON.stringify writes it ("null" for
	// undefined or a value JSON cannot hold).
	JSON string
	// PNG is a Capture's image: what the view shows, at the display's scale.
	PNG []byte
	// Err is why it failed: ErrScript with the page's message for a script
	// that threw or rejected, ErrClosed for a view closed first, or the
	// engine's error.
	Err error
}

// ErrScript: the script given to Eval threw, or the promise it awaited was
// rejected; the error carries the page's message.
var ErrScript = errors.New("webview: the script failed")

// calls is a backend's record of its Evals and Captures: those running and
// the results not yet taken (main thread).
type calls struct {
	running map[uint32]bool
	done    map[uint32]Result
}

// nextCall numbers the Evals and Captures of every view (main thread).
var nextCall uint32

func (c *calls) start() uint32 {
	nextCall++
	if c.running == nil {
		c.running, c.done = map[uint32]bool{}, map[uint32]Result{}
	}
	c.running[nextCall] = true
	return nextCall
}

// end records id's result and reports whether id was running (a second end,
// or one after the view closed, is dropped).
func (c *calls) end(id uint32, r Result) bool {
	if !c.running[id] {
		return false
	}
	delete(c.running, id)
	c.done[id] = r
	return true
}

func (c *calls) take(id uint32) (Result, bool) {
	r, ok := c.done[id]
	delete(c.done, id)
	return r, ok
}

// asyncBody is script as the body of an async function whose result is the
// JSON of what script returns, the form both engines run (macOS as
// callAsyncJavaScript's body, Windows inside an async arrow).
func asyncBody(script string) string {
	return "const __gnui = await (async () => {\n" + script + "\n})();\nreturn JSON.stringify(__gnui === undefined ? null : __gnui) ?? \"null\";"
}

// View is a hosted browser view.
type View struct {
	id     uint32
	host   platform.HostID
	opt    Options
	be     backend
	closed bool
}

// backend is the OS's view.
type backend interface {
	navigate(u string) error
	reload()
	stop()
	back()
	forward()
	state() State
	blocked() string
	download(id uint32) (Download, bool)
	cancelDownload(id uint32)
	// eval runs body (asyncBody's) for call id, capture captures for id;
	// each ends with end(id, …) and an EvalEnded or CaptureEnded event, or
	// fails at once.
	eval(id uint32, body string) error
	capture(id uint32) error
	start() uint32
	end(id uint32, r Result) bool
	take(id uint32) (Result, bool)
	close()
}

// New creates a view hosted in window w (its host is placed in Build with
// ui.Context.Host). It loads the engine on the first call.
func New(w *platform.Window, o Options) (*View, error) {
	if w == nil {
		return nil, ErrNoWindow
	}
	if !validProfile(o.Profile) {
		return nil, fmt.Errorf("%w: %q", ErrProfile, o.Profile)
	}
	if o.Schemes == nil {
		o.Schemes = DefaultSchemes
	}
	v := &View{opt: o}
	var err error
	if v.be, v.host, v.id, err = osNew(w, &v.opt); err != nil {
		return nil, err
	}
	return v, nil
}

// ID names the view in events (Event.View).
func (v *View) ID() uint32 { return v.id }

// Host is the view's host in its window, for ui.Context.Host.
func (v *View) Host() platform.HostID { return v.host }

// Navigate loads rawURL. A URL whose scheme is not allowed is refused with
// ErrBadURL (and not reported as NavigationBlocked, which is for the page's
// own navigations).
func (v *View) Navigate(rawURL string) error {
	if v.closed {
		return ErrClosed
	}
	u, err := url.Parse(rawURL)
	if err != nil || !v.allowed(u.Scheme) {
		return fmt.Errorf("%w: %q", ErrBadURL, rawURL)
	}
	return v.be.navigate(rawURL)
}

// Reload reloads the page (and restarts a failed content process).
func (v *View) Reload() {
	if !v.closed {
		v.be.reload()
	}
}

// Stop stops loading.
func (v *View) Stop() {
	if !v.closed {
		v.be.stop()
	}
}

// Back goes back in the view's history, if it can.
func (v *View) Back() {
	if !v.closed {
		v.be.back()
	}
}

// Forward goes forward, if it can.
func (v *View) Forward() {
	if !v.closed {
		v.be.forward()
	}
}

// State is what the view shows now. It allocates nothing.
func (v *View) State() State {
	if v.closed {
		return State{}
	}
	return v.be.state()
}

// Blocked is the URL of the last blocked navigation or refused window.
func (v *View) Blocked() string {
	if v.closed {
		return ""
	}
	return v.be.blocked()
}

// Download reads download id.
func (v *View) Download(id uint32) (Download, bool) {
	if v.closed {
		return Download{}, false
	}
	return v.be.download(id)
}

// CancelDownload cancels download id; it ends as DownloadFailed.
func (v *View) CancelDownload(id uint32) {
	if !v.closed {
		v.be.cancelDownload(id)
	}
}

// Eval runs script in the page's main frame, in the page's own world (it
// sees the page's globals and DOM), as the body of an async function: it may
// await, and what it returns, JSON-serialisable, is the result. The result
// comes later, with an EvalEnded event whose Request is the ID returned;
// Result takes it. A navigation that replaces the page before the script
// ends may end it with an error. The app chooses what to run: the page has no
// channel back to the app (D94).
func (v *View) Eval(script string) (uint32, error) {
	if v.closed {
		return 0, ErrClosed
	}
	id := v.be.start()
	if err := v.be.eval(id, asyncBody(script)); err != nil {
		v.be.end(id, Result{})
		v.be.take(id)
		return 0, err
	}
	return id, nil
}

// Capture takes an image of what the view shows (the visible part of the
// page, at the display's scale), as a PNG: a CaptureEnded event whose
// Request is the ID returned, and Result takes it.
func (v *View) Capture() (uint32, error) {
	if v.closed {
		return 0, ErrClosed
	}
	id := v.be.start()
	if err := v.be.capture(id); err != nil {
		v.be.end(id, Result{})
		v.be.take(id)
		return 0, err
	}
	return id, nil
}

// Result takes the result of Eval or Capture id, once it ended: a second call
// reports false. Results not taken are kept until the view closes.
func (v *View) Result(id uint32) (Result, bool) {
	if v.closed {
		return Result{}, false
	}
	return v.be.take(id)
}

// Close releases the view and its host; a non-persistent session's data
// goes with it. A second Close does nothing.
func (v *View) Close() {
	if !v.closed {
		v.closed = true
		v.be.close()
	}
}

func (v *View) allowed(scheme string) bool {
	scheme = strings.ToLower(scheme)
	for _, s := range v.opt.Schemes {
		if s == scheme {
			return true
		}
	}
	return false
}

// validProfile accepts "" and plain names.
func validProfile(p string) bool {
	if len(p) > 64 || p == "." || p == ".." {
		return false
	}
	for i := 0; i < len(p); i++ {
		c := p[i]
		if !(c >= 'a' && c <= 'z' || c >= 'A' && c <= 'Z' || c >= '0' && c <= '9' || c == '-' || c == '_' || c == '.') {
			return false
		}
	}
	return true
}

// uniquePath is a file in dir for suggested, unused on disk and not taken by
// the view's other downloads: "name.ext", then "name (2).ext" …
func uniquePath(dir, suggested string, taken func(path string) bool) (string, error) {
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return "", err
	}
	name := cleanName(suggested)
	ext := filepath.Ext(name)
	stem := strings.TrimSuffix(name, ext)
	for i := 1; i < 10000; i++ {
		p := filepath.Join(dir, name)
		if i > 1 {
			p = filepath.Join(dir, fmt.Sprintf("%s (%d)%s", stem, i, ext))
		}
		if _, err := os.Lstat(p); err == nil {
			continue
		}
		if !taken(p) {
			return p, nil
		}
	}
	return "", os.ErrExist
}

// cleanName is a server's file name made safe to create: its last element,
// no separators or control characters, not hidden; "download" if nothing is
// left.
func cleanName(s string) string {
	s = filepath.Base(strings.ReplaceAll(s, "\\", "/"))
	s = strings.Map(func(r rune) rune {
		if r < 0x20 || r == 0x7F || r == '/' || r == ':' {
			return '_'
		}
		return r
	}, s)
	s = strings.TrimLeft(strings.TrimSpace(s), ".")
	if s == "" {
		return "download"
	}
	return s
}
