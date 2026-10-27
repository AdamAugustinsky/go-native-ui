module gonativeui

go 1.27.1

require (
	github.com/alecthomas/chroma/v2 v2.27.0
	github.com/ebitengine/purego v0.12.0-alpha.1.0.20261015130536-d456b1bb95e8
	github.com/go-text/typesetting v0.3.5
)

require (
	github.com/dlclark/regexp2/v2 v2.2.1 // indirect
	golang.org/x/image v0.23.0 // indirect
)

// Patched copy: zero-allocation shaping (DECISIONS.md D23).
replace github.com/go-text/typesetting => ./third_party/typesetting
