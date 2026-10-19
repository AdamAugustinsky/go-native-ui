#!/bin/sh
# Rebuild the native tool apps (GnuiShot, BounceRec, AppKitRef) and sign them
# with the stable gnui-dev identity, so a privacy grant given to one of them
# (Screen Recording) survives rebuilds.
#
#	tools/resign-tools.sh [gnuishot|bouncerec|appkitref ...]    (default: all three)
#
# Output: tools/<tool>/bin/<App>.app, as with each tool's build.sh. Nothing is
# installed or copied anywhere: put a bundle on the test Mac yourself (see the
# tool's README), once, and grant it once. Run `gnui doctor -setup-dev-cert`
# first if the identity does not exist yet.
set -eu
root=$(cd "$(dirname "$0")/.." && pwd)
cd "$root"
export CGO_ENABLED=0
[ $# -gt 0 ] || set -- gnuishot bouncerec appkitref
gnui=$(mktemp -d)/gnui
go build -o "$gnui" ./cmd/gnui
for t in "$@"; do
	case $t in
		gnuishot)  app=tools/gnuishot/bin/GnuiShot.app ;;
		bouncerec) app=tools/bouncerec/bin/BounceRec.app ;;
		appkitref) app=tools/appkitref/bin/AppKitRef.app ;;
		*) echo "resign-tools: unknown tool '$t'" >&2; exit 64 ;;
	esac
	bash "tools/$t/build.sh" >/dev/null
	# the build scripts sign ad-hoc; sign again, keeping each bundle's identifier
	"$gnui" sign "$app"
done
