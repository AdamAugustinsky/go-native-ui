package app

import (
	"errors"
	"sync/atomic"

	"gonativeui/gpu"
	"gonativeui/platform"
	"gonativeui/text"
	"gonativeui/ui"
)

// More than one window (DECISIONS D30, built for macOS in phase 6). Each
// window has an App of its own (its platform window, surface, ui context,
// input, pacer, dialogs, menus state) and a Win handle; they all point at one
// appObject, which owns what they share: the GPU device, the renderer, the
// text system and its glyph atlas (whose pages one set of textures serves
// every window), the font scales kept alive, the loop and its pump, the safe
// points, the app events and the quit policy.
//
// The Handler's App (Main's, or Run's) holds the first window, and the next
// window opened while it has none, so that a one-window app sees its window
// where it always did; every other window gets a new App, which its Builder's
// Init receives.
//
// Window changes happen at safe points: between frames, outside Build and
// outside every event delivery. OpenWindow and Win.Close asked for inside a
// frame or an event are applied after it, and so are the window commands
// (Win.SetTitle, SetFrame, ToggleFullscreen …), which are kept as state and
// applied once (applyCommands), so asking every frame costs nothing.

// Win is a window the app opened (OpenWindow): its handle for window
// commands. The commands are applied at the next safe point (at once when
// called at one: from Start, a safe-point function, or the loop between
// frames); a handle whose window has closed ignores them. Main thread, except
// Wake.
type Win struct {
	a   *App // the window's App, once it opened
	id  uint32
	err error
	pw  atomic.Pointer[platform.Window] // for Wake from other goroutines
}

// Err is the error that kept a window asked for during an event or a frame
// from opening at its safe point (OpenWindow), nil otherwise.
func (w *Win) Err() error { return w.err }

// ID is the window's id (Event.Window); never 0 once it opened.
func (w *Win) ID() uint32 { return w.id }

// Open reports whether the window is still open.
func (w *Win) Open() bool { return w.a != nil && w.a.Win != nil && w.a.winID == w.id }

// App is the window's App: its Builder's view of the app (Win, Surf, UI), nil
// before the window opened.
func (w *Win) App() *App {
	if w.a == nil || w.a.winID != w.id {
		return nil
	}
	return w.a
}

// Close closes the window at the next safe point, without asking its
// CloseRequester: between frames, at the loop's turn or the application
// object's dispatch (which also runs while a quit answer is pending). The app
// ends with its last window unless it keeps running.
func (w *Win) Close() {
	if w.Open() {
		w.a.closing = true
		platform.WakeLoop()
	}
}

// Show shows the window and activates the app.
func (w *Win) Show() { w.command(cmdShow) }

// Hide takes the window off the screen without closing it (Show brings it
// back).
func (w *Win) Hide() { w.command(cmdHide) }

// Focus brings the window to the front, out of the Dock if it is there, and
// makes it the key window.
func (w *Win) Focus() { w.command(cmdFocus) }

// Miniaturize puts the window in the Dock.
func (w *Win) Miniaturize() { w.command(cmdMiniaturize) }

// ToggleFullscreen enters full screen or leaves it. With a custom titlebar
// (Config.Titlebar) the window buttons are the system's own in full screen:
// the ui gets no system-button rectangle there (ui.Context.SetSystemButtons),
// so a titlebar drops their inset, and the buttons are not moved to
// Config.TrafficLights until the window is back.
func (w *Win) ToggleFullscreen() { w.command(cmdFullscreen) }

// Fullscreen reports whether the window is in full screen (or entering it).
func (w *Win) Fullscreen() bool { return w.Open() && w.a.Win.FullScreen() }

// SetTitle sets the window's title (the title bar, the Window menu, Mission
// Control, and ui.Context's window title for a drawn titlebar).
func (w *Win) SetTitle(title string) {
	if !w.Open() || (title == w.a.cfg.Title && w.a.cmd&cmdTitle == 0) {
		return
	}
	w.a.cfg.Title = title
	w.command(cmdTitle)
}

