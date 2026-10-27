package ws

import "context"

// No WebSocket client here yet: N2 brings NSURLSessionWebSocketTask on
// macOS, WinHTTP on Windows and net/http with our framing on Linux. When N2
// lands this file is split per OS.

func osDial(context.Context, string, Options) (backend, string, error) {
	return nil, "", ErrUnsupported
}
