// Package widgettest drives an application the way a person does, for tests,
// benchmarks and screenshot scenes: it finds what to act on by its role and
// name in the semantic tree of the last frame, moves the pointer there, presses,
// types and waits for conditions, through ui.Input exactly as translated
// platform events would.
//
//	r := widgettest.Open(t, cfg, 420, 560, 1, myApp) // an offscreen window on the real renderer
//	r.Play(
//		widgettest.Click(ui.RoleTextField, "New note"),
//		widgettest.Type("Buy bread"),
//		widgettest.Key(ui.KeyEnter, 0),
//		widgettest.WaitFor(ui.RoleListItem, "Buy bread"),
//	)
//	if _, ok := r.Find(ui.RoleListItem, "Buy bread"); !ok { … }
//
// A step names its target, "the Add button", not a coordinate, so a test
// survives a change of layout, font or scale. Waits are on conditions, never on
// frame counts, because a window whose display is asleep draws as fast as it
// can and a paced one at 60 Hz.
//
// # Pieces
//
// Step, built by Click, Key, Type, WaitFor and the other constructors, is one
// thing a person does. Player runs a list of steps one frame at a time and
// implements app.Scripter, so a benchmark (app.RunBench) or a screenshot scene
// (app.NewOffscreen) can loop it; it needs only a Host, anything that can name
// the window's ui.Context, such as the application or a widget.Kit.
//
// Rig is an application in a window of its own, and the way into tests. Open
// runs it on app.Offscreen (the real text system and renderer, as the
// application runs); NewRig runs it on ui.Context alone with a fake text system
// (faster, no GPU: for the logic of widgets). Both play lists of steps one
// after another, see Rig.Play, and look into the semantic tree (Find, Must,
// Tree, Dump).
//
// # One script after another
//
// An app.Scripter that returns false ends scripting for good: the Offscreen
// and the benchmark stop asking it. A Rig is the Scripter of its window and
// never returns false, so a test plays as many lists of steps as it likes; a
// Player used on its own as a Scripter does return false when its steps are
// done, unless it loops.
//
// # Real windows (macOS)
//
// Offscreen frames take the platform out of the picture. RunLive opens the real
// window for a few frames and reads what the system says about it. StartLive
// keeps it open: a LiveApp runs the app's loop on the main thread (the test
// package calls RunMain from TestMain) while the test goroutine drives it:
//
//	l := widgettest.StartLive(t, cfg, builder)
//	l.Click(ui.RoleButton, "Press")                 // real NSEvents, through the pump
//	l.Type("hello")                                 // key events, the view's keyDown:, the input method
//	l.Scroll(x, y, 0, -125)                         // a trackpad scroll, built from a CGEvent
//	l.Magnify(x, y, 0.5, 3)                         // a pinch, begun, changed, ended
//	l.Until("the dialog is answered", 120, cond)    // frame time, never wall time
//	l.Do(func(a *app.App) { ... AppKit ... })       // on the main thread, between frames
//
// Waits count frames, not seconds, because a runner's display can stall for
// half a minute. Every hand-off to the main thread is bounded in wall time
// (LiveOptions): a callback that never returns, a main thread that never
// answers and a window that never draws end in a failure that says which. A
// function given to Do runs on the main thread: it must not call t.Fatal or
// t.Skip (the failure is reported, naming the mistake). A wait that sees no
// frame looks at the environment and skips only with evidence that it cannot
// draw (a locked screen, a hidden or covered window, a display that does not
// tick while the run loop is alive); otherwise it fails, with the app's state
// line.
//
// The input helpers are also functions of Win (NativeWindow): MouseEvent,
// KeyEvent, Key (the US layout; KeyIn takes another, NamedKeyLayout reads one
// without selecting it), ScrollEvent (whole points: a precise scroll event
// has no fractions), ScrollLinesEvent (a wheel's fractional lines),
// MagnifyEvent, RotateEvent, SmartMagnifyEvent, each building an NSEvent that
// AppKit itself says is aimed at a pixel of the content. Post, Send and
// SendToWindow hand an event to AppKit, and Dispatch picks the way that works
// for its kind (a gesture goes to the window: [NSApp sendEvent:] does not hand
// a synthetic one on to a view). TextClient calls the NSTextInputClient methods
// of a view (setMarkedText, insertText, unmarkText, firstRect…) as an input
// method does; ViewString and SetViewString read and set the text of a hosted
// native view. Windows, FindMenuItem, Sheet, SetAppAppearance, OpenPlainWindow,
// SendQuitEvent and PressCmdQ are the AppKit state and the app-level flows (the
// quit, a menu item, an alert sheet, another window, the appearance).
//
// A flow that ends the process (an allowed quit) is a Scenario: the test binary
// runs again as a child, RegisterScenario's function on its main thread, and
// reports with Say. The flows of the application object (reopen, quit with no
// window, delayed replies, a menu command with no window) are in
// live/appflows_f3_darwin_test.go.
//
// # Golden files, captures and fakes
//
// AssertTree and AssertImage compare with files in testdata, rewrite them with
// -update (DefineUpdateFlag) and leave what the test got in testdata/failed;
// images are compared in CIELAB with a perceptual Tolerance that is strict by
// default (a control that moved by one pixel is seen; AssertImageRect compares
// the control, not the window around it) and Exact compares raw pixels, alpha
// included. ViewImage draws an NSView as AppKit does. Capture takes a
// picture of the real window with GnuiShot when GNUI_GNUISHOT names its
// script. The fakes of native surfaces: FakeDialogs answers an app's alerts and
// panels, and the steps Command, Edit, Appearance and Do deliver a menu
// command, an Edit action, a change of the system's appearance and any
// function at an exact frame.
package widgettest
