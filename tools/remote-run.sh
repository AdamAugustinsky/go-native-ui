#!/bin/sh
# Build cmd/spike for darwin/arm64 on this machine and run it on a test Mac
# over SSH. The window opens in the remote user's logged-in GUI session.
#
#	GNUI_TEST_HOST=user@host tools/remote-run.sh -bench -idle 60s -gate -quiet
set -eu
host=${GNUI_TEST_HOST:?set GNUI_TEST_HOST=user@host}
cd "$(dirname "$0")/.."
CGO_ENABLED=0 GOOS=darwin GOARCH=arm64 go build -trimpath -ldflags="-s -w" -o bin/spike-darwin-arm64 ./cmd/spike
ssh -o BatchMode=yes "$host" 'mkdir -p ~/gnui-test'
scp -q bin/spike-darwin-arm64 "$host:gnui-test/spike"
exec ssh -o BatchMode=yes "$host" "cd ~/gnui-test && ./spike $*"
