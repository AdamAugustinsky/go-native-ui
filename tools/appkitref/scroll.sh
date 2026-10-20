#!/bin/sh
# Build AppKitScroll (scroll.m) and record NSScrollView's offsets for every
# scenario in ui/testdata/appkit-scroll: NAME.txt -> NAME.appkit, which
# ui/scrollfit_test.go compares ui's physics with.
#
#	sh tools/appkitref/scroll.sh [NAME...]
#
# It opens a real window per scenario (a GUI session is needed) and keeps the
# display awake while it runs: NSScrollView moves only on display frames.
#
# The files belong to the machine that records them (internal/appkitref): the
# MacBook's are NAME.appkit, the Mac mini's NAME-macmini.appkit, next to the
# same NAME.txt. The set is this host's, or GNUI_APPKIT_REF=macbook|macmini; an
# unknown machine records nothing. A machine.json beside the files says where
# they were recorded.
set -eu
here=$(cd "$(dirname "$0")" && pwd)
data=$here/../../ui/testdata/appkit-scroll
app=$here/bin/AppKitScroll.app
sdk=${SDKROOT:-/Library/Developer/CommandLineTools/SDKs/MacOSX.sdk}
[ -d "$sdk" ] || sdk=$(xcrun --show-sdk-path)

set=$(cd "$here/../.." && CGO_ENABLED=0 go run ./tools/appkitref/measure -set-name) || exit 1
sfx=; [ "$set" = macbook ] || sfx=-$set
rm -rf "$app"
mkdir -p "$app/Contents/MacOS"
clang -O2 -Wall -Wextra -Wno-unused-parameter -fobjc-arc -mmacosx-version-min=14.0 \
	-isysroot "$sdk" -framework AppKit -framework QuartzCore \
	-o "$app/Contents/MacOS/AppKitScroll" "$here/scroll.m"
sed -e 's/AppKitRef/AppKitScroll/g' -e 's/dev.gonativeui.appkitref/dev.gonativeui.appkitscroll/' \
	-e '/LSUIElement/,/<true\/>/d' -e '/Never active/d' -e '/LSBackgroundOnly/,/<true\/>/d' \
	-e 's/<string>ARApp<\/string>/<string>NSApplication<\/string>/' "$here/Info.plist" >"$app/Contents/Info.plist"
codesign -s "${SIGN_IDENTITY:--}" -f --identifier dev.gonativeui.appkitscroll "$app"

if [ $# -eq 0 ]; then
	set -- $(cd "$data" && ls *.txt | sed 's/\.txt$//')
fi
caffeinate -u -t 1
for n in "$@"; do
	caffeinate -d "$app/Contents/MacOS/AppKitScroll" "$data/$n.txt" "$data/$n$sfx.appkit"
	echo "recorded $n$sfx"
done
# the machine, for the files just written (the scroll tool writes plain text)
sh "$here/scroller.sh" -machine >"$data/machine$sfx.json"
