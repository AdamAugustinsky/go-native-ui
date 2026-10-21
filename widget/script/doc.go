// Package script defines and plays a script of input: the steps a person
// takes, found by role and name in the semantic tree, delivered through
// ui.Input as the translated events of a person would be.
//
//	p := script.NewPlayer(host, []script.Step{
//		script.Click(ui.RoleButton, "Add"),
//		script.Type("Buy bread"),
//		script.Key(ui.KeyEnter, 0),
//		script.WaitFor(ui.RoleListItem, "Buy bread"),
//	}, script.Options{})
//
// A Player is an app.Scripter, so it drives a live window, a benchmark or an
// offscreen scene; a demo tour in a release build is the same thing. The
// package imports no test machinery (testing, regexp, flag, os/exec): it is
// safe to link into an application. Tests use widget/widgettest, which builds
// rigs, assertions and real-window drivers on top of it and re-exports every
// name here.
package script
