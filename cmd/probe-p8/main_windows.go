//go:build windows

package main

import (
	"bytes"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"net"
	"net/http"
	"os"
	"strconv"
	"sync"
	"sync/atomic"
	"time"
	"unsafe"
)

type sample struct {
	Bytes int64   `json:"bytes"`
	Ms    float64 `json:"ms"`
}

type upload struct {
	Bytes       int64    `json:"bytes"`
	ServerBytes int64    `json:"server_bytes"`
	Status      int      `json:"status"`
	Ms          float64  `json:"ms"`
	MBps        float64  `json:"mb_per_s"`
	Progress    []sample `json:"progress"`
	OK          bool     `json:"ok"`
	Err         string   `json:"err,omitempty"`
}

// recvStats describes one wsReceive: the pieces WinHttpWebSocketReceive
// returned.
type recvStats struct {
	Pieces    int    `json:"pieces"`
	PieceMin  int    `json:"piece_min"`
	PieceMax  int    `json:"piece_max"`
	Types     [5]int `json:"types"` // count per WINHTTP_WEB_SOCKET_BUFFER_TYPE 0-4
	LastPiece int    `json:"last_piece"`
}

type bigTrip struct {
	Mode          string    `json:"mode"`
	Sent          int       `json:"sent"`
	ServerIn      int64     `json:"server_in"`
	ServerFrames  int64     `json:"server_in_frames"`
	ServerOut     int64     `json:"server_out"`
	Received      int       `json:"received"`
	Recv          recvStats `json:"recv"`
	FirstMismatch int       `json:"first_mismatch"` // -1: none
	Ms            float64   `json:"ms"`
	OK            bool      `json:"ok"`
	Err           string    `json:"err,omitempty"`
}

type streamResult struct {
	Reader      string  `json:"reader"`
	Lines       int     `json:"lines"`
	Reads       int     `json:"reads"`
	FirstLineMs float64 `json:"first_line_ms"`
	MaxGapMs    float64 `json:"max_gap_ms"` // the longest wait between two reads
	TotalMs     float64 `json:"total_ms"`
	ServerMs    float64 `json:"server_ms"`
	Encoding    string  `json:"transfer_encoding"`
	Streamed    bool    `json:"streamed"`
	OK          bool    `json:"ok"`
	Err         string  `json:"err,omitempty"`
}

type report struct {
	Session struct {
		AccessType string `json:"access_type"`
		OpenErr    string `json:"open_err,omitempty"`
	} `json:"session"`
	IEProxy struct {
		OK            bool   `json:"ok"`
		AutoDetect    bool   `json:"auto_detect"`
		AutoConfigURL string `json:"auto_config_url"`
		Proxy         string `json:"proxy"`
		Bypass        string `json:"bypass"`
		Err           string `json:"err,omitempty"`
	} `json:"ie_proxy"`
	DefaultProxy struct {
		OK         bool   `json:"ok"`
		AccessType uint32 `json:"access_type"`
		Proxy      string `json:"proxy"`
		Bypass     string `json:"bypass"`
		Err        string `json:"err,omitempty"`
	} `json:"default_proxy"`
	UploadKnown   upload `json:"upload_known"`
	UploadChunked upload `json:"upload_chunked"`
	// StreamLines reads a chunked response of 200 lines, one every 5 ms,
	// as N1 will: WinHttpQueryDataAvailable, then WinHttpReadData of that
	// many bytes. StreamLinesReadData is the first run's reader (a 4 KiB
	// WinHttpReadData loop), kept to record what synchronous ReadData does.
	StreamLines         streamResult `json:"stream_lines"`
	StreamLinesReadData streamResult `json:"stream_lines_readdata_only"`
	Cancel              struct {
		Stalled             bool    `json:"stalled"`
		WrittenBeforeCancel int64   `json:"written_before_cancel"`
		ReturnMs            float64 `json:"return_ms"`
		Errno               uint32  `json:"errno"`
		OK                  bool    `json:"ok"`
		Err                 string  `json:"err,omitempty"`
	} `json:"cancel"`
	WebSocket struct {
		UpgradeMs    float64 `json:"upgrade_ms"`
		Status       int     `json:"status"`
		TextOK       bool    `json:"text_ok"`
		TextRTTMs    float64 `json:"text_rtt_ms"`
		BinaryOK     bool    `json:"binary_ok"`
		BigOK        bool    `json:"big_ok"`
		BigBytes     int     `json:"big_bytes"`
		BigFragments int     `json:"big_fragments"`
		BigMs        float64 `json:"big_ms"`
		// Big is every large-message round trip: sent as one
		// WinHttpWebSocketSend ("single") or as 1 MiB BinaryFragment sends
		// ending with a BinaryMessage ("fragmented"), twice each.
		Big           []bigTrip `json:"big"`
		ServerGotPong bool      `json:"server_got_pong"`
		CloseStatus   uint16    `json:"close_status"`
		CloseReason   string    `json:"close_reason"`
		CloseErr      string    `json:"close_err,omitempty"`
		OK            bool      `json:"ok"`
		Err           string    `json:"err,omitempty"`
	} `json:"websocket"`
	Worked   bool    `json:"worked"`
	TimedOut bool    `json:"timed_out"`
	TotalMs  float64 `json:"total_ms"`
}

