#!/bin/sh
# Build AppKitKeynav (keynav.m) and print which controls Tab reaches with the
# machine's Keyboard navigation setting (never changed by the tool).
#
#	sh tools/appkitref/keynav.sh [-machine]
set -eu
here=$(cd "$(dirname "$0")" && pwd)
sdk=${SDKROOT:-/Library/Developer/CommandLineTools/SDKs/MacOSX.sdk}
[ -d "$sdk" ] || sdk=$(xcrun --show-sdk-path)
mkdir -p "$here/bin"
clang -O2 -Wall -Wextra -Wno-unused-parameter -fobjc-arc -mmacosx-version-min=14.0 -isysroot "$sdk" \
	-framework AppKit -o "$here/bin/AppKitKeynav" "$here/keynav.m"
exec "$here/bin/AppKitKeynav" "$@"