// Frame is the window's frame: the whole window, title bar included, in
// points from the top-left of the primary display, y down (the space of
// Screens). Zero where the OS does not say (Linux).
func (w *Win) Frame() (x, y, width, height float64) {
	if !w.Open() {
		return 0, 0, 0, 0
	}
	return w.a.Win.FrameRect()
}

// SetFrame moves and sizes the window to a frame in Frame's space.
func (w *Win) SetFrame(x, y, width, height float64) {
	if !w.Open() || width <= 0 || height <= 0 {
		return
	}
	w.a.frameWant = [4]float64{x, y, width, height}
	w.command(cmdFrame)
}

// SetDocumentEdited marks the window's document as having unsaved changes
// (macOS: the dot in the close button). Unchanged, it does nothing, so it can
// be called every frame.
func (w *Win) SetDocumentEdited(edited bool) {
	if !w.Open() || (edited == w.a.edited && w.a.cmd&cmdEdited == 0) {
		return
	}
	w.a.edited = edited
	w.command(cmdEdited)
}

// AttentionID identifies a RequestAttention, for CancelAttention; 0 is none.
type AttentionID int

// RequestAttention asks for the user's attention for this window's app: the
// Dock icon bounces once, or, critical, until the app is activated. It
// returns the request's id; 0 when the app is active already (nothing is
// asked) or the OS has no such request.
func (w *Win) RequestAttention(critical bool) AttentionID {
	if !w.Open() {
		return 0
	}
	return AttentionID(platform.RequestAttention(critical))
}

// CancelAttention ends a request RequestAttention made.
func (w *Win) CancelAttention(id AttentionID) { platform.CancelAttention(int(id)) }

// Wake tells this window, from any goroutine, that something it draws
// changed (a worker's result): it gets a frame, and the app's other windows
// do not (platform.Wake wakes them all). Cheap when a wake is pending; it
// does nothing once the window has closed.
func (w *Win) Wake() {
	if pw := w.pw.Load(); pw != nil {
		pw.Wake()
	}
}

// cmdMask is the window commands waiting for a safe point.
type cmdMask uint16

const (
	cmdTitle cmdMask = 1 << iota
	cmdFrame
	cmdEdited
	cmdShow
	cmdHide
	cmdFocus
	cmdMiniaturize
	cmdFullscreen
)

// winState is a window's part of the application object, in its App.
type winState struct {
	handle  *Win   // the window's handle (OpenWindow; Window)
	closing bool   // the window closes at the next safe point
	told    uint32 // the id whose EvWindowClosed was delivered (announceClosed)
	winID   uint32 // its id, 0 before it opened

	waited []platform.Event // the window's events from the last pump, where the pump is its Wait (pump_other.go)

	cmd       cmdMask    // window commands waiting for the safe point
	frameWant [4]float64 // SetFrame's frame
	edited    bool       // SetDocumentEdited's state

	ownTex *winUploader // the textures the window's widgets made on the shared renderer

	held bool // the App has held a window: its state is that window's until renew
}

// command applies window command c now when the app is at a safe point, and
// otherwise leaves it for the next one, which it makes come.
func (w *Win) command(c cmdMask) {
	if !w.Open() {
		return
	}
	a := w.a
	a.cmd |= c
	if a.framing > 0 || a.evDepth > 0 || a.opening > 0 {
		platform.WakeLoop()
		return
	}
	a.applyCommands()
}

// applyCommands carries out the window's commands, at a safe point.
func (a *App) applyCommands() {
	c := a.cmd
	if c == 0 || a.Win == nil {
		return
	}
	a.cmd = 0
	win := a.Win
	if c&cmdTitle != 0 {
		win.SetTitle(a.cfg.Title)
	}
	if c&cmdEdited != 0 {
		win.SetDocumentEdited(a.edited)
	}
	if c&cmdFrame != 0 {
		f := a.frameWant
		win.SetFrameRect(f[0], f[1], f[2], f[3])
	}
	if c&cmdHide != 0 {
		win.Hide()
	}
	if c&cmdShow != 0 {
		win.Show()
	}
	if c&cmdFocus != 0 {
		win.Activate()
	}
	if c&cmdMiniaturize != 0 {
		win.Miniaturize()
	}
	if c&cmdFullscreen != 0 {
		win.ToggleFullScreen()
	}
	a.p.invalidate() // a title or a frame changes what the window shows
}

