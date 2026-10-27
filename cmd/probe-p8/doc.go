// Command probe-p8 is phase 8's probe P8 (docs/PHASE8-PLAN.md, D73): does
// WinHTTP, called from Go through winhttp.dll alone, do what fetch.Do (N1)
// and app/ws (N2) need on Windows: the system proxy, streamed uploads of a
// known and an unknown length with progress, a response read as it streams,
// cancellation by closing the request handle, and a WebSocket with large
// fragmented messages, pings and close codes?
//
// It decides N1's and N2's Windows design: if every section answers "ok",
// synchronous WinHTTP on goroutines is the plan; a failed section changes
// that item's approach before the track starts.
//
// Run it on a CI runner (windows-2025 and windows-11-arm):
//
//	go run ./cmd/probe-p8
//	go run ./cmd/probe-p8 -upload-mib 64 -ws-big-mib 32 -timeout 120s
//
// The servers are loopback ones inside the probe: net/http for the HTTP
// sections and a minimal RFC 6455 server on net for the WebSocket (probes are
// not size-gated; the client side uses WinHTTP only).
//
// Output: one JSON object on stdout:
//
//	session         {access_type: "automatic" (WINHTTP_ACCESS_TYPE_AUTOMATIC_PROXY,
//	                Windows 8.1+) or "default" (the fallback), open_err}
//	ie_proxy        WinHttpGetIEProxyConfigForCurrentUser: {ok, auto_detect,
//	                auto_config_url, proxy, bypass, err}
//	default_proxy   WinHttpGetDefaultProxyConfiguration: {ok, access_type, proxy, bypass, err}
//	upload_known    {bytes, server_bytes, status, ms, mb_per_s, progress: [{bytes, ms}], err}
//	upload_chunked  the same, with Transfer-Encoding: chunked framed by the probe
//	stream_lines    {reader, lines, reads, first_line_ms, max_gap_ms, total_ms,
//	                server_ms, transfer_encoding, streamed, err}: a chunked
//	                response of 200 lines 5 ms apart, read with
//	                WinHttpQueryDataAvailable then WinHttpReadData of that many
//	                bytes; streamed is true when the first line came within
//	                50 ms and no read waited over 100 ms
//	stream_lines_readdata_only  the same read with a plain 4 KiB
//	                WinHttpReadData loop, which waits for a full buffer
//	                (recorded, not part of worked)
//	cancel          {written_before_cancel, return_ms, errno, err}: how fast a
//	                WinHttpWriteData blocked on a stalled server returns once
//	                another goroutine closes the request handle
//	websocket       {upgrade_ms, status, text_ok, text_rtt_ms, binary_ok,
//	                big_ok, big_bytes, big_fragments, big_ms, big, server_got_pong,
//	                close_status, close_reason, close_err, err}; big lists
//	                the 32 MiB round trips (-ws-big-trips, 10 by default,
//	                alternating one send and 1 MiB fragments) with what the server received and
//	                echoed, the receive pieces and the first differing byte
//	worked          every section ok
//	timed_out       true when -timeout fired (exit status 3)
//	total_ms
//
// Exit status: 0 answered, 1 could not run (no winhttp.dll, no loopback
// listener), 3 timed out (the JSON so far is printed).
//
// Function signatures, constants and struct layouts are from the Windows
// SDK's winhttp.h, cited in winhttp_windows.go.
package main
