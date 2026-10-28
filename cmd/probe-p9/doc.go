// Command probe-p9 is phase 8's probe P9 (docs/PHASE8-PLAN.md, "How
// Windows is verified"): on macOS 15, does SCRecordingOutput, driven
// through internal/objc (no cgo, D36's blocks and class registry), record
// one of our own windows through an SCContentFilter to an H.264 MP4 with
// the system's audio and the app's own audio excluded? It decides M1's
// design (D72: the system writes the file, no Go per frame).
//
// It must run as a signed bundle, because Screen Recording is granted to a
// bundle identity: gnui run builds .gnui/dev/GNUI Probe P9.app with the
// bundle id dev.gonativeui.probe.p9.dev, signed with the gnui-dev identity,
// so one grant survives rebuilds.
//
//	cd cmd/probe-p9
//	go run ../gnui run -- -preflight   # is the grant there? never prompts
//	go run ../gnui run -- -request     # once: ask for the grant (System Settings opens)
//	go run ../gnui run                 # the probe: record 5 s of its own window
//	go run ../gnui run -- -seconds 10 -mic
//	go run . -dry                      # smoke test of the window, classes and blocks: no grant, no ScreenCaptureKit
//
// Without the grant the probe (and -preflight) reports "granted": false and
// touches no ScreenCaptureKit API, so it never triggers the system's prompt;
// only -request does. Grant it in System Settings → Privacy & Security →
// Screen & System Audio Recording, for "GNUI Probe P9".
//
// What it does: opens a window with an animated spinner, finds that window
// in SCShareableContent by its window number, makes an SCContentFilter for
// it alone (initWithDesktopIndependentWindow:), an SCStreamConfiguration
// (its pixel size, capturesAudio, excludesCurrentProcessAudio, the
// microphone with -mic), an SCStream, an SCRecordingOutput (MP4, H.264)
// with a delegate class of ours, starts the capture, stops it after
// -seconds, and when the delegate reports the recording finished, opens the
// file with AVURLAsset to read its duration and count its video and audio
// tracks.
//
// Output: one JSON object, on stdout (the gnui run log) and in -o (default
// $TMPDIR/gnui-probe-p9.json). Exit status 0 when the probe answered, 1 when
// it could not run.
package main
