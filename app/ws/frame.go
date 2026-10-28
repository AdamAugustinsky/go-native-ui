package ws

import (
	"bufio"
	"encoding/binary"
	"errors"
	"io"
	"slices"
	"sync"
	"sync/atomic"
	"time"
)

// RFC 6455 framing over a byte stream: the Linux client's (D73: net/http's
// upgrade plus our own framing), and the loopback server the tests run on
// every OS. It links nothing beyond bufio and io, and nothing on darwin or
// windows calls it outside the tests.
//
// A received frame is read whole into the caller's buffer, its length
// taken from its header, so a message is never cut short without an error
// (the arm64 short read of probe P8 was on WinHTTP's side, where frame
// lengths are not visible).

const (
	opCont   = 0x0
	opText   = 0x1
	opBinary = 0x2
	opClose  = 0x8
	opPing   = 0x9
	opPong   = 0xA

	// closeWait is how long Close waits for the server's close frame when a
	// read is pending to see it.
	closeWait = time.Second
	// writeChunk is the scratch a peer frames and masks writes in.
	writeChunk = 64 << 10
)

// errProtocol is a frame this end cannot accept; the connection is closed
// with CloseProtocolError.
var errProtocol = errors.New("ws: protocol error")

// peer is one end of a connection.
type peer struct {
	client bool // masks what it sends, accepts only unmasked frames
	br     *bufio.Reader
	conn   io.ReadWriteCloser
	max    int64
	// newKey fills a client's masking key for the next frame.
	newKey func(*[4]byte)
	// onPong, if set, sees each pong's payload (the tests' server).
	onPong func([]byte)
	// onClosed runs once after the connection is closed.
	onClosed func()

	// The reader's state; one reader at a time.
	rh   [8]byte
	rkey [4]byte
	rctl [125]byte
	rerr error // sticky

	wmu  sync.Mutex
	wbuf []byte
	wkey [4]byte

	closeSent atomic.Bool
	peerClose atomic.Pointer[CloseError] // the other end's close frame
	reading   atomic.Bool
	gotClose  chan struct{} // closed when the reader has read the peer's close frame
	closeOnce sync.Once
	gotOnce   sync.Once
}

func newPeer(conn io.ReadWriteCloser, br *bufio.Reader, client bool, max int64) *peer {
	if br == nil {
		br = bufio.NewReaderSize(conn, 64<<10)
	}
	return &peer{client: client, br: br, conn: conn, max: max, wbuf: make([]byte, 14+writeChunk), gotClose: make(chan struct{})}
}

// readHeader reads a frame's header: fin, opcode, payload length; a masked
// frame's key goes to p.rkey.
func (p *peer) readHeader() (fin bool, op byte, masked bool, n int64, err error) {
	if _, err = io.ReadFull(p.br, p.rh[:2]); err != nil {
		return
	}
	fin, op, masked = p.rh[0]&0x80 != 0, p.rh[0]&0x0F, p.rh[1]&0x80 != 0
	if p.rh[0]&0x70 != 0 || masked == p.client { // no extension; only clients mask
		return false, 0, false, 0, errProtocol
	}
	n = int64(p.rh[1] & 0x7F)
	switch n {
	case 126:
		if _, err = io.ReadFull(p.br, p.rh[:2]); err != nil {
			return
		}
		n = int64(binary.BigEndian.Uint16(p.rh[:2]))
	case 127:
		if _, err = io.ReadFull(p.br, p.rh[:8]); err != nil {
			return
		}
		u := binary.BigEndian.Uint64(p.rh[:8])
		if u>>63 != 0 {
			return false, 0, false, 0, errProtocol
		}
		n = int64(u)
	}
	if masked {
		_, err = io.ReadFull(p.br, p.rkey[:])
	}
	return
}

// payload reads n payload bytes into dst (len(dst) == n) and unmasks them.
func (p *peer) payload(dst []byte, masked bool) error {
	if _, err := io.ReadFull(p.br, dst); err != nil {
		return err
	}
	if masked {
		mask(p.rkey, 0, dst)
	}
	return nil
}

// mask XORs b with key, b starting at offset pos of the payload.
func mask(key [4]byte, pos int, b []byte) {
	for i := range b {
		b[i] ^= key[(pos+i)&3]
	}
}

// read reads the next whole message, appending to buf (D73: into the
// caller's buffer). Pings are answered and pongs passed to onPong on the
// way.
func (p *peer) read(buf []byte) (MessageKind, []byte, error) {
	if p.rerr != nil {
		return 0, buf, p.rerr
	}
	p.reading.Store(true)
	defer p.reading.Store(false)
	var kind MessageKind
	for {
		fin, op, masked, n, err := p.readHeader()
		if err != nil {
			return 0, buf, p.fail(err)
		}
		if op >= 0x8 { // a control frame, possibly between fragments
			if !fin || n > 125 {
				return 0, buf, p.fail(errProtocol)
			}
			ctl := p.rctl[:n]
			if err := p.payload(ctl, masked); err != nil {
				return 0, buf, p.fail(err)
			}
			switch op {
			case opPing:
				p.control(opPong, ctl)
			case opPong:
				if p.onPong != nil {
					p.onPong(ctl)
				}
			case opClose:
				return 0, buf, p.closed(ctl)
			default:
				return 0, buf, p.fail(errProtocol)
			}
			continue
		}
		switch {
		case (op == opText || op == opBinary) && kind == 0:
			kind = MessageKind(op) // Text and Binary are the opcodes
		case op == opCont && kind != 0:
		default:
			return 0, buf, p.fail(errProtocol)
		}
		if int64(len(buf))+n > p.max {
			p.sendClose(CloseTooBig, "")
			p.rerr = ErrTooLarge
			p.conn.Close()
			return 0, buf, ErrTooLarge
		}
		start := len(buf)
		buf = slices.Grow(buf, int(n))[:start+int(n)]
		if err := p.payload(buf[start:], masked); err != nil {
			return 0, buf[:start], p.fail(err)
		}
		if fin {
			return kind, buf, nil
		}
	}
}

