# go-native-ui (placeholder name)

A native desktop UI stack in Go for macOS, Windows and Linux, built to File
Pilot's engineering bar: instant startup, small memory, zero idle CPU and
responsive input. A dual-pane file browser will prove it.

- [ARCHITECTURE.md](ARCHITECTURE.md): the design and the evidence behind it
- [DECISIONS.md](DECISIONS.md): decisions, dependencies, the cgo policy and open questions
- [docs/measurements/](docs/measurements/): raw numbers per phase and machine
- `probes/`: throwaway phase-0 experiments (each file header has its command)

Toolchain: Go 1.27 with `CGO_ENABLED=0` on every target.
