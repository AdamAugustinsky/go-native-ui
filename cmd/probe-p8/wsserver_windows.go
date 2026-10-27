//go:build windows

package main

import (
	"bufio"
	"crypto/sha1"
	"encoding/base64"
	"encoding/binary"
	"errors"
	"io"
	"net"
	"net/textproto"
	"strings"
	"sync/atomic"
)

// wsServer is a minimal RFC 6455 server for the probe: it accepts the
// upgrade, sends one ping, echoes every message as one unmasked frame,
// answers pings, records pongs, and answers a close with 1000 "server-bye".
type wsServer struct {
	ln      net.Listener
	gotPong atomic.Bool
	// The last message received whole: its length and frame count, and the
	// length echoed (one frame).
	lastIn, lastInFrames, lastOut atomic.Int64
}

const wsGUID = "258EAFA5-E914-47DA-95CA-C5AB0DC85B11"

func (s *wsServer) serve() {
	for {
		c, err := s.ln.Accept()
		if err != nil {
			return
		}
		go s.handle(c)
	}
}

func (s *wsServer) handle(c net.Conn) {
	defer c.Close()
	br := bufio.NewReaderSize(c, 64<<10)
	tp := textproto.NewReader(br)
	if _, err := tp.ReadLine(); err != nil { // GET /ws HTTP/1.1
		return
	}
	h, err := tp.ReadMIMEHeader()
	if err != nil || !strings.EqualFold(h.Get("Upgrade"), "websocket") {
		io.WriteString(c, "HTTP/1.1 400 Bad Request\r\nContent-Length: 0\r\n\r\n")
		return
	}
	sum := sha1.Sum([]byte(h.Get("Sec-WebSocket-Key") + wsGUID))
	io.WriteString(c, "HTTP/1.1 101 Switching Protocols\r\nUpgrade: websocket\r\nConnection: Upgrade\r\nSec-WebSocket-Accept: "+
		base64.StdEncoding.EncodeToString(sum[:])+"\r\n\r\n")
	bw := bufio.NewWriterSize(c, 64<<10)
	writeFrame(bw, 0x9, []byte("p8")) // a ping: the client's WinHTTP is to answer it
	bw.Flush()

	var msg []byte
	var msgOp byte
	var frames int64
	for {
		fin, op, payload, err := readFrame(br)
		if err != nil {
			return
		}
		switch op {
		case 0x8: // close
			reason := []byte{0x03, 0xE8} // 1000
			writeFrame(bw, 0x8, append(reason, "server-bye"...))
			bw.Flush()
			return
		case 0x9:
			writeFrame(bw, 0xA, payload)
			bw.Flush()
			continue
		case 0xA:
			s.gotPong.Store(true)
			continue
		case 0x1, 0x2:
			msgOp, msg, frames = op, append(msg[:0], payload...), 1
		case 0x0:
			msg, frames = append(msg, payload...), frames+1
		default:
			return
		}
		if fin {
			s.lastIn.Store(int64(len(msg)))
			s.lastInFrames.Store(frames)
			s.lastOut.Store(int64(len(msg)))
			writeFrame(bw, msgOp, msg)
			if bw.Flush() != nil {
				return
			}
		}
	}
}

const wsMaxFrame = 128 << 20

func readFrame(r *bufio.Reader) (fin bool, op byte, payload []byte, err error) {
	var hdr [2]byte
	if _, err = io.ReadFull(r, hdr[:]); err != nil {
		return
	}
	fin, op = hdr[0]&0x80 != 0, hdr[0]&0x0F
	masked := hdr[1]&0x80 != 0
	n := uint64(hdr[1] & 0x7F)
	switch n {
	case 126:
		var b [2]byte
		if _, err = io.ReadFull(r, b[:]); err != nil {
			return
		}
		n = uint64(binary.BigEndian.Uint16(b[:]))
	case 127:
		var b [8]byte
		if _, err = io.ReadFull(r, b[:]); err != nil {
			return
		}
		n = binary.BigEndian.Uint64(b[:])
	}
	if n > wsMaxFrame {
		return false, 0, nil, errors.New("frame too large")
	}
	var key [4]byte
	if masked {
		if _, err = io.ReadFull(r, key[:]); err != nil {
			return
		}
	}
	payload = make([]byte, n)
	if _, err = io.ReadFull(r, payload); err != nil {
		return
	}
	if masked {
		for i := range payload {
			payload[i] ^= key[i&3]
		}
	}
	return
}

func writeFrame(w *bufio.Writer, op byte, p []byte) {
	w.WriteByte(0x80 | op)
	switch n := len(p); {
	case n < 126:
		w.WriteByte(byte(n))
	case n <= 0xFFFF:
		w.WriteByte(126)
		var b [2]byte
		binary.BigEndian.PutUint16(b[:], uint16(n))
		w.Write(b[:])
	default:
		w.WriteByte(127)
		var b [8]byte
		binary.BigEndian.PutUint64(b[:], uint64(n))
		w.Write(b[:])
	}
	w.Write(p)
}
