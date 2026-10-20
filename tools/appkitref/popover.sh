#!/bin/sh
# Build AppKitPopover (popover.m) and capture a real NSPopover, light and dark,
# with GnuiShot (run it on the test Mac, with the console unlocked: it opens a
# window for about ten seconds and quits it).
#
#	sh popover.sh build
#	sh popover.sh behavior OUT_DIR
#	sh popover.sh capture OUT_DIR [below|above|right|left] [WxH]
#
# It writes OUT/popover-<edge>-<mode>.png (the window's region of the main
# display at 2×) and popover-<edge>-<mode>.json (the popover's metrics from
# AppKit, in screen points from the display's top left, and the capture's
# rectangle). tools/appkitref/popover_measure.py reads them.
set -eu
here=$(cd "$(dirname "$0")" && pwd)
app=$here/bin/AppKitPopover.app
SHOT=${SHOT:-$HOME/gnui-test/shot.sh}
case ${1:-} in
build)
	sdk=${SDKROOT:-/Library/Developer/CommandLineTools/SDKs/MacOSX.sdk}
	[ -d "$sdk" ] || sdk=$(xcrun --show-sdk-path)
	rm -rf "$app"
	mkdir -p "$app/Contents/MacOS"
	clang -O2 -Wall -Wextra -Wno-unused-parameter -fobjc-arc -mmacosx-version-min=14.0 -isysroot "$sdk" \
		-framework AppKit -o "$app/Contents/MacOS/AppKitPopover" "$here/popover.m"
	sed -e 's/AppKitRef/AppKitPopover/g' -e 's/dev.gonativeui.appkitref/dev.gonativeui.appkitpopover/' -e 's/ARApp/NSApplication/' "$here/Info.plist" >"$app/Contents/Info.plist"
	codesign -s "${SIGN_IDENTITY:--}" -f --identifier dev.gonativeui.appkitpopover "$app"
	echo "built $app"
	;;
behavior)
	# what closes a real NSPopover of each behavior (popover_behavior.m): one JSON line per
	# behavior and scenario, in OUT_DIR/behavior.jsonl
	out=${2:?usage: popover.sh behavior OUT_DIR}
	bapp=$here/bin/AppKitPopoverBehavior.app
	sdk=${SDKROOT:-/Library/Developer/CommandLineTools/SDKs/MacOSX.sdk}
	[ -d "$sdk" ] || sdk=$(xcrun --show-sdk-path)
	rm -rf "$bapp"
	mkdir -p "$bapp/Contents/MacOS" "$out"
	clang -O2 -Wall -Wextra -Wno-unused-parameter -Wno-deprecated-declarations -fobjc-arc -mmacosx-version-min=14.0 -isysroot "$sdk" \
		-framework AppKit -o "$bapp/Contents/MacOS/AppKitPopoverBehavior" "$here/popover_behavior.m"
	sed -e 's/AppKitRef/AppKitPopoverBehavior/g' -e 's/dev.gonativeui.appkitref/dev.gonativeui.appkitpopoverbehavior/' -e 's/ARApp/NSApplication/' "$here/Info.plist" >"$bapp/Contents/Info.plist"
	codesign -s "${SIGN_IDENTITY:--}" -f --identifier dev.gonativeui.appkitpopoverbehavior "$bapp"
	if ioreg -n Root -d1 | grep -q '"IOConsoleLocked" = Yes'; then
		echo "console locked: not running" >&2
		exit 3
	fi
	: >"$out/behavior.jsonl"
	for b in transient semi app; do
		for s in other empty titlebar anchor inside away escape return deactivate; do
			rm -f "$out/one.out"
			# run the binary itself (open(1) can leave the app inactive, with no key window)
			"$bapp/Contents/MacOS/AppKitPopoverBehavior" -behavior $b -scenario $s >"$out/one.out" 2>/dev/null &
			pid=$!
			sleep 4
			kill $pid 2>/dev/null || true
			wait $pid 2>/dev/null || true
			cat "$out/one.out" >>"$out/behavior.jsonl" 2>/dev/null || true
		done
	done
	rm -f "$out/one.out"
	cat "$out/behavior.jsonl"
	;;
capture)
	out=${2:?usage: popover.sh capture OUT_DIR [edge] [WxH]}
	edge=${3:-below}
	size=${4:-260x140}
	mkdir -p "$out"
	# a locked console shows no windows: stop rather than capture the lock screen
	if ioreg -n Root -d1 | grep -q '"IOConsoleLocked" = Yes'; then
		echo "console locked: not capturing" >&2
		exit 3
	fi
	for mode in light dark; do
		rm -f "$out/popover-$edge-$mode.out"
		open -n "$app" --stdout "$out/popover-$edge-$mode.out" --args -$mode -edge "$edge" -size "$size" -seconds 9
		sleep 3
		sh "$SHOT" display -rect 0,0,640,520 -scale 2 -no-prompt -out "$out/popover-$edge-$mode.png" >"$out/popover-$edge-$mode.shot.json" ||
			echo "capture $mode failed" >&2
		sleep 1
		pkill -f AppKitPopover.app/Contents/MacOS/AppKitPopover || true
		sleep 1
		cp "$out/popover-$edge-$mode.out" "$out/popover-$edge-$mode.json" 2>/dev/null || true
	done
	echo done
	;;
*)
	echo "usage: popover.sh build | behavior OUT_DIR | capture OUT_DIR [edge] [WxH]" >&2
	exit 64
	;;
esac
