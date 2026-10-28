// Command probe-p4 is phase 8's probe P4 (docs/PHASE8-PLAN.md, "How Windows
// is verified"): capture and encoding on a CI runner, before track M2.
//
// Questions:
//   - Does Media Foundation exist on the runner (mfplat.dll, mfreadwrite.dll,
//     MFStartup)? windows-2025 is a Server image, which may lack the Media
//     Foundation feature.
//   - Which H.264 and AAC encoders does MFTEnumEx list, hardware and
//     software?
//   - Does a sink writer (MFCreateSinkWriterFromURL, hardware transforms
//     allowed) write an H.264 + AAC MP4 from NV12 frames (RGB32 if NV12 is
//     refused) and silent PCM, and does the source reader read back its
//     duration and two streams?
//   - Is Windows.Graphics.Capture supported, and does a free-threaded frame
//     pool on our D3D11 device deliver frames of our own window
//     (IGraphicsCaptureItemInterop::CreateForWindow), polled with
//     TryGetNextFrame?
//
// It decides M2's design (D72) and the reference runner: if windows-2025
// has no Media Foundation, windows-11-arm and the VM carry M2's tests.
//
// Run on a runner (or the VM), from the repository root:
//
//	go run ./cmd/probe-p4                      # both parts, 2 s capture, a 3 s file
//	go run ./cmd/probe-p4 -skip-wgc -hw=false  # MF only, software transforms
//	go run ./cmd/probe-p4 -seconds 5 -keep     # longer, and keep the MP4
//
// Flags: -seconds (capture length; the file is at least 3 s), -skip-wgc,
// -skip-mf, -hw (hardware transforms, default true), -keep, -timeout
// (default 60 s; on expiry the report so far is printed with timedOut and
// the exit status is 1).
//
// Output: one JSON object on stdout: probe, arch, windows (major, minor,
// build), mediaFoundation (mfplatLoaded, mfreadwriteLoaded, startupHR,
// available, encoders {h264Hardware, h264Software, aacHardware,
// aacSoftware}, encoderNames, writer {hardwareTransforms, inputFormat,
// steps (HRESULT per step), framesWritten, audioBlocksWritten, writeMs,
// fileBytes, ok}, reader {openHR, durationMs, streams}), wgc (supported,
// steps, d3dDriver, itemSize, frames, seconds, firstFrameMs,
// firstSystemRelativeTime, lastSystemRelativeTime, firstContentSize,
// meanFrameIntervalMs, capturedOwnWindow), elapsedMs. Exit status 0 when
// the probe answered (a "no" included), 1 on a timeout, 2 off Windows.
package main
