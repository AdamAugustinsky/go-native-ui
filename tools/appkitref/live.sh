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
# ours-<page>-<mode>.png and appkit-<page>-<mode>.png with GnuiShot's JSON.
# Then, on the build machine:
#
#	go run ./tools/appkitref/measure -cut OUT/appkit-0-light.png -page 0 -o CELLS/appkit/light
#	go run ./tools/appkitref/measure -live -ref CELLS/appkit -ours CELLS/ours
#	sh tools/appkitref/sheets.sh CELLS/appkit CELLS/ours SHEETS
set -u
here=$(cd "$(dirname "$0")" && pwd)
SHOT=${SHOT:-$HOME/gnui-test/shot.sh}
out=${1:?usage: live.sh OUT_DIR [page]}
page=${2:-0}
scene=appkit
[ "$page" = 1 ] && scene=appkit-type
[ "$page" = 2 ] && scene=appkit-forms
mkdir -p "$out"
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
