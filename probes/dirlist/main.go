//go:build darwin

// Throwaway phase-0 probe: macOS getattrlistbulk vs os.ReadDir (+Lstat) for
// name, type, size and mtime of every entry in a directory.
//
//	go run ./probes/dirlist -mk 100000 /tmp/dl100k   # create synthetic dir once
//	go run ./probes/dirlist /tmp/dl100k /usr/lib /System/Library/Frameworks
package main

import (
	"encoding/binary"
	"flag"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"syscall"
	"time"
	"unsafe"

	"github.com/ebitengine/purego"
)

const (
	attrBitMapCount      = 5
	attrCmnName          = 0x00000001
	attrCmnObjType       = 0x00000008
	attrCmnModTime       = 0x00000400
	attrCmnReturnedAttrs = 0x80000000
	attrFileDataLength   = 0x00000200
	fsoptPackInvalAttrs  = 0x00000008
	fsoptNoFollow        = 0x00000001
	vdir                 = 2
)

type attrlist struct {
	bitmapcount uint16
	reserved    uint16
	commonattr  uint32
	volattr     uint32
	dirattr     uint32
	fileattr    uint32
	forkattr    uint32
}

// Entry is the pointer-free record we would keep per file.
type Entry struct {
	NameOff uint32
	NameLen uint16
	Kind    uint8
	_       uint8
	Size    int64
	ModNs   int64
}

var getattrlistbulk uintptr

func listBulk(dir string, buf []byte, names []byte, out []Entry) ([]byte, []Entry, error) {
	fd, err := syscall.Open(dir, syscall.O_RDONLY|syscall.O_DIRECTORY, 0)
	if err != nil {
		return names, out, err
	}
	defer syscall.Close(fd)
	al := attrlist{bitmapcount: attrBitMapCount,
		commonattr: attrCmnReturnedAttrs | attrCmnName | attrCmnObjType | attrCmnModTime,
		fileattr:   attrFileDataLength}
	for {
		n, _, errno := purego.SyscallN(getattrlistbulk, uintptr(fd), uintptr(unsafe.Pointer(&al)),
			uintptr(unsafe.Pointer(&buf[0])), uintptr(len(buf)), fsoptPackInvalAttrs|fsoptNoFollow)
		if int(n) < 0 {
			return names, out, syscall.Errno(errno)
		}
		if n == 0 {
			return names, out, nil
		}
		p := buf
		for i := 0; i < int(n); i++ {
			length := binary.LittleEndian.Uint32(p)
			e := p[:length]
			retFile := binary.LittleEndian.Uint32(e[4+12:]) // attribute_set_t.fileattr
			q := e[4+20:]                                   // skip length + attribute_set_t
			nameOff := int32(binary.LittleEndian.Uint32(q))
			nameLen := binary.LittleEndian.Uint32(q[4:])
			nameStart := 4 + 20 + int(nameOff)
			name := e[nameStart : nameStart+int(nameLen)-1] // drop NUL
			q = q[8:]
			kind := binary.LittleEndian.Uint32(q)
			q = q[4:]
			sec := int64(binary.LittleEndian.Uint64(q))
			nsec := int64(binary.LittleEndian.Uint64(q[8:]))
			q = q[16:]
			var size int64
			if retFile&attrFileDataLength != 0 {
				size = int64(binary.LittleEndian.Uint64(q))
			}
			out = append(out, Entry{NameOff: uint32(len(names)), NameLen: uint16(len(name)),
				Kind: uint8(kind), Size: size, ModNs: sec*1e9 + nsec})
			names = append(names, name...)
			p = p[length:]
		}
	}
}

func listReadDir(dir string, names []byte, out []Entry, stat bool) ([]byte, []Entry, error) {
	des, err := os.ReadDir(dir)
	if err != nil {
		return names, out, err
	}
	for _, d := range des {
		e := Entry{NameOff: uint32(len(names)), NameLen: uint16(len(d.Name()))}
		if d.IsDir() {
			e.Kind = vdir
		}
		if stat {
			if fi, err := d.Info(); err == nil {
				e.Size, e.ModNs = fi.Size(), fi.ModTime().UnixNano()
			}
		}
		names = append(names, d.Name()...)
		out = append(out, e)
	}
	return names, out, nil
}

func median(d []time.Duration) time.Duration {
	sort.Slice(d, func(i, j int) bool { return d[i] < d[j] })
	return d[len(d)/2]
}

func main() {
	mk := flag.Int("mk", 0, "create N empty files in the target dir and exit")
	runs := flag.Int("runs", 9, "runs per method")
	flag.Parse()
	if *mk > 0 {
		dir := flag.Arg(0)
		os.MkdirAll(dir, 0o755)
		for i := 0; i < *mk; i++ {
			f, err := os.Create(filepath.Join(dir, fmt.Sprintf("file_%07d_some_typical_name.txt", i)))
			if err != nil {
				panic(err)
			}
			f.Close()
		}
		return
	}
	lib, err := purego.Dlopen("/usr/lib/libSystem.B.dylib", purego.RTLD_NOW)
	if err != nil {
		panic(err)
	}
	getattrlistbulk, _ = purego.Dlsym(lib, "getattrlistbulk")
	buf := make([]byte, 256<<10)
	for _, dir := range flag.Args() {
		var tBulk, tRD, tRDStat []time.Duration
		var nBulk, nRD int
		for r := 0; r < *runs; r++ {
			t0 := time.Now()
			_, e, err := listBulk(dir, buf, make([]byte, 0, 1<<20), make([]Entry, 0, 1024))
			if err != nil {
				fmt.Println(dir, err)
				break
			}
			tBulk = append(tBulk, time.Since(t0))
			nBulk = len(e)
			t0 = time.Now()
			_, e, _ = listReadDir(dir, make([]byte, 0, 1<<20), make([]Entry, 0, 1024), false)
			tRD = append(tRD, time.Since(t0))
			nRD = len(e)
			t0 = time.Now()
			listReadDir(dir, make([]byte, 0, 1<<20), make([]Entry, 0, 1024), true)
			tRDStat = append(tRDStat, time.Since(t0))
		}
		if len(tBulk) == 0 {
			continue
		}
		fmt.Printf("%-40s entries=%d/%d  getattrlistbulk(name,type,size,mtime)=%v  os.ReadDir(name,type)=%v  os.ReadDir+Info=%v  (median of %d, warm)\n",
			dir, nBulk, nRD, median(tBulk), median(tRD), median(tRDStat), *runs)
	}
	fmt.Printf("sizeof(Entry)=%d bytes + name bytes\n", unsafe.Sizeof(Entry{}))
}
