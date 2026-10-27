// Command probe-p3 is phase 8's probe P3 (docs/PHASE8-PLAN.md, D65): is the
// WebView2 Evergreen runtime installed on the runner, and does its internal
// export CreateWebViewEnvironmentWithOptionsInternal, called from Go without
// WebView2Loader.dll, create an environment, a controller in our own window
// and navigate to a loopback page, on amd64 and arm64?
//
// It decides D65: option 1 (our loader) holds if "worked" is true on both
// Windows runners; otherwise option 2 (shipping WebView2Loader.dll, which
// needs approval under D2) is compared with -loader.
//
// Run it on a CI runner (windows-2025 and windows-11-arm):
//
//	go run ./cmd/probe-p3                                  # our loader
//	go run ./cmd/probe-p3 -loader C:\path\WebView2Loader.dll  # also the supported loader
//	go run ./cmd/probe-p3 -timeout 90s
//
// What it does, on the main thread locked as an OLE single-threaded
// apartment (OleInitialize) with its own message loop:
//  1. reads the runtime's folder (EBWebView) and version (pv) from
//     HKLM\SOFTWARE\WOW6432Node\Microsoft\EdgeUpdate, HKLM\SOFTWARE\Microsoft\EdgeUpdate
//     and HKCU\SOFTWARE\Microsoft\EdgeUpdate (ClientState and Clients of
//     {F3017226-FE2A-4295-8BDF-00C3A9A7E4C5});
//  2. loads <version folder>\EBWebView\<x64|arm64>\EmbeddedBrowserWebView.dll
//     (EBWebView, the registry value, is the version folder itself);
//  3. calls CreateWebViewEnvironmentWithOptionsInternal(TRUE, 0, userDataFolder,
//     options, handler) with an ICoreWebView2EnvironmentOptions and a
//     completion handler implemented in Go (internal/com's object table),
//     and, only if that does not work, again with options NULL;
//  4. creates a controller in a window opened hidden and then shown, sets
//     its bounds, navigates to a loopback http page served by the probe,
//     waits for NavigationCompleted and reads the document title.
//
// Output: one JSON object on stdout:
//
//	arch          "amd64" or "arm64"
//	runtime       {found, folder, version, dll, candidates: [{key, ebwebview, version, err}]}
//	runtime.candidates also carry folder_version, dll, dll_found and
//	              ebwebview_arches (the architectures the runtime ships)
//	options       "go_object": the first attempt's options argument
//	steps         [{name, hr ("0x…"), ms, ok, detail}] in order
//	env_version   the environment's browser version string
//	nav_success   NavigationCompleted's IsSuccess; web_error_status its status
//	title         the document title read back ("gnui-p3" expected)
//	worked        true when the title came back
//	null_options  the same for options NULL (absent when the first worked)
//	option_calls  which of our options object's methods the runtime called
//	loader        the same steps for -loader (absent without the flag)
//	timed_out     true when -timeout fired (exit status 3)
//	total_ms
//
// Exit status: 0 answered (worked or not), 1 could not run (no window, no
// OLE), 3 timed out (the JSON so far is printed).
//
// Vtable slots and IIDs were checked against the C vtables of WebView2.h in
// the Microsoft.Web.WebView2 NuGet package 1.0.4314.35 (2026-10-27); the
// interfaces used are the original ones, whose order never changes. They are
// cited in main_windows.go.
package main