var (
	mu    sync.Mutex
	res   report
	start = time.Now()
)

func emit() {
	mu.Lock()
	defer mu.Unlock()
	res.TotalMs = ms(start)
	b, _ := json.MarshalIndent(&res, "", "  ")
	os.Stdout.Write(append(b, '\n'))
}

func ms(t time.Time) float64 { return float64(time.Since(t).Microseconds()) / 1000 }

func errs(err error) string {
	if err == nil {
		return ""
	}
	return err.Error()
}

// The loopback HTTP server.
var linesServerMs atomic.Int64 // microseconds the /lines handler took

func httpServer(ln net.Listener) {
	mux := http.NewServeMux()
	mux.HandleFunc("/upload", func(w http.ResponseWriter, r *http.Request) {
		n, _ := io.Copy(io.Discard, r.Body)
		fmt.Fprint(w, n)
	})
	mux.HandleFunc("/lines", func(w http.ResponseWriter, r *http.Request) {
		t := time.Now()
		f, _ := w.(http.Flusher)
		for i := range 200 {
			fmt.Fprintf(w, "line %d of the streamed response\n", i)
			if f != nil {
				f.Flush()
			}
			time.Sleep(5 * time.Millisecond)
		}
		linesServerMs.Store(time.Since(t).Microseconds())
	})
	mux.HandleFunc("/stall", func(w http.ResponseWriter, r *http.Request) {
		select { // never reads the body: the client's writes fill the socket and block
		case <-r.Context().Done():
		case <-time.After(60 * time.Second):
		}
	})
	http.Serve(ln, mux)
}

func readProxies() {
	var cfg ieProxyConfig
	err := boolCall("WinHttpGetIEProxyConfigForCurrentUser", pIEProxy, uintptr(unsafe.Pointer(&cfg)))
	mu.Lock()
	res.IEProxy.OK, res.IEProxy.Err = err == nil, errs(err)
	if err == nil {
		res.IEProxy.AutoDetect = cfg.autoDetect != 0
		res.IEProxy.AutoConfigURL, res.IEProxy.Proxy, res.IEProxy.Bypass = wstr(cfg.autoConfigURL), wstr(cfg.proxy), wstr(cfg.bypass)
	}
	mu.Unlock()
	for _, p := range []uintptr{cfg.autoConfigURL, cfg.proxy, cfg.bypass} {
		if p != 0 {
			call(pGlobalFree, p)
		}
	}
	var pi proxyInfo
	err = boolCall("WinHttpGetDefaultProxyConfiguration", pDefaultProxy, uintptr(unsafe.Pointer(&pi)))
	mu.Lock()
	res.DefaultProxy.OK, res.DefaultProxy.Err = err == nil, errs(err)
	res.DefaultProxy.AccessType, res.DefaultProxy.Proxy, res.DefaultProxy.Bypass = pi.accessType, wstr(pi.proxy), wstr(pi.bypass)
	mu.Unlock()
	for _, p := range []uintptr{pi.proxy, pi.bypass} {
		if p != 0 {
			call(pGlobalFree, p)
		}
	}
}

