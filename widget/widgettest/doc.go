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
package widgettest
