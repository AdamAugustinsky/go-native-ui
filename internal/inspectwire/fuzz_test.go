package inspectwire

import (
	"bufio"
	"bytes"
	"encoding/json"
	"errors"
	"io"
	"reflect"
	"runtime"
	"testing"
	"time"
)

type fuzzChunks struct {
	*bytes.Reader
	chunk, read int
}

func (r *fuzzChunks) Read(p []byte) (int, error) {
	n, err := r.Reader.Read(p[:min(len(p), r.chunk)])
	r.read += n
	return n, err
}

type fuzzEcho struct{ req *Request }

func (h *fuzzEcho) Handle(r *Request) *Response {
	h.req = r
	return &Response{OK: true}
}

// The protocol has no declared length: a line, including its newline, is
// capped at MaxLine. Input is at most 2*MaxLine; reads stop within one 4 KiB
// buffer of the limit. Per case: 5 s and 8 MiB + 128*input bytes allocated.
func FuzzInspectFrames(f *testing.F) {
	f.Add([]byte(`{"op":"tree","steps":[]}`), uint16(42))
	for _, s := range []string{"", "\n", "null\n", `{"op":"tree","id":"a"}`, `{"op":"do","steps":[{"action":"type","text":"a\\nb"}]}`, `{"op":"shot","path":"/tmp/a","timeout_ms":18446744073709551615}`, "{} {}\n", "\xff\xff\xff\xff", string(bytes.Repeat([]byte{'x'}, MaxLine+1))} {
		f.Add([]byte(s), uint16(1))
	}
	f.Fuzz(func(t *testing.T, data []byte, split uint16) {
		if len(data) > 2*MaxLine {
			t.Skip()
		}
		start := time.Now()
		var before, after runtime.MemStats
		runtime.ReadMemStats(&before)
		defer func() {
			runtime.ReadMemStats(&after)
			if time.Since(start) > 5*time.Second || after.TotalAlloc-before.TotalAlloc > uint64(8<<20+128*len(data)) {
				t.Fatal("frame resource bound exceeded")
			}
		}()
		r := &fuzzChunks{Reader: bytes.NewReader(data), chunk: 1 + int(split)%4096}
		line, err := ReadLine(bufio.NewReaderSize(r, 4096), MaxLine)
		end := bytes.IndexByte(data, '\n')
		if end < 0 {
			end = len(data)
		} else {
			end++
		}
		switch {
		case end > MaxLine:
			if !errors.Is(err, ErrLineTooLong) {
				t.Fatalf("oversized line accepted: %v", err)
			}
			if r.read > MaxLine+4096 {
				t.Fatalf("read past bound: %d", r.read)
			}
			return
		case len(data) == 0:
			if !errors.Is(err, io.EOF) {
				t.Fatalf("empty input: %v", err)
			}
			return
		}
		want := bytes.TrimSuffix(data[:end], []byte{'\n'})
		want = bytes.TrimRight(want, "\r")
		if err != nil || !bytes.Equal(line, want) {
			t.Fatalf("split line mismatch: %v", err)
		}
		h := new(fuzzEcho)
		resp := Dispatch(h, line)
		encoded := resp.Marshal()
		if len(encoded) > 6*len(line)+4096 || bytes.Count(encoded, []byte{'\n'}) != 1 {
			t.Fatal("response framing/size")
		}
		var back Response
		if err := json.Unmarshal(encoded, &back); err != nil || !reflect.DeepEqual(resp, &back) {
			t.Fatalf("response round trip: %v", err)
		}
		if h.req != nil {
			b, err := json.Marshal(h.req)
			if err != nil {
				t.Fatal(err)
			}
			h2 := new(fuzzEcho)
			Dispatch(h2, b)
			// omitempty canonicalizes an empty Steps slice to nil. Compare
			// canonical messages; these represent the same protocol request.
			b2, err := json.Marshal(h2.req)
			if err != nil || !bytes.Equal(b, b2) || resp.ID != h.req.ID {
				t.Fatal("request round trip/ID")
			}
		} else if resp.OK || resp.Error == nil || resp.Error.Code != ErrBadRequest {
			t.Fatal("malformed request was not rejected")
		}
	})
}