// doUpload sends size bytes to /upload, with a known length or chunked.
func doUpload(ses uintptr, port int, size int64, chunked bool) (u upload) {
	u.Bytes = size
	conn, req, err := request(ses, port, "POST", "/upload")
	if err != nil {
		u.Err = err.Error()
		return
	}
	defer closeAll(req, conn)
	addHeader(req, "Content-Type: application/octet-stream")
	total := uint32(size)
	if chunked {
		addHeader(req, "Transfer-Encoding: chunked")
		total = 0 // WINHTTP_IGNORE_REQUEST_TOTAL_LENGTH: the body is framed by us
	}
	t := time.Now()
	if err := send(req, total); err != nil {
		u.Err = err.Error()
		return
	}
	block := make([]byte, 1<<20)
	for i := range block {
		block[i] = byte(i * 7)
	}
	var framed []byte
	if chunked {
		framed = append([]byte(strconv.FormatInt(int64(len(block)), 16)+"\r\n"), block...)
		framed = append(framed, "\r\n"...)
	}
	var sent int64
	for sent < size {
		p := block
		if chunked {
			p = framed
		}
		if _, err := write(req, p); err != nil {
			u.Err = err.Error()
			return
		}
		sent += int64(len(block))
		if sent%(8<<20) == 0 || sent == size {
			u.Progress = append(u.Progress, sample{sent, ms(t)})
		}
	}
	if chunked {
		if _, err := write(req, []byte("0\r\n\r\n")); err != nil {
			u.Err = err.Error()
			return
		}
	}
	if err := receive(req); err != nil {
		u.Err = err.Error()
		return
	}
	u.Status, _ = status(req)
	body, err := readAll(req)
	u.Ms = ms(t)
	u.MBps = float64(size) / (1 << 20) / (u.Ms / 1000)
	u.ServerBytes, _ = strconv.ParseInt(string(bytes.TrimSpace(body)), 10, 64)
	u.Err = errs(err)
	u.OK = err == nil && u.Status == 200 && u.ServerBytes == size
	return
}

// streamLines reads /lines; withAvailable picks the reader.
func streamLines(ses uintptr, port int, withAvailable bool) (s streamResult) {
	s.Reader = "readdata_4k"
	if withAvailable {
		s.Reader = "query_data_available"
	}
	conn, req, err := request(ses, port, "GET", "/lines")
	defer closeAll(req, conn)
	if err == nil {
		err = addHeader(req, "Accept-Encoding: identity")
	}
	t := time.Now()
	if err == nil {
		err = send(req, 0)
	}
	if err == nil {
		err = receive(req)
	}
	if err == nil {
		s.Encoding = header(req, "Transfer-Encoding")
	}
	buf := make([]byte, 4096)
	last := t
	for err == nil {
		want := len(buf)
		if withAvailable {
			var avail int
			if avail, err = available(req); err != nil || avail == 0 {
				break
			}
			want = min(avail, len(buf))
		}
		var n int
		n, err = read(req, buf[:want])
		if n == 0 {
			break
		}
		s.Reads++
		s.MaxGapMs = max(s.MaxGapMs, ms(last))
		last = time.Now()
		c := bytes.Count(buf[:n], []byte{'\n'})
		if s.Lines == 0 && c > 0 {
			s.FirstLineMs = ms(t)
		}
		s.Lines += c
	}
	s.TotalMs, s.ServerMs = ms(t), float64(linesServerMs.Load())/1000
	// Streamed: the first line arrived within 50 ms (the server writes one
	// every 5 ms) and no read waited for more than 100 ms. Go's clock on
	// Windows moves in timer ticks (up to 15.6 ms), hence the slack.
	s.Streamed = s.Lines > 0 && s.FirstLineMs < 50 && s.MaxGapMs < 100
	s.Err = errs(err)
	s.OK = err == nil && s.Lines == 200 && s.Streamed
	return
}

