// Command probe-p10 is phase 8's probe P10 (docs/PHASE8-PLAN.md, "How
// Windows is verified"): on macOS, is a Carbon hotkey handler reached
// through the ffi callback table (internal/ffi, D36), with no cgo and no
// permission, when a synthetic hotkey event is sent to the application
// event target? It decides W7's macOS design (D70).
//
//	go run ./cmd/probe-p10            # one JSON object on stdout
//	go run ./cmd/probe-p10 -n 10000   # more synthetic presses for the allocation count
//
// What it does, on the main thread:
//  1. loads Carbon (HIToolbox) and installs one event handler for
//     kEventClassKeyboard/kEventHotKeyPressed on GetApplicationEventTarget,
//     its function an ffi callback;
//  2. registers ⌃⌥⇧⌘F19 with RegisterEventHotKey (a chord nobody uses) and
//     registers it a second time, to see what a taken chord returns;
//  3. builds a kEventHotKeyPressed event carrying the hotkey's
//     EventHotKeyID (CreateEvent, SetEventParameter) and sends it n times
//     with SendEventToEventTarget, counting handler calls, checking the id
//     the handler reads back with GetEventParameter, and counting Go
//     allocations over the sends (runtime.ReadMemStats);
//  4. unregisters and removes the handler.
//
// It needs no Screen Recording, Accessibility or Input Monitoring grant: a
// synthetic Carbon event never leaves the process. Exit status 0 when the
// probe answered, 1 when it could not run, 2 off macOS.
package main
