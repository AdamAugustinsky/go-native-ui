#!/usr/bin/env python3
"""Generate a Go overlay, leaving the checked-out control/candidate untouched."""
import json
import pathlib
import sys

root, harness, out = map(lambda s: pathlib.Path(s).resolve(), sys.argv[1:])
out.mkdir(parents=True, exist_ok=True)
source = harness.read_text()
insertions = {
    'fs.t0 = platform.Now()': 'fs.t0 = platform.Now()\n\tdefer fsProbeStart(a)()',
    'func greenFrame(a *App, win objc.ID) {': 'func greenFrame(a *App, win objc.ID) {\n\tfsProbeFrame(a)',
    'func fsSample() {': 'func fsSample() {\n\tfsProbeTurn()',
    'func fullScreenNote(p int) {': 'func fullScreenNote(p int) {\n\tfsLog("probe note %d: %s", p, fsProbeSnapshot(fs.a))',
    'fsLog("+%d frames +%d ticks", a.frames-fs.lastFrame, st.Ticks-fs.lastTicks)': 'fsLog("+%d frames +%d ticks", a.frames-fs.lastFrame, st.Ticks-fs.lastTicks)\n\t\tfsLog("%s", fsProbeSnapshot(a))',
}
for old, new in insertions.items():
    if source.count(old) != 1:
        raise SystemExit(f"instrumentation anchor is not unique: {old}")
    source = source.replace(old, new)
generated = out / 'fullscreen_darwin_test.go'
generated.write_text(source)
overlay = {"Replace": {
    str(root / 'app/fullscreen_darwin_test.go'): str(generated),
    str(root / 'app/fullscreen_probe_darwin_test.go'): str((pathlib.Path(__file__).parent / 'testdata/probe_darwin_test.go').resolve()),
}}
(out / 'overlay.json').write_text(json.dumps(overlay, indent=2) + '\n')