// cancelUpload blocks a WinHttpWriteData on a server that never reads, then
// closes the request handle from this goroutine.
func cancelUpload(ses uintptr, port int) {
	c := &res.Cancel
	conn, req, err := request(ses, port, "POST", "/stall")
	if err == nil {
		err = send(req, 1<<30) // declares 1 GiB
	}
	if err != nil {
		closeAll(req, conn)
		mu.Lock()
		c.Err = err.Error()
		mu.Unlock()
		return
	}
	var written atomic.Int64
	done := make(chan error, 1)
	go func() {
		block := make([]byte, 1<<20)
		for {
			n, err := write(req, block)
			written.Add(int64(n))
			if err != nil {
				done <- err
				return
			}
		}
	}()
	stalled, last, since := false, int64(-1), time.Now()
	for end := time.Now().Add(5 * time.Second); time.Now().Before(end); time.Sleep(50 * time.Millisecond) {
		if w := written.Load(); w != last {
			last, since = w, time.Now()
		} else if time.Since(since) > 300*time.Millisecond {
			stalled = true
			break
		}
	}
	t := time.Now()
	call(pCloseHandle, req)
	var werr error
	select {
	case werr = <-done:
	case <-time.After(10 * time.Second):
		werr = fmt.Errorf("WinHttpWriteData still blocked 10 s after WinHttpCloseHandle")
	}
	ret := ms(t)
	call(pCloseHandle, conn)
	mu.Lock()
	c.Stalled, c.WrittenBeforeCancel, c.ReturnMs, c.Errno, c.Err = stalled, written.Load(), ret, errno(werr), errs(werr)
	c.OK = stalled && c.Errno == errOperationCancelled && ret < 1000
	mu.Unlock()
}

func wsSend(ws uintptr, kind int, p []byte) error {
	return dwordCall("WinHttpWebSocketSend", pWSSend, ws, uintptr(kind), uintptr(unsafe.Pointer(&p[0])), uintptr(len(p)))
}

// wsReceive reads one whole message into dst, counting its fragments.
func wsReceive(ws uintptr, dst []byte) (n, kind, fragments int, err error) {
	n, kind, st, err := wsReceiveStats(ws, dst)
	return n, kind, st.Types[wsBinaryFragment] + st.Types[wsUTF8Fragment], err
}

// wsReceiveStats is wsReceive with the pieces described. Each call offers
// at most 1 MiB of dst; a *Fragment type means more of the same message
// follows, a *Message type ends it.
func wsReceiveStats(ws uintptr, dst []byte) (n, kind int, st recvStats, err error) {
	st.PieceMin = -1
	for {
		var got, typ uint32
		chunk := dst[n:]
		if len(chunk) > 1<<20 {
			chunk = chunk[:1<<20]
		}
		if len(chunk) == 0 {
			return n, kind, st, fmt.Errorf("message longer than %d bytes", len(dst))
		}
		err = dwordCall("WinHttpWebSocketReceive", pWSReceive, ws, uintptr(unsafe.Pointer(&chunk[0])), uintptr(len(chunk)),
			uintptr(unsafe.Pointer(&got)), uintptr(unsafe.Pointer(&typ)))
		if err != nil {
			return
		}
		n += int(got)
		kind = int(typ)
		st.Pieces++
		if st.PieceMin < 0 || int(got) < st.PieceMin {
			st.PieceMin = int(got)
		}
		st.PieceMax = max(st.PieceMax, int(got))
		st.LastPiece = int(got)
		if kind >= 0 && kind < len(st.Types) {
			st.Types[kind]++
		}
		if kind == wsBinaryFragment || kind == wsUTF8Fragment {
			continue
		}
		return
	}
}

