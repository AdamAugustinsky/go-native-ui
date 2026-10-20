#!/bin/sh
# Run GlassProbe once and capture its phases with GnuiShot (on the test Mac).
#
#	sh probes/glass/run.sh OUT TAG PHASES [GlassProbe args...]
#
# PHASES is GlassProbe's -phases (comma separated). The probe starts in the
# background (open -g), so the user's app keeps focus; it activates itself
# only for active-* phases, under the activation lock that live tests share
# (internal/testlock). For each static phase run.sh waits for OUT/TAG-PHASE.ready,
# captures the window by id at 2x without its shadow, and writes .done. For
# anim and flash phases it captures a sequence while the probe animates.
# idle, hit and dump need no capture. The screen must be unlocked; the run
# keeps the display awake.
set -u
here=$(cd "$(dirname "$0")" && pwd)
APP=${APP:-$here/bin/GlassProbe.app}
SHOT=${SHOT:-$HOME/gnui-test/shot.sh}
out=${1:?usage: run.sh OUT TAG PHASES [args]}
tag=${2:?tag}
phases=${3:?phases}
shift 3
[ -n "${GLASS_AWAKE:-}" ] || { GLASS_AWAKE=1 exec caffeinate -d sh "$0" "$out" "$tag" "$phases" "$@"; }
if ioreg -n Root -d1 | grep -q '"IOConsoleLocked" = Yes'; then
	echo "run.sh: the screen is locked; nothing was run" >&2
	exit 3
fi
mkdir -p "$out"
rm -f "$out/$tag"-*.ready "$out/$tag"-*.done
open -g -n "$APP" --args -out "$out" -tag "$tag" -phases "$phases" "$@"

waitfile() { # waitfile PATH TENTHS
	n=0
	while [ ! -e "$1" ]; do
		[ "$n" -ge "$2" ] && return 1
		if [ -e "$out/$tag.need-activate" ]; then
			rm -f "$out/$tag.need-activate"
			open -b dev.gonativeui.glassprobe
		fi
		sleep 0.1
		n=$((n + 1))
	done
}

for ph in $(echo "$phases" | tr ',' ' '); do
	case $ph in
	*idle* | *hit* | *dump*) continue ;;
	esac
	if ! waitfile "$out/$tag-$ph.ready" 2400; then
		echo "$tag-$ph: no ready file" >&2
		break
	fi
	id=$(sed -n 's/.*"windowNumber":\([0-9][0-9]*\).*/\1/p' "$out/$tag-$ph.ready")
	case $ph in
	*anim*)
		# NOCAP=1: no capture while the probe measures (captures load the window server)
		if [ -z "${NOCAP:-}" ]; then
			sh "$SHOT" window -id "$id" -scale 2 -no-prompt -repeat "${SEQ:-24}" -interval-ms "${SEQ_MS:-97}" -out "$out/$tag-$ph/seq.png" >"$out/$tag-$ph.shot.json" ||
				echo "$tag-$ph: capture failed" >&2
		fi
		waitfile "$out/$tag-$ph.json" 600 || echo "$tag-$ph: no result" >&2
		;;
	*flash*)
		sh "$SHOT" window -id "$id" -scale 2 -no-prompt -repeat "${SEQ:-24}" -interval-ms "${SEQ_MS:-97}" -out "$out/$tag-$ph/seq.png" >"$out/$tag-$ph.shot.json" ||
			echo "$tag-$ph: capture failed" >&2
		# the probe writes its numbers when the animation ends
		waitfile "$out/$tag-$ph.json" 600 || echo "$tag-$ph: no result" >&2
		;;
	*fullscreen)
		sh "$SHOT" window -id "$id" -scale 2 -no-prompt -poll-ms 4000 -include-app -out "$out/$tag-$ph.png" >"$out/$tag-$ph.shot.json" ||
			echo "$tag-$ph: capture failed" >&2
		;;
	*)
		sh "$SHOT" window -id "$id" -scale 2 -no-prompt -poll-ms 4000 -out "$out/$tag-$ph.png" >"$out/$tag-$ph.shot.json" ||
			echo "$tag-$ph: capture failed" >&2
		;;
	esac
	: >"$out/$tag-$ph.done"
	echo "$tag-$ph ok"
done
n=0
while pgrep -f "GlassProbe.app/Contents/MacOS/GlassProbe -out $out -tag $tag " >/dev/null 2>&1 && [ "$n" -lt 600 ]; do
	sleep 0.1
	n=$((n + 1))
done
pkill -f "GlassProbe.app/Contents/MacOS/GlassProbe -out $out -tag $tag " 2>/dev/null
exit 0
