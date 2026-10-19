#!/bin/sh
# Show each scene on the test Mac and capture it with GnuiShot (run it there).
#
#	sh run.sh OUT_DIR [scene ...]          # default: finder notes settings chat
#
# Per scene and appearance it launches AppKitScenes in the background (open -g,
# so the user's app keeps focus), and for each phase waits for the tool's
# .ready file, captures the window at 2x with GnuiShot, copies the geometry
# dump, and writes .done. The tool activates itself only for the two active
# phases and quits after the last one. APP and SHOT override the paths.
#
# TAG=name EXTRA="-Key value" runs a variant: EXTRA goes to the tool's argument
# domain (this process only, no setting changes), TAG names the files
# (<scene>_<tag>-...). PHASES="inactive-scrolled inactive-top" picks and orders
# the phases (default: inactive-top inactive-scrolled active-top active-scrolled).
set -u
here=$(cd "$(dirname "$0")" && pwd)
APP=${APP:-$here/AppKitScenes.app}
SHOT=${SHOT:-$HOME/gnui-test/shot.sh}
out=${1:?usage: run.sh OUT_DIR [scene ...]}
shift
scenes=${*:-finder notes settings chat}
mkdir -p "$out"

waitfile() { # waitfile PATH TENTHS -> 0 when it exists
	n=0
	while [ ! -e "$1" ]; do
		[ "$n" -ge "$2" ] && return 1
		if [ -e "$hs/need-activate" ]; then
			rm -f "$hs/need-activate"
			open -b dev.gonativeui.appkitscenes
		fi
		sleep 0.1
		n=$((n + 1))
	done
	return 0
}

phases=${PHASES:-inactive-top inactive-scrolled active-top active-scrolled}
for s in $scenes; do
	for ap in light dark; do
		hs=$(mktemp -d /tmp/appkitscenes.XXXXXX)
		# shellcheck disable=SC2086
		open -g -n "$APP" --args -scene "$s" -appearance "$ap" -hs "$hs" -AppleAccentColor 4 ${TAG:+-tag "$TAG"} -phases "$(echo $phases | tr ' ' ,)" ${EXTRA:-}
		for ph in $phases; do
			base=$s${TAG:+_$TAG}-$ap-$ph
			if ! waitfile "$hs/$base.ready" 300; then
				echo "$base: no ready file" >&2
				break
			fi
			id=$(sed -n 's/^    "windowNumber" : \([0-9][0-9]*\).*/\1/p' "$hs/$base.json" | head -n 1)
			sh "$SHOT" window -id "$id" -scale 2 -no-prompt -out "$out/$base.png" >"$out/$base.shot.json" || echo "$base: capture failed" >&2
			cp "$hs/$base.json" "$out/$base.json"
			: >"$hs/$base.done"
			echo "$base ok"
		done
		# wait for the tool to quit before the next one
		n=0
		while pgrep -f "AppKitScenes.app/Contents/MacOS/AppKitScenes -scene" >/dev/null 2>&1 && [ "$n" -lt 100 ]; do
			sleep 0.1
			n=$((n + 1))
		done
		rm -rf "$hs"
	done
done