// bigRoundTrip sends big (as one message or in 1 MiB fragments) and reads
// the echo into buf.
func bigRoundTrip(ws uintptr, srv *wsServer, big, buf []byte, fragmented bool) (b bigTrip) {
	b.Mode, b.Sent, b.FirstMismatch = "single", len(big), -1
	srv.lastIn.Store(-1)
	t := time.Now()
	var err error
	if fragmented {
		b.Mode = "fragmented"
		for off := 0; off < len(big) && err == nil; off += 1 << 20 {
			end := min(off+1<<20, len(big))
			typ := wsBinaryFragment
			if end == len(big) {
				typ = wsBinaryMessage
			}
			err = wsSend(ws, typ, big[off:end])
		}
	} else {
		err = wsSend(ws, wsBinaryMessage, big)
	}
	if err == nil {
		var kind int
		b.Received, kind, b.Recv, err = wsReceiveStats(ws, buf)
		if err == nil && kind != wsBinaryMessage {
			err = fmt.Errorf("message ended with buffer type %d", kind)
		}
	}
	b.Ms = ms(t)
	b.ServerIn, b.ServerFrames, b.ServerOut = srv.lastIn.Load(), srv.lastInFrames.Load(), srv.lastOut.Load()
	for i := range min(b.Received, len(big)) {
		if buf[i] != big[i] {
			b.FirstMismatch = i
			break
		}
	}
	if b.FirstMismatch < 0 && b.Received != len(big) {
		b.FirstMismatch = min(b.Received, len(big))
	}
	b.Err = errs(err)
	b.OK = err == nil && b.Received == len(big) && b.FirstMismatch < 0
	return
}

func webSocket(ses uintptr, srv *wsServer, bigMiB int) {
	w := &res.WebSocket
	fail := func(err error) {
		mu.Lock()
		w.Err = err.Error()
		mu.Unlock()
	}
	port := srv.ln.Addr().(*net.TCPAddr).Port
	t := time.Now()
	conn, req, err := request(ses, port, "GET", "/ws")
	if err != nil {
		fail(err)
		return
	}
	defer closeAll(conn)
	if err := boolCall("WinHttpSetOption(UPGRADE_TO_WEB_SOCKET)", pSetOption, req, optionUpgradeToWebSocket, 0, 0); err != nil {
		closeAll(req)
		fail(err)
		return
	}
	if err = send(req, 0); err == nil {
		err = receive(req)
	}
	code, _ := status(req)
	mu.Lock()
	w.Status = code
	mu.Unlock()
	if err != nil {
		closeAll(req)
		fail(err)
		return
	}
	ws, e := call(pWSCompleteUpgrade, req, 0)
	closeAll(req) // the WebSocket handle lives on its own
	if ws == 0 {
		fail(&winErr{"WinHttpWebSocketCompleteUpgrade", e})
		return
	}
	defer closeAll(ws)
	mu.Lock()
	w.UpgradeMs = ms(t)
	mu.Unlock()

	buf := make([]byte, (bigMiB+1)<<20)
	text := []byte("hello from probe P8")
	t = time.Now()
	if err := wsSend(ws, wsUTF8Message, text); err != nil {
		fail(err)
		return
	}
	n, kind, _, err := wsReceive(ws, buf)
	mu.Lock()
	w.TextRTTMs, w.TextOK = ms(t), err == nil && kind == wsUTF8Message && bytes.Equal(buf[:n], text)
	mu.Unlock()
	if err != nil {
		fail(err)
		return
	}

	bin := make([]byte, 1024)
	for i := range bin {
		bin[i] = byte(i)
	}
	if err := wsSend(ws, wsBinaryMessage, bin); err != nil {
		fail(err)
		return
	}
	n, kind, _, err = wsReceive(ws, buf)
	mu.Lock()
	w.BinaryOK = err == nil && kind == wsBinaryMessage && bytes.Equal(buf[:n], bin)
	mu.Unlock()

	big := make([]byte, bigMiB<<20)
	for i := range big {
		big[i] = byte(i*31 + i>>16)
	}
	allOK := true
	for _, frag := range []bool{false, true, false, true} {
		b := bigRoundTrip(ws, srv, big, buf, frag)
		allOK = allOK && b.OK
		mu.Lock()
		w.Big = append(w.Big, b)
		if len(w.Big) == 1 { // the first run's fields: the first single send
			w.BigMs, w.BigBytes, w.BigFragments = b.Ms, b.Received, b.Recv.Types[wsBinaryFragment]
		}
		mu.Unlock()
		if b.Err != "" && b.Received == 0 {
			fail(errors.New(b.Err))
			return
		}
	}
	mu.Lock()
	w.BigOK = allOK
	w.ServerGotPong = srv.gotPong.Load()
	mu.Unlock()

	reason := []byte("client-bye")
	cerr := dwordCall("WinHttpWebSocketClose", pWSClose, ws, 1000, uintptr(unsafe.Pointer(&reason[0])), uintptr(len(reason)))
	var st uint16
	var used uint32
	rb := make([]byte, 123)
	qerr := dwordCall("WinHttpWebSocketQueryCloseStatus", pWSQueryCloseStatus, ws, uintptr(unsafe.Pointer(&st)),
		uintptr(unsafe.Pointer(&rb[0])), uintptr(len(rb)), uintptr(unsafe.Pointer(&used)))
	mu.Lock()
	w.CloseErr = errs(cerr)
	if qerr != nil && cerr == nil {
		w.CloseErr = qerr.Error()
	}
	w.CloseStatus, w.CloseReason = st, string(rb[:used])
	w.OK = w.TextOK && w.BinaryOK && w.BigOK && w.ServerGotPong && cerr == nil && st == 1000 && w.CloseReason == "server-bye"
	mu.Unlock()
}

