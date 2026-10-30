package session

import (
	"bufio"
	"encoding/binary"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"sync"
)

// Version is the wire protocol's version. A daemon refuses a client whose
// hello names another one.
const Version = 1

// A frame is a 4-byte big-endian length n, then n bytes: a type byte and
// the payload. Control payloads are JSON; data payloads are raw bytes.
const (
	maxFrame   = 1 << 20
	maxPayload = 64 << 10 // output and input are split into frames this big
)

type frameType byte

const (
	fHello   frameType = 1  // both ways, JSON hello
	fRequest frameType = 2  // client → daemon, JSON request
	fReply   frameType = 3  // daemon → client, JSON reply
	fInput   frameType = 4  // client → daemon, raw input for the program
	fResize  frameType = 5  // client → daemon, 4 × uint32: cols, rows, width, height
	fDetach  frameType = 6  // client → daemon, empty
	fClose   frameType = 7  // client → daemon, empty
	fReset   frameType = 8  // daemon → client, empty: reset the terminal, the replay follows
	fOutput  frameType = 9  // daemon → client, raw output
	fLive    frameType = 10 // daemon → client, empty: the replay is over
	fExit    frameType = 11 // daemon → client, int32 exit code; no output follows
)

type hello struct {
	Version int
	Pid     int `json:",omitempty"`
}

type request struct {
	Op      string   // create, list, attach, kill, shutdown
	ID      string   `json:",omitempty"`
	Options *Options `json:",omitempty"`
	Size    Size
}

type reply struct {
	Error string `json:",omitempty"`
	Info  *Info  `json:",omitempty"`
	List  []Info `json:",omitempty"`
}

func (r *reply) err() error {
	switch r.Error {
	case "":
		return nil
	case ErrNotFound.Error():
		return ErrNotFound
	}
	return errors.New(r.Error)
}

// framer reads and writes frames on one connection; writes are safe from
// several goroutines.
type framer struct {
	r    *bufio.Reader
	rbuf []byte

	mu   sync.Mutex
	w    io.Writer
	wbuf []byte
}

func newFramer(rw io.ReadWriter) *framer {
	return &framer{r: bufio.NewReaderSize(rw, 64<<10), w: rw}
}

func (f *framer) write(t frameType, payload []byte) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.wbuf = binary.BigEndian.AppendUint32(f.wbuf[:0], uint32(len(payload)+1))
	f.wbuf = append(f.wbuf, byte(t))
	f.wbuf = append(f.wbuf, payload...)
	_, err := f.w.Write(f.wbuf)
	if cap(f.wbuf) > 2*maxPayload {
		f.wbuf = nil
	}
	return err
}

// writeData writes p as frames of type t, maxPayload bytes at most each.
func (f *framer) writeData(t frameType, p []byte) error {
	for len(p) > 0 {
		k := min(len(p), maxPayload)
		if err := f.write(t, p[:k]); err != nil {
			return err
		}
		p = p[k:]
	}
	return nil
}

func (f *framer) writeJSON(t frameType, v any) error {
	b, err := json.Marshal(v)
	if err != nil {
		return err
	}
	return f.write(t, b)
}

// read returns the next frame; the payload is valid until the next read.
func (f *framer) read() (frameType, []byte, error) {
	var h [4]byte
	if _, err := io.ReadFull(f.r, h[:]); err != nil {
		return 0, nil, err
	}
	n := binary.BigEndian.Uint32(h[:])
	if n == 0 || n > maxFrame {
		return 0, nil, fmt.Errorf("session: bad frame length %d", n)
	}
	if cap(f.rbuf) < int(n) {
		f.rbuf = make([]byte, n)
	}
	b := f.rbuf[:n]
	if _, err := io.ReadFull(f.r, b); err != nil {
		return 0, nil, err
	}
	return frameType(b[0]), b[1:], nil
}

func (f *framer) readJSON(want frameType, v any) error {
	t, b, err := f.read()
	if err != nil {
		return err
	}
	if t != want {
		return fmt.Errorf("session: frame %d, want %d", t, want)
	}
	return json.Unmarshal(b, v)
}

func encodeSize(s Size) []byte {
	b := make([]byte, 0, 16)
	for _, v := range [...]int{s.Cols, s.Rows, s.Width, s.Height} {
		b = binary.BigEndian.AppendUint32(b, uint32(v))
	}
	return b
}

func decodeSize(b []byte) (Size, bool) {
	if len(b) != 16 {
		return Size{}, false
	}
	u := func(i int) int { return int(binary.BigEndian.Uint32(b[4*i:])) }
	return Size{u(0), u(1), u(2), u(3)}, true
}