// Window is this App's window handle (its Win), nil for an App without a
// window (Main's before its first window opens; RenderOffscreen).
func (a *App) Window() *Win {
	if a.appObject == nil || a.Win == nil {
		return nil
	}
	if a.handle == nil || a.handle.a != a || a.handle.id != a.winID {
		// A Run's window, which OpenWindow did not open.
		if a.winID == 0 {
			a.nextWinID++
			a.winID = a.nextWinID
		}
		a.handle = &Win{a: a, id: a.winID}
		a.handle.pw.Store(a.Win)
	}
	return a.handle
}

// Windows appends the app's open windows to dst, in the order they opened,
// and returns it. Main thread.
func (a *App) Windows(dst []*Win) []*Win {
	for _, w := range a.wins {
		if h := w.Window(); h != nil {
			dst = append(dst, h)
		}
	}
	return dst
}

// Screen is one display: its frame, the part windows can use (without the
// menu bar and the Dock) and its scale, in the space of Win.Frame.
type Screen = platform.ScreenInfo

// Screens returns the displays, the primary one first, in dst's storage
// (dst[:0], grown when short); with room in dst it allocates nothing. Main
// thread. Empty on Linux so far (the displays are not read there yet).
func Screens(dst []Screen) []Screen { return platform.ScreensInto(dst[:0]) }

// The resources the windows share (D30), made with the first window and
// released with the last.

// newSurface makes a window's surface: on the app's GPU device where the OS
// shares one between windows (macOS), else on a device of its own (the one
// window of Windows and Linux).
func (a *App) newSurface(n platform.Native, w, h int32, scale float32) (*gpu.Surface, error) {
	s := a.appObject
	if s.dev == nil && !s.noDev {
		d, err := gpu.NewDevice()
		switch {
		case err == nil:
			s.dev = d
		case errors.Is(err, errors.ErrUnsupported):
			s.noDev = true
		default:
			return nil, err
		}
	}
	if s.dev != nil {
		return gpu.NewSurfaceOn(s.dev, n, w, h, scale)
	}
	return gpu.NewSurface(n, w, h, scale)
}

// sharedRenderer is the app's renderer, made on surf's device by the first
// window, with the shared glyph atlas's page textures on it.
func (a *App) sharedRenderer(surf *gpu.Surface) (*gpu.Renderer, error) {
	s := a.appObject
	if s.r == nil {
		r, err := gpu.NewRenderer(surf.Device(), surf.Queue())
		if err != nil {
			return nil, err
		}
		s.r, s.atlas = r, ui.NewSharedAtlas(r)
	}
	return s.r, nil
}

// sharedText is the app's text system, made by the first window.
func (a *App) sharedText() (*text.System, error) {
	s := a.appObject
	if s.text == nil {
		t, err := text.New()
		if err != nil {
			return nil, err
		}
		s.text = t
	}
	return s.text, nil
}

// winUploader is a window's widgets' way to the shared renderer
// (ui.Context.Uploader: a kit's icon atlas makes its texture with it). It
// remembers what they made and frees it when the window closes: the
// renderer outlives the window and would keep it.
type winUploader struct {
	r    *gpu.Renderer
	made []gpu.TextureID
}

func (u *winUploader) NewTexture(kind gpu.TextureKind, w, h int) gpu.TextureID {
	t := u.r.NewTexture(kind, w, h)
	u.made = append(u.made, t)
	return t
}

func (u *winUploader) Upload(t gpu.TextureID, x, y, w, h int, pixels []byte, stride int) {
	u.r.Upload(t, x, y, w, h, pixels, stride)
}

// FreeTexture frees a texture the window made, for an icon atlas that grew
// into a new one.
func (u *winUploader) FreeTexture(t gpu.TextureID) {
	u.r.FreeTexture(t)
	for i, m := range u.made {
		if m == t {
			u.made = append(u.made[:i], u.made[i+1:]...)
			break
		}
	}
}

