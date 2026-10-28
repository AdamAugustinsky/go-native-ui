module gonativeui

go 1.27.1

require (
	github.com/alecthomas/chroma/v2 v2.27.0
	github.com/ebitengine/purego v0.12.0-alpha.1.0.20261015130536-d456b1bb95e8
	github.com/go-text/typesetting v0.3.5
	github.com/srwiley/oksvg v0.0.0-20221011165216-be6e8873101c
	github.com/srwiley/rasterx v0.0.0-20220730225603-2ab79fcdd4ef
	github.com/yuin/goldmark v1.8.6
	golang.org/x/image v0.23.0
)

require (
	github.com/dlclark/regexp2/v2 v2.2.1 // indirect
	golang.org/x/net v0.62.0 // indirect
	golang.org/x/text v0.43.0 // indirect
)

// Patched copy: zero-allocation shaping (DECISIONS.md D23).
replace github.com/go-text/typesetting => ./third_party/typesetting
