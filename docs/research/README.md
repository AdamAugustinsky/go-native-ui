# Framework research (phase 6 input, 2026-10-18)

Read-only studies of other toolkits and of ZapFast, written to rank what the
framework still lacks for real apps, macOS first. Each report cites paths in
shallow clones under `~/Projects/research/` (not in this repo); no third-party
code was copied.

| Report | Scope |
|---|---|
| [zapfast.md](zapfast.md) | What ZapFast (crmne/zapfast) needs from us; its UI/backend boundary and a C ABI sketch; whatsmeow comparison; port size |
| [egui.md](egui.md) | egui, eframe, AccessKit, rfd, arboard, winit |
| [gpui-ghostty.md](gpui-ghostty.md) | GPUI (Zed) and Ghostty: native macOS integration, hosting native views next to a GPU surface |
| [electron-tauri.md](electron-tauri.md) | App infrastructure: lifecycle, notifications, tray, updates, packaging, signing, notarization |
| [go-and-others.md](go-and-others.md) | Gio, Fyne, Wails, Slint, Iced |
| [displaylink-stall.md](displaylink-stall.md) | A macOS display link that stops calling back on CI: the evidence, two leads, the recovery |

The design measurements are in `docs/design/native-audit.md` and
`docs/design/rex-design-system.md` (AppKit reference: `tools/appkitref`).
