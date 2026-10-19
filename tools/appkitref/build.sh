#!/bin/sh
# Build tools/appkitref/bin/AppKitRef.app (ad-hoc signed, stable identifier).
#
#	bash tools/appkitref/build.sh
set -eu
here=$(cd "$(dirname "$0")" && pwd)
out=$here/bin/AppKitRef.app
sdk=${SDKROOT:-/Library/Developer/CommandLineTools/SDKs/MacOSX.sdk}
[ -d "$sdk" ] || sdk=$(xcrun --show-sdk-path)

rm -rf "$out"
mkdir -p "$out/Contents/MacOS"
clang -O2 -Wall -Wextra -Wno-unused-parameter -fobjc-arc -mmacosx-version-min=14.0 \
	-isysroot "$sdk" \
	-framework AppKit -framework CoreText \
	-o "$out/Contents/MacOS/AppKitRef" "$here/main.m"
cp "$here/Info.plist" "$out/Contents/Info.plist"
codesign -s "${SIGN_IDENTITY:--}" -f --identifier dev.gonativeui.appkitref "$out"
echo "built $out"

# AppKitShell (shell.m): a real window, the AppKit counterpart of rexlook's
# native shell, for GnuiShot captures.
shell=$here/bin/AppKitShell.app
rm -rf "$shell"
mkdir -p "$shell/Contents/MacOS"
clang -O2 -Wall -Wextra -Wno-unused-parameter -fobjc-arc -mmacosx-version-min=14.0 \
	-isysroot "$sdk" \
	-framework AppKit -framework QuartzCore \
	-o "$shell/Contents/MacOS/AppKitShell" "$here/shell.m"
sed -e 's/AppKitRef/AppKitShell/g' -e 's/dev.gonativeui.appkitref/dev.gonativeui.appkitshell/' \
	-e '/LSUIElement/,/<true\/>/d' -e '/Never active/d' -e '/LSBackgroundOnly/,/<true\/>/d' \
	-e 's/<string>ARApp<\/string>/<string>NSApplication<\/string>/' "$here/Info.plist" >"$shell/Contents/Info.plist"
codesign -s "${SIGN_IDENTITY:--}" -f --identifier dev.gonativeui.appkitshell "$shell"
echo "built $shell"
