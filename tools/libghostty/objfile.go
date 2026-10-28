package main

import (
	"bytes"
	"debug/elf"
	"debug/macho"
	"debug/pe"
	"encoding/binary"
	"errors"
	"fmt"
	"sort"
	"strings"
)

// listExports is the sorted list of ghostty_ functions the library exports:
// the export table of a PE DLL, the defined global dynamic symbols of an
// ELF object, the external defined symbols of a Mach-O dylib.
func listExports(goos string, data []byte) ([]string, error) {
	var names []string
	switch goos {
	case "windows":
		n, err := peExports(data)
		if err != nil {
			return nil, err
		}
		names = n
	case "darwin":
		f, err := macho.NewFile(bytes.NewReader(data))
		if err != nil {
			return nil, err
		}
		if f.Symtab == nil {
			return nil, errors.New("no symbol table")
		}
		for _, s := range f.Symtab.Syms {
			const nExt, nSect = 0x01, 0x0e
			if s.Type&nExt != 0 && s.Type&0x0e == nSect && s.Sect != 0 {
				names = append(names, strings.TrimPrefix(s.Name, "_"))
			}
		}
	default:
		f, err := elf.NewFile(bytes.NewReader(data))
		if err != nil {
			return nil, err
		}
		syms, err := f.DynamicSymbols()
		if err != nil {
			return nil, err
		}
		for _, s := range syms {
			if elf.ST_BIND(s.Info) == elf.STB_GLOBAL && s.Section != elf.SHN_UNDEF && elf.ST_TYPE(s.Info) == elf.STT_FUNC {
				names = append(names, s.Name)
			}
		}
	}
	var out []string
	for _, n := range names {
		if strings.HasPrefix(n, "ghostty_") {
			out = append(out, n)
		}
	}
	sort.Strings(out)
	return out, nil
}

// peExports reads the names in a DLL's export directory.
func peExports(data []byte) ([]string, error) {
	f, err := pe.NewFile(bytes.NewReader(data))
	if err != nil {
		return nil, err
	}
	var dd pe.DataDirectory
	switch oh := f.OptionalHeader.(type) {
	case *pe.OptionalHeader64:
		dd = oh.DataDirectory[pe.IMAGE_DIRECTORY_ENTRY_EXPORT]
	case *pe.OptionalHeader32:
		dd = oh.DataDirectory[pe.IMAGE_DIRECTORY_ENTRY_EXPORT]
	default:
		return nil, errors.New("no optional header")
	}
	if dd.VirtualAddress == 0 {
		return nil, errors.New("no export directory")
	}
	// rva maps a relative virtual address to its bytes in the file.
	rva := func(a uint32) ([]byte, error) {
		for _, s := range f.Sections {
			if a >= s.VirtualAddress && a < s.VirtualAddress+s.VirtualSize {
				d, err := s.Data()
				if err != nil {
					return nil, err
				}
				off := a - s.VirtualAddress
				if int(off) >= len(d) {
					return nil, fmt.Errorf("rva %#x past %s's data", a, s.Name)
				}
				return d[off:], nil
			}
		}
		return nil, fmt.Errorf("rva %#x in no section", a)
	}
	dir, err := rva(dd.VirtualAddress)
	if err != nil || len(dir) < 40 {
		return nil, fmt.Errorf("export directory: %v", err)
	}
	le := binary.LittleEndian
	n := le.Uint32(dir[24:])        // NumberOfNames
	namesRVA := le.Uint32(dir[32:]) // AddressOfNames
	tab, err := rva(namesRVA)
	if err != nil || len(tab) < int(n)*4 {
		return nil, fmt.Errorf("export names: %v", err)
	}
	names := make([]string, 0, n)
	for i := uint32(0); i < n; i++ {
		s, err := rva(le.Uint32(tab[4*i:]))
		if err != nil {
			return nil, err
		}
		if j := bytes.IndexByte(s, 0); j >= 0 {
			s = s[:j]
		}
		names = append(names, string(s))
	}
	return names, nil
}

