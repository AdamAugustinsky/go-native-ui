#!/bin/sh
# Run GnuiShot through LaunchServices and print its JSON result.
#
#	tools/gnuishot/shot.sh list [-app Name]
#	tools/gnuishot/shot.sh window -app MyApp -out /tmp/shot.png
#
# Over SSH on the test Mac this is the only way that works: the app must be
# launched by `open` so macOS attributes Screen Recording to GnuiShot itself.
# `open` swallows stdout, so a temporary -json file carries the result; this
# script prints it on stdout and exits with the command's exit code on an error
# result (2 permission needed, 3 failure, 4 no match, 5 timeout, 64 bad args;
# 1 if the app produced nothing; 66 if the app bundle is missing).
#
# APP defaults to ~/gnui-test/GnuiShot.app. Relative -out/-json paths are made
# absolute (an app started by `open` runs with / as its working directory).
# Do not pass -json unless you want to keep the file; the script supplies one.
set -u
APP=${APP:-$HOME/gnui-test/GnuiShot.app}

if [ $# -eq 0 ]; then
	echo '{"error":"usage: shot.sh <list|window|display|appearance|permission|selftest> [options]","exitCode":64}'
	exit 64
fi
if [ ! -d "$APP" ]; then
	printf '{"error":"GnuiShot.app not found at %s (set APP=...)","exitCode":66}\n' "$APP"
	exit 66
fi

absolute() {
	case $1 in
	/*) printf '%s' "$1" ;;
	"~") printf '%s' "$HOME" ;;
	"~/"*) printf '%s/%s' "$HOME" "${1#"~/"}" ;;
	*) printf '%s/%s' "$PWD" "$1" ;;
	esac
}

# Rebuild the argument list with absolute paths; pick out values that size the deadline.
json=""
wait_ms=0 poll_ms=0 repeat=1 interval_ms=0 timeout_s=15
n=$#
i=0
while [ "$i" -lt "$n" ]; do
	a=$1
	shift
	i=$((i + 1))
	case $a in
	-out | -json | -wait-ms | -poll-ms | -repeat | -interval-ms | -timeout)
		if [ "$i" -lt "$n" ]; then
			v=$1
			shift
			i=$((i + 1))
			case $a in
			-out) v=$(absolute "$v") ;;
			-json) v=$(absolute "$v") json=$v ;;
			-wait-ms) wait_ms=$v ;;
			-poll-ms) poll_ms=$v ;;
			-repeat) repeat=$v ;;
			-interval-ms) interval_ms=$v ;;
			-timeout) timeout_s=$v ;;
			esac
			set -- "$@" "$a" "$v"
		else
			set -- "$@" "$a"
		fi
		;;
	*) set -- "$@" "$a" ;;
	esac
done

tmpdir=$(mktemp -d "${TMPDIR:-/tmp}/gnuishot.XXXXXX") || exit 1
trap 'rm -rf "$tmpdir"' EXIT INT TERM
[ -n "$json" ] || json=$tmpdir/result.json
rm -f "$json"

# The app has its own watchdog (15 s + wait + repeat); this is a backstop so the wrapper never hangs either.
deadline=$(awk -v w="$wait_ms" -v p="$poll_ms" -v r="$repeat" -v i="$interval_ms" -v t="$timeout_s" 'BEGIN { d = t + 20 + w / 1000 + p / 1000 + (r > 0 ? r * (i / 1000 + 0.5) : 0); if (d != d + 0) d = 60; printf "%d", d + 1 }' 2>/dev/null)
case $deadline in '' | *[!0-9]*) deadline=60 ;; esac

open -g -W -n "$APP" --args "$@" -json "$json" 2>"$tmpdir/open.err" &
open_pid=$!
waited=0
while kill -0 "$open_pid" 2>/dev/null; do
	if [ "$waited" -ge $((deadline * 10)) ]; then
		kill "$open_pid" 2>/dev/null
		break
	fi
	sleep 0.1
	waited=$((waited + 1))
done
wait "$open_pid" 2>/dev/null

# `open -W` can return before the app is observable (very fast exits); give the result file a moment.
tries=0
while [ ! -s "$json" ] && [ "$tries" -lt 50 ]; do
	sleep 0.1
	tries=$((tries + 1))
done
if [ ! -s "$json" ]; then
	detail=$(grep -v 'Unable to block on application' "$tmpdir/open.err" 2>/dev/null | tr -d '"\\' | tr '\n' ' ')
	printf '{"error":"GnuiShot wrote no result (crashed, blocked by the system, or timed out in the wrapper)","detail":"%s","exitCode":1}\n' "$detail"
	exit 1
fi

cat "$json"
# Pretty-printed with sorted keys: a top-level key sits at exactly two spaces of indent.
if grep -q '^  "error" :' "$json"; then
	code=$(sed -n 's/^  "exitCode" : \([0-9][0-9]*\).*/\1/p' "$json" | head -n 1)
	case $code in '' | 0) code=1 ;; esac
	exit "$code"
fi
exit 0
