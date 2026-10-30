package instance

import (
	"bytes"
	"encoding/binary"
	"encoding/json"
	"reflect"
	"runtime"
	"testing"
	"time"
)

type fuzzReader struct {
	*bytes.Reader
	chunk, consumed int
}

func (r *fuzzReader) Read(p []byte) (int, error) {
	n, err := r.Reader.Read(p[:min(len(p), r.chunk)])
	r.consumed += n
	return n, err
}

// At most 2*maxFrame input bytes; rejected lengths read just four bytes.
// The body allocation is bounded by maxFrame even for a truncated frame.
// Per case: 5 s and 8 MiB + 128*input bytes, including JSON round trips.
func FuzzInstanceFrames(f *testing.F) {
	for _, body := range []string{`{"id":"x","args":["a","file:///tmp/a"],"dir":"/tmp"}`, `{"id":"x","args":null}`, `{}`, `null`, `{"id":"x","args":["\ud800","\u0000"]}`} {
		f.Add(append(binary.BigEndian.AppendUint32(nil, uint32(len(body))), body...), uint16(1))
	}
	for _, size := range []uint32{0, 1, maxFrame, maxFrame + 1, 0x7fffffff, 0xffffffff} {
		f.Add(binary.BigEndian.AppendUint32(nil, size), uint16(2))
	}
	f.Add([]byte{0, 1}, uint16(1))
	f.Fuzz(func(t *testing.T, data []byte, split uint16) {
		if len(data) > 2*maxFrame {
			t.Skip()
		}
		start := time.Now()
		var before, after runtime.MemStats
		runtime.ReadMemStats(&before)
		defer func() {
			runtime.ReadMemStats(&after)
			if time.Since(start) > 5*time.Second || after.TotalAlloc-before.TotalAlloc > uint64(8<<20+128*len(data)) {
				t.Fatal("IPC resource bound exceeded")
			}
		}()
		r := &fuzzReader{Reader: bytes.NewReader(data), chunk: 1 + int(split)%1024}
		id, req, ok := readRequest(r)
		var want wire
		valid := false
		if len(data) >= 4 {
			n := binary.BigEndian.Uint32(data)
			if n == 0 || n > maxFrame {
				if r.consumed != 4 {
					t.Fatal("invalid size read a body")
				}
			} else if uint64(n)+4 <= uint64(len(data)) {
				valid = json.Unmarshal(data[4:4+int(n)], &want) == nil && want.ID != "" && len(want.ID) <= 64
				if r.consumed != 4+int(n) {
					t.Fatal("reader crossed frame boundary")
				}
			}
		}
		if ok != valid {
			t.Fatalf("acceptance differs: got %v, want %v", ok, valid)
		}
		if !ok {
			return
		}
		if id != want.ID || !reflect.DeepEqual(req, Request{Args: want.Args, Dir: want.Dir}) {
			t.Fatal("decoded message differs")
		}
		b, err := json.Marshal(want)
		if err != nil {
			t.Fatal(err)
		}
		// Escaping can enlarge a valid message beyond the transport limit.
		if len(b) > maxFrame {
			return
		}
		frame := append(binary.BigEndian.AppendUint32(nil, uint32(len(b))), b...)
		id2, req2, ok2 := readRequest(&fuzzReader{Reader: bytes.NewReader(frame), chunk: 1})
		if !ok2 || id2 != id || !reflect.DeepEqual(req, req2) {
			t.Fatal("split-read round trip")
		}
	})
}
