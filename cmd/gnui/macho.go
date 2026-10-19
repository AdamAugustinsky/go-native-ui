package main

import (
	"bytes"
	"debug/macho"
	"encoding/binary"
	"fmt"
)

// fatMagic is the big-endian magic of a universal ("fat") Mach-O file.
const fatMagic = 0xcafebabe

// machoSlice is one thin Mach-O image to put into a universal binary.
type machoSlice struct {
	data []byte
}

// makeUniversal merges thin Mach-O executables into one universal binary,
// the format lipo writes: a big-endian header, then each slice at an offset
// aligned to its page size (16 KiB for arm64, 4 KiB otherwise).
func makeUniversal(slices []machoSlice) ([]byte, error) {
	if len(slices) == 0 {
		return nil, fmt.Errorf("universal: no slices")
	}
	type info struct {
		cpu, sub uint32
		align    uint32 // log2
		off      uint32
		data     []byte
	}
	infos := make([]info, len(slices))
	seen := map[macho.Cpu]bool{}
	for i, s := range slices {
		f, err := macho.NewFile(bytes.NewReader(s.data))
		if err != nil {
			return nil, fmt.Errorf("universal: slice %d is not a thin Mach-O: %w", i, err)
		}
		if seen[f.Cpu] {
			return nil, fmt.Errorf("universal: two slices for cpu %v", f.Cpu)
		}
		seen[f.Cpu] = true
		align := uint32(12)
		if f.Cpu == macho.CpuArm64 {
			align = 14
		}
		infos[i] = info{cpu: uint32(f.Cpu), sub: f.SubCpu, align: align, data: s.data}
	}
	off := uint32(8 + 20*len(infos))
	for i := range infos {
		a := uint32(1) << infos[i].align
		off = (off + a - 1) &^ (a - 1)
		infos[i].off = off
		off += uint32(len(infos[i].data))
	}
	var out bytes.Buffer
	be := binary.BigEndian
	binary.Write(&out, be, uint32(fatMagic))
	binary.Write(&out, be, uint32(len(infos)))
	for _, in := range infos {
		binary.Write(&out, be, in.cpu)
		binary.Write(&out, be, in.sub)
		binary.Write(&out, be, in.off)
		binary.Write(&out, be, uint32(len(in.data)))
		binary.Write(&out, be, in.align)
	}
	for _, in := range infos {
		out.Write(make([]byte, int(in.off)-out.Len()))
		out.Write(in.data)
	}
	return out.Bytes(), nil
}

// isMachO reports whether the first bytes of a file are a Mach-O or universal
// magic number, which is what makes a file signable code.
func isMachO(head []byte) bool {
	if len(head) < 4 {
		return false
	}
	m := binary.BigEndian.Uint32(head)
	switch m {
	case 0xfeedface, 0xfeedfacf, 0xcefaedfe, 0xcffaedfe, fatMagic, 0xbebafeca:
		return true
	}
	return false
}
