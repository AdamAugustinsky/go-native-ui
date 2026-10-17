#!/bin/sh
# Wrap an executable into a minimal, ad-hoc signed .app bundle.
#
#	tools/mkapp.sh <binary> <out.app> <bundle-id> [name]
#
# name defaults to the basename of <binary> and becomes Contents/MacOS/<name>,
# CFBundleExecutable and CFBundleName. The app is meant to be started with
# `open` (LaunchServices), which is what makes the Dock bounce.
set -eu
if [ $# -lt 3 ] || [ $# -gt 4 ]; then
	echo "usage: $0 <binary> <out.app> <bundle-id> [name]" >&2
	exit 64
fi
bin=$1
app=${2%/}
id=$3
name=${4:-$(basename "$bin")}
[ -f "$bin" ] && [ -x "$bin" ] || { echo "mkapp: $bin is not an executable file" >&2; exit 66; }
case $app in *.app) ;; *) echo "mkapp: output must end in .app" >&2; exit 64 ;; esac
case $name in */*|'') echo "mkapp: bad name '$name'" >&2; exit 64 ;; esac

rm -rf "$app"
mkdir -p "$app/Contents/MacOS"
cp "$bin" "$app/Contents/MacOS/$name"
chmod 755 "$app/Contents/MacOS/$name"
cat > "$app/Contents/Info.plist" <<PLIST
<?xml version="1.0" encoding="UTF-8"?>
<!DOCTYPE plist PUBLIC "-//Apple//DTD PLIST 1.0//EN" "http://www.apple.com/DTDs/PropertyList-1.0.dtd">
<plist version="1.0">
<dict>
	<key>CFBundleExecutable</key>
	<string>$name</string>
	<key>CFBundleIdentifier</key>
	<string>$id</string>
	<key>CFBundleName</key>
	<string>$name</string>
	<key>CFBundlePackageType</key>
	<string>APPL</string>
	<key>CFBundleInfoDictionaryVersion</key>
	<string>6.0</string>
	<key>CFBundleVersion</key>
	<string>1</string>
	<key>CFBundleShortVersionString</key>
	<string>1.0</string>
	<key>LSMinimumSystemVersion</key>
	<string>14.0</string>
	<key>NSHighResolutionCapable</key>
	<true/>
	<key>NSPrincipalClass</key>
	<string>NSApplication</string>
</dict>
</plist>
PLIST
codesign -s - -f "$app" >/dev/null 2>&1 || { echo "mkapp: codesign failed" >&2; exit 1; }
echo "$app"