// free frees the window's textures (the frames in flight that sample them
// keep them until they finish: Renderer.FreeTexture).
func (u *winUploader) free() {
	for _, t := range u.made {
		u.r.FreeTexture(t)
	}
	u.made = u.made[:0]
}

// releaseShared releases what the windows shared, once none is open: the
// renderer (if a window's close has not), the text system, the atlas's
// textures with them, the font scales and the device. Idempotent.
func (a *App) releaseShared() {
	s := a.appObject
	if s == nil || len(s.wins) > 0 {
		return
	}
	if s.r != nil {
		s.r.Destroy()
		s.r = nil
	}
	if s.text != nil {
		s.text.Close()
		s.text = nil
	}
	s.atlas, s.scales = nil, s.scales[:0]
	if s.dev != nil {
		s.dev.Release()
		s.dev = nil
	}
}

// forget takes the window out of the app's open windows (it is closing).
func (a *App) forget() {
	s := a.appObject
	if s == nil {
		return
	}
	for i, w := range s.wins {
		if w == a {
			s.wins = append(s.wins[:i], s.wins[i+1:]...)
			s.wins[len(s.wins):cap(s.wins)][0] = nil // the backing array keeps no closed window
			break
		}
	}
	if a.handle != nil {
		a.handle.pw.Store(nil)
	}
}

// markMenus has the app's menu bar installed again once no window is open
// (the platform hands a closed window's bar to the next key window's).
func (a *App) markMenus() {
	if s := a.appObject; s.root != nil && len(s.wins) == 0 {
		s.root.menuDirty = true
	}
}

// windowSource is the part of platform.Window that syncWindow reads and
// drives. *platform.Window has all of it on every OS (the calls that do not
// apply are no-ops there).
type windowSource interface {
	ClientDecorations() bool
	Maximized() bool
	Minimize()
	ToggleMaximize()
	RequestClose()
}

// buttonsSource is the part of the window that knows where the system draws
// the window buttons over the content (macOS with a custom titlebar), in
// points.
type buttonsSource interface{ SystemButtons() [4]float32 }

// syncWindow hands the window's state to the UI for the frame about to be
// built, as syncAppearance does the system appearance, and carries out what
// the UI asked of the window in the last one:
//
//   - ui.Context.SetSystemButtons: where the OS draws the window buttons over
//     the content (in pixels; empty where it draws none there), so that a
//     titlebar leaves them room;
//   - ui.Context.SetDecorations: whether the app has to draw the titlebar and
//     whether the window is maximized (Wayland client-side decorations);
//   - ui.Context.SetWindowTitle: Config.Title, for that titlebar;
//   - the window action a titlebar button requested (close, minimise,
//     maximise), which asked for the frame it is carried out in.
//
// It reads cached values and allocates nothing.
func (a *App) syncWindow() {
	if a.Win != nil { // a nil *platform.Window in the interface would not be nil
		a.syncWindowFrom(a.Win, a.buttons)
	}
}

// syncWindowFrom is syncWindow on win; b is win's buttonsSource, nil when it
// has none.
func (a *App) syncWindowFrom(win windowSource, b buttonsSource) {
	switch a.UI.TakeWindowAction() {
	case ui.ActionClose:
		win.RequestClose()
	case ui.ActionMinimise:
		win.Minimize()
	case ui.ActionToggleMaximise:
		win.ToggleMaximize()
	}
	a.syncTitlebar(win)
	a.UI.SetDecorations(ui.Decorations{Client: win.ClientDecorations(), Maximized: win.Maximized()})
	a.UI.SetWindowTitle(a.cfg.Title)
	var r [4]float32
	if b != nil {
		p := b.SystemButtons()
		r = [4]float32{p[0] * a.scale, p[1] * a.scale, p[2] * a.scale, p[3] * a.scale}
	}
	a.UI.SetSystemButtons(r) // after syncTitlebar: where the buttons are now
}

// scaleInUse reports whether an open window draws at effective scale s now:
// its fonts stay alive (useFontScale).
func (a *App) scaleInUse(s float32) bool {
	for _, w := range a.wins {
		if w.EffectiveScale() == s {
			return true
		}
	}
	return false
}