// closed handles the peer's close frame: it answers one the peer started,
// and returns the read's error.
func (p *peer) closed(ctl []byte) error {
	ce := &CloseError{Code: CloseNoStatus}
	if len(ctl) >= 2 {
		ce.Code, ce.Reason = CloseCode(binary.BigEndian.Uint16(ctl)), string(ctl[2:])
	}
	p.peerClose.Store(ce)
	p.gotOnce.Do(func() { close(p.gotClose) })
	if p.closeSent.Load() {
		p.rerr = ErrClosed
	} else {
		code := ce.Code
		if code == CloseNoStatus {
			code = CloseNormal
		}
		p.sendClose(code, "")
		p.rerr = ce
	}
	p.conn.Close()
	return p.rerr
}

// fail turns a read error into the read's sticky error: ErrClosed after
// this end's Close, a protocol error (answered with a close frame), or a
// dropped connection.
func (p *peer) fail(err error) error {
	switch {
	case p.closeSent.Load():
		p.rerr = ErrClosed
	case errors.Is(err, errProtocol):
		p.sendClose(CloseProtocolError, "")
		p.rerr = err
	default:
		p.rerr = &CloseError{Code: CloseAbnormal}
	}
	p.conn.Close()
	return p.rerr
}

// write sends one message as one frame.
func (p *peer) write(kind MessageKind, data []byte) error {
	p.wmu.Lock()
	defer p.wmu.Unlock()
	if p.closeSent.Load() {
		return ErrClosed
	}
	if err := p.frame(true, byte(kind), data); err != nil {
		if p.closeSent.Load() {
			return ErrClosed
		}
		return err
	}
	return nil
}

// frame writes one frame (p.wmu held), in pieces of writeChunk, each in one
// Write: the header with the first piece.
func (p *peer) frame(fin bool, op byte, data []byte) error {
	h := p.wbuf[:0]
	b0 := op
	if fin {
		b0 |= 0x80
	}
	var mbit byte
	if p.client {
		mbit = 0x80
	}
	switch n := len(data); {
	case n < 126:
		h = append(h, b0, byte(n)|mbit)
	case n <= 0xFFFF:
		h = append(h, b0, 126|mbit, byte(n>>8), byte(n))
	default:
		h = append(h, b0, 127|mbit)
		h = binary.BigEndian.AppendUint64(h, uint64(n))
	}
	if p.client {
		p.newKey(&p.wkey)
		h = append(h, p.wkey[:]...)
	}
	pos := 0
	for {
		k := copy(p.wbuf[len(h):cap(p.wbuf)], data[pos:])
		out := p.wbuf[:len(h)+k]
		if p.client {
			mask(p.wkey, pos, out[len(h):])
		}
		if _, err := p.conn.Write(out); err != nil {
			return err
		}
		pos += k
		h = h[:0]
		if pos == len(data) {
			return nil
		}
	}
}

// control sends a control frame, unless a write holds the connection (a
// pong is then skipped: the peer's next ping gets one).
func (p *peer) control(op byte, payload []byte) {
	if !p.wmu.TryLock() {
		return
	}
	defer p.wmu.Unlock()
	if !p.closeSent.Load() {
		p.frame(true, op, payload)
	}
}

// sendClose sends a close frame once.
func (p *peer) sendClose(code CloseCode, reason string) bool {
	if !p.wmu.TryLock() {
		return false // a write is blocked: closing the connection ends it
	}
	defer p.wmu.Unlock()
	if p.closeSent.Swap(true) {
		return false
	}
	var b [125]byte
	binary.BigEndian.PutUint16(b[:], uint16(code))
	n := 2 + copy(b[2:], reason)
	p.frame(true, opClose, b[:n])
	return true
}

// close sends a close frame, waits up to closeWait for the peer's when a
// read is pending to see it, and closes the connection.
func (p *peer) close(code CloseCode, reason string) error {
	p.closeOnce.Do(func() {
		if !p.sendClose(code, reason) {
			p.closeSent.Store(true)
		}
		if p.reading.Load() {
			t := time.NewTimer(closeWait)
			select {
			case <-p.gotClose:
			case <-t.C:
			}
			t.Stop()
		}
		p.conn.Close()
		if p.onClosed != nil {
			p.onClosed()
		}
	})
	return nil
}
