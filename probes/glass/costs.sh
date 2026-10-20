#!/bin/sh
# The cost matrix of docs/research/glass-over-metal.md: each configuration
# scrolls at 120 Hz for 6 s (active window, no captures meanwhile), in
# ROUNDS interleaved rounds so that background load spreads over all of
# them; then idle runs. Writes OUT/<config>-r<round>-active-anim.json and
# OUT/<config>-r<round>-active-idle.json.
#
#	sh probes/glass/costs.sh OUT [ROUNDS]
set -u
here=$(cd "$(dirname "$0")" && pwd)
out=${1:?usage: costs.sh OUT [ROUNDS]}
rounds=${2:-3}
APPEARANCE=${APPEARANCE:-light}

configs="
base|-toolbar none -overlay none
tbpocket|-toolbar empty -overlay none
items|-toolbar items -overlay none
vev|-toolbar none -overlay vev
glass|-toolbar none -overlay glass
drawn|-toolbar none -overlay none -drawn 1 -split 1
drawnk|-toolbar none -overlay none -drawn 1 -split 1 -blur kawase
fgasync|-toolbar none -overlay none -fg 1 -sync async
fgtx|-toolbar none -overlay none -fg 1 -sync tx
fgonecb|-toolbar none -overlay none -fg 1 -sync onecb
"
r=1
while [ "$r" -le "$rounds" ]; do
	echo "$configs" | while IFS='|' read -r name args; do
		[ -n "$name" ] || continue
		case " ${CONFIGS:-$name} " in *" $name "*) ;; *) continue ;; esac
		# shellcheck disable=SC2086
		NOCAP=1 sh "$here/run.sh" "$out" "$name-r$r" active-anim,active-idle -mode metal $args -anim 6 -idle 6 -appearance "$APPEARANCE" >/dev/null 2>&1
		echo "$name round $r"
	done
	r=$((r + 1))
done
