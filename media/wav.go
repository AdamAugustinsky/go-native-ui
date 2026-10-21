package media

import (
	"encoding/binary"
	"fmt"
)

// WAV (RIFF WAVE) is how PCM reaches the system's player and comes back from
// its recorder.

const (
	wavPCM   = 1 // WAVE_FORMAT_PCM
	wavFloat = 3 // WAVE_FORMAT_IEEE_FLOAT
	wavExt   = 0xFFFE

	// maxWAVData is the most PCM a WAV can hold: the RIFF size is 32-bit.
	maxWAVData = 1<<32 - 1 - 64
)

// wavHeaderLen is the size of the header appendWAVHeader writes for f.
func wavHeaderLen(f Format) int {
	if f.Sample == Float32 {
		return 12 + 8 + 18 + 12 + 8 // a non-PCM fmt has cbSize and a fact chunk
	}
	return 12 + 8 + 16 + 8
}

// appendWAVHeader appends the header of a WAV holding n bytes of PCM in f,
// which must be valid; n must be a whole number of frames, at most
// maxWAVData.
func appendWAVHeader(dst []byte, f Format, n int) []byte {
	le := binary.LittleEndian
	tag, fmtLen := uint16(wavPCM), uint32(16)
	if f.Sample == Float32 {
		tag, fmtLen = wavFloat, 18
	}
	blockAlign := f.FrameBytes()
	riff := uint32(wavHeaderLen(f) - 8 + n + n&1)
	dst = append(dst, "RIFF"...)
	dst = le.AppendUint32(dst, riff)
	dst = append(dst, "WAVEfmt "...)
	dst = le.AppendUint32(dst, fmtLen)
	dst = le.AppendUint16(dst, tag)
	dst = le.AppendUint16(dst, uint16(f.Channels))
	dst = le.AppendUint32(dst, uint32(f.SampleRate))
	dst = le.AppendUint32(dst, uint32(f.SampleRate*blockAlign))
	dst = le.AppendUint16(dst, uint16(blockAlign))
	dst = le.AppendUint16(dst, uint16(8*f.Sample.Bytes()))
	if f.Sample == Float32 {
		dst = le.AppendUint16(dst, 0) // cbSize
		dst = append(dst, "fact"...)
		dst = le.AppendUint32(dst, 4)
		dst = le.AppendUint32(dst, uint32(n/blockAlign))
	}
	dst = append(dst, "data"...)
	return le.AppendUint32(dst, uint32(n))
}

// parseWAV finds the format and the PCM of a WAV file: the data chunk, cut to
// whole frames and to the bytes present (a file whose recording was cut short
// may claim more). Chunks it does not know (Apple's FLLR padding, LIST) are
// skipped. Only 16-bit integer and 32-bit float PCM are accepted.
func parseWAV(b []byte) (Format, []byte, error) {
	le := binary.LittleEndian
	if len(b) < 12 || string(b[0:4]) != "RIFF" || string(b[8:12]) != "WAVE" {
		return Format{}, nil, fmt.Errorf("%w: not a WAV file", ErrInvalid)
	}
	var f Format
	haveFmt := false
	for p := 12; p+8 <= len(b); {
		id, size := string(b[p:p+4]), int64(le.Uint32(b[p+4:p+8]))
		body := b[p+8:]
		if int64(len(body)) > size {
			body = body[:size]
		}
		switch id {
		case "fmt ":
			if len(body) < 16 {
				return Format{}, nil, fmt.Errorf("%w: a WAV fmt chunk of %d bytes", ErrInvalid, len(body))
			}
			tag, bits := le.Uint16(body[0:2]), le.Uint16(body[14:16])
			if tag == wavExt && len(body) >= 26 {
				tag = le.Uint16(body[24:26]) // the subformat GUID's first two bytes
			}
			f = Format{SampleRate: int(le.Uint32(body[4:8])), Channels: int(le.Uint16(body[2:4]))}
			switch {
			case tag == wavPCM && bits == 16:
				f.Sample = Int16
			case tag == wavFloat && bits == 32:
				f.Sample = Float32
			default:
				return Format{}, nil, fmt.Errorf("%w: WAV format %d with %d-bit samples", ErrInvalid, tag, bits)
			}
			if err := f.validate(); err != nil {
				return Format{}, nil, err
			}
			haveFmt = true
		case "data":
			if !haveFmt {
				return Format{}, nil, fmt.Errorf("%w: WAV data before its fmt chunk", ErrInvalid)
			}
			n := len(body) / f.FrameBytes() * f.FrameBytes()
			return f, body[:n:n], nil
		}
		if size > int64(len(b)) {
			break
		}
		p += 8 + int(size) + int(size&1)
	}
	return Format{}, nil, fmt.Errorf("%w: a WAV file without data", ErrInvalid)
}