// stripELF drops the sections a dynamic loader never reads from a 64-bit
// little-endian ELF shared object: the DWARF .debug_* sections and the
// static symbol table (.symtab, .strtab). Everything the program headers
// map stays byte for byte at its offset; the kept unmapped sections
// (.shstrtab, .comment) and a new section header table follow it.
func stripELF(data []byte) ([]byte, error) {
	f, err := elf.NewFile(bytes.NewReader(data))
	if err != nil {
		return nil, err
	}
	if f.Class != elf.ELFCLASS64 || f.Data != elf.ELFDATA2LSB {
		return nil, errors.New("not a 64-bit little-endian ELF file")
	}
	le := binary.LittleEndian
	shoff := le.Uint64(data[0x28:])
	shentsize := int(le.Uint16(data[0x3a:]))
	shnum := int(le.Uint16(data[0x3c:]))
	shstrndx := int(le.Uint16(data[0x3e:]))
	if shentsize != 64 || shnum != len(f.Sections) {
		return nil, errors.New("unexpected section header table")
	}
	drop := func(s *elf.Section) bool {
		if s.Flags&elf.SHF_ALLOC != 0 {
			return false
		}
		return strings.HasPrefix(s.Name, ".debug") || s.Name == ".symtab" || s.Name == ".strtab"
	}
	// The mapped extent: every loadable segment and every allocated section.
	var end uint64 = 0x40
	for _, p := range f.Progs {
		end = max(end, p.Off+p.Filesz)
	}
	for _, s := range f.Sections {
		if s.Flags&elf.SHF_ALLOC != 0 && s.Type != elf.SHT_NOBITS {
			end = max(end, s.Offset+s.FileSize)
		}
	}
	out := append([]byte(nil), data[:end]...)
	newIndex := make([]int, shnum)
	var headers [][]byte
	for i, s := range f.Sections {
		if i > 0 && drop(s) {
			newIndex[i] = 0
			continue
		}
		newIndex[i] = len(headers)
		h := append([]byte(nil), data[shoff+uint64(i*64):shoff+uint64(i*64)+64]...)
		if i > 0 && s.Flags&elf.SHF_ALLOC == 0 && s.Type != elf.SHT_NOBITS {
			align := max(s.Addralign, 1)
			for uint64(len(out))%align != 0 {
				out = append(out, 0)
			}
			le.PutUint64(h[0x18:], uint64(len(out))) // sh_offset
			out = append(out, data[s.Offset:s.Offset+s.FileSize]...)
		}
		headers = append(headers, h)
	}
	for i, s := range f.Sections {
		if s.Flags&elf.SHF_ALLOC != 0 && newIndex[i] != i {
			return nil, fmt.Errorf("an unmapped section precedes %s: its index would change under .dynsym", s.Name)
		}
	}
	for _, h := range headers {
		if l := le.Uint32(h[0x28:]); int(l) < shnum { // sh_link
			le.PutUint32(h[0x28:], uint32(newIndex[l]))
		}
		if t := elf.SectionType(le.Uint32(h[4:])); t == elf.SHT_REL || t == elf.SHT_RELA {
			if info := le.Uint32(h[0x2c:]); int(info) < shnum { // sh_info: the section relocated
				le.PutUint32(h[0x2c:], uint32(newIndex[info]))
			}
		}
	}
	for len(out)%8 != 0 {
		out = append(out, 0)
	}
	le.PutUint64(out[0x28:], uint64(len(out)))
	le.PutUint16(out[0x3c:], uint16(len(headers)))
	le.PutUint16(out[0x3e:], uint16(newIndex[shstrndx]))
	for _, h := range headers {
		out = append(out, h...)
	}
	// The result must still parse and keep its dynamic symbols.
	g, err := elf.NewFile(bytes.NewReader(out))
	if err != nil {
		return nil, fmt.Errorf("stripped file: %v", err)
	}
	a, _ := f.DynamicSymbols()
	b, err := g.DynamicSymbols()
	if err != nil || len(a) != len(b) {
		return nil, fmt.Errorf("stripped file lost dynamic symbols (%d → %d, %v)", len(a), len(b), err)
	}
	return out, nil
}