func main() {
	timeout := flag.Duration("timeout", 180*time.Second, "give up after this long (exit status 3)")
	uploadMiB := flag.Int("upload-mib", 64, "size of each streamed upload, MiB")
	bigMiB := flag.Int("ws-big-mib", 32, "size of the large WebSocket message, MiB")
	flag.Parse()
	time.AfterFunc(*timeout, func() {
		mu.Lock()
		res.TimedOut = true
		mu.Unlock()
		emit()
		os.Exit(3)
	})
	if pOpen == 0 || pWSCompleteUpgrade == 0 {
		fmt.Fprintln(os.Stderr, "probe-p8: winhttp.dll or its WebSocket API is missing")
		os.Exit(1)
	}
	hl, err1 := net.Listen("tcp", "127.0.0.1:0")
	wl, err2 := net.Listen("tcp", "127.0.0.1:0")
	if err1 != nil || err2 != nil {
		fmt.Fprintln(os.Stderr, "probe-p8: no loopback listener:", err1, err2)
		os.Exit(1)
	}
	go httpServer(hl)
	srv := &wsServer{ln: wl}
	go srv.serve()
	port := hl.Addr().(*net.TCPAddr).Port

	ses, kind, err := session()
	mu.Lock()
	res.Session.AccessType, res.Session.OpenErr = kind, errs(err)
	mu.Unlock()
	if err != nil {
		emit()
		os.Exit(1)
	}
	defer closeAll(ses)
	call(pSetTimeouts, ses, 10000, 10000, 30000, 30000) // resolve, connect, send, receive (ms)

	readProxies()
	size := int64(*uploadMiB) << 20
	u := doUpload(ses, port, size, false)
	mu.Lock()
	res.UploadKnown = u
	mu.Unlock()
	u = doUpload(ses, port, size, true)
	mu.Lock()
	res.UploadChunked = u
	mu.Unlock()
	sl := streamLines(ses, port, true)
	slr := streamLines(ses, port, false)
	mu.Lock()
	res.StreamLines, res.StreamLinesReadData = sl, slr
	mu.Unlock()
	cancelUpload(ses, port)
	webSocket(ses, srv, *bigMiB)

	mu.Lock()
	res.Worked = res.UploadKnown.OK && res.UploadChunked.OK && res.StreamLines.OK && res.Cancel.OK && res.WebSocket.OK
	mu.Unlock()
	emit()
	os.Exit(0)
}
