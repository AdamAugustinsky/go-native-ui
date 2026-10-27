# go-native-ui (placeholder name)

A native desktop UI stack in Go for macOS, Windows and Linux, built to File
Pilot's engineering bar: instant startup, small memory, zero idle CPU and
responsive input. A dual-pane file browser will prove it.

- [ARCHITECTURE.md](ARCHITECTURE.md): the design and the evidence behind it
- [DECISIONS.md](DECISIONS.md): decisions, dependencies, the cgo policy and open questions
- [docs/GUIDE.md](docs/GUIDE.md): the developer guide: build a small app step by step ([examples/notes](examples/notes))
- [docs/guide/rex.md](docs/guide/rex.md): Rex, the default look, and the rules a new app follows in it
- [docs/measurements/](docs/measurements/): raw numbers per phase and machine
- `probes/`: throwaway phase-0 experiments (each file header has its command)

Toolchain: Go 1.27 with `CGO_ENABLED=0` on every target. Targets: macOS on
Apple silicon (darwin/arm64 only; Intel Macs are not supported, D55), Windows
and Linux on amd64 and arm64.

## Phase 1 spike

```sh
go build -trimpath -ldflags="-s -w" -o bin/spike ./cmd/spike
./bin/spike                    # interactive: space animates, Esc quits, events are logged
./bin/spike -bench -gate       # scripted run: JSON report; exit 3 if a gate fails
```

`GNUI_BACKEND=wayland|x11` forces a Linux backend. The go/no-go report is
[docs/measurements/phase1-macos.md](docs/measurements/phase1-macos.md).
