#!/bin/sh
# Capture the gallery's appkit scene and AppKitRef -live side by side in a
# live window, light and dark, with GnuiShot (run it on the test Mac).
#
#	sh live.sh OUT_DIR [page]
#
# Next to this script: RexuiAppKit.app (cmd/gallery, darwin/arm64, in a bundle
# so GnuiShot can find its window), AppKitRef.app (tools/appkitref/build.sh)
# and names<page>.txt (go run ./tools/appkitref/measure -names <page>). Each
# app shows its window for a few seconds, active, and quits or is stopped; no
# setting is changed (the mode is each app's own -mode). It writes
# ours-<page>-<mode>.png and appkit-<page>-<mode>.png with GnuiShot's JSON,
# and machine.json: where it ran (model, macOS build, display, appearance,
# accent, scroller style). The AppKit it captures is this host's own, so the
# comparison is like for like; the committed numbers a live check reads (the
# colours "ours -ref auto" pins, the recipes, the scroller) are this host's set
# too: GNUI_APPKIT_REF=macbook|macmini overrides (internal/appkitref).
#
# The screen must be unlocked (unlocked.sh next to this file, if present) and
# is kept awake with caffeinate -d for the run.
# Then, on the build machine:
#
#	go run ./tools/appkitref/measure -cut OUT/appkit-0-light.png -page 0 -o CELLS/appkit/light
#	go run ./tools/appkitref/measure -live -ref CELLS/appkit -ours CELLS/ours
#	sh tools/appkitref/sheets.sh CELLS/appkit CELLS/ours SHEETS
set -u
here=$(cd "$(dirname "$0")" && pwd)
if [ -f "$here/unlocked.sh" ] && ! sh "$here/unlocked.sh"; then
	echo "live.sh: the screen is locked; nothing was captured" >&2
	exit 3
fi
# keep the display awake for the whole run
[ -n "${GNUI_LIVE_AWAKE:-}" ] || { GNUI_LIVE_AWAKE=1 exec caffeinate -d sh "$0" "$@"; }
SHOT=${SHOT:-$HOME/gnui-test/shot.sh}
out=${1:?usage: live.sh OUT_DIR [page]}
page=${2:-0}
scene=appkit
[ "$page" = 1 ] && scene=appkit-type
[ "$page" = 2 ] && scene=appkit-lists
if ! sh "$SHOT" permission >/dev/null; then
	echo "live.sh: GnuiShot has no Screen Recording grant; nothing was captured" >&2
	exit 2
fi
mkdir -p "$out"
open -W -n "$here/AppKitRef.app" --args -machine "$out/machine.json"
for mode in light dark; do
	open -n "$here/RexuiAppKit.app" --args -scene $scene -mode $mode
	sleep 3
	sh "$SHOT" window -app RexuiAppKit -scale 2 -no-prompt -out "$out/ours-$page-$mode.png" >"$out/ours-$page-$mode.json" ||
		echo "ours $mode: capture failed" >&2
	pkill -f RexuiAppKit.app/Contents/MacOS/RexuiAppKit
	# AppKitRef takes about two seconds to build its window
	open -n "$here/AppKitRef.app" --args -live "$here/names$page.txt" -mode $mode -seconds 10
	sleep 5
	sh "$SHOT" window -app AppKitRef -scale 2 -no-prompt -out "$out/appkit-$page-$mode.png" >"$out/appkit-$page-$mode.json" ||
		echo "appkit $mode: capture failed" >&2
	pkill -f AppKitRef.app/Contents/MacOS/AppKitRef
	sleep 1
done
echo done
