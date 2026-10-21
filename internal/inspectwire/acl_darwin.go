//go:build darwin

package inspectwire

import (
	"encoding/binary"
	"errors"
	"fmt"
	"runtime"
	"sync"
	"syscall"
	"unsafe"

	"github.com/ebitengine/purego"

	"gonativeui/internal/ffi"
)

// Extended ACLs on macOS (<sys/acl.h>, <sys/kauth.h>, getattrlist(2)), without
// cgo. An owned 0755 directory can carry an inheritable ACL that grants another
// user write access; what is created in it inherits the entry, and chmod 0600
// does not take it away. A non-inheritable one on a directory above the socket's
// can grant another user the right to delete or rename what is in it.
//
// Reading. getattrlist(2) with ATTR_CMN_EXTENDED_SECURITY returns the file's
// kauth_filesec, the kernel's own record of the ACL, and it is read here:
// libSystem's acl_get_link_np reports "no ACL" and failure alike as NULL and
// tells them apart only by errno, which belongs to a thread the runtime also
// makes libc calls on between two of ours (a stale ETIMEDOUT read as the answer
// was seen), so it can not be relied on. A layout that is not the expected one
// is an error, never "no ACL".
//
// Writing. acl_init and acl_set_link_np (an empty ACL removes the old one) are
// called through libSystem and internal/ffi. What C is given lives in aclArgs, a
// heap object reached from a package variable, next to the Frame, which is heap
// memory too; both are used only under aclMu. Go's heap does not move, and a
// goroutine's stack can, so nothing here passes C the address of a local.

const (
	aclTypeExtended = 0x100 // ACL_TYPE_EXTENDED

	attrCmnReturnedAttrs    = 0x80000000 // ATTR_CMN_RETURNED_ATTRS
	attrCmnExtendedSecurity = 0x00400000 // ATTR_CMN_EXTENDED_SECURITY
	fsoptNoFollow           = 0x1        // FSOPT_NOFOLLOW

	filesecMagic  = 0x012CC16D // KAUTH_FILESEC_MAGIC
	filesecNoACL  = 0xffffffff // KAUTH_FILESEC_NOACL
	aceKindMask   = 0xf        // KAUTH_ACE_KINDMASK
	acePermit     = 1          // KAUTH_ACE_PERMIT
	filesecHeader = 4 + 16 + 16
	aceSize       = 16 + 4 + 4 // guid, flags, rights
)

// mutatingRights are the rights of an ACL entry that change what is in a
// directory or who owns it: write/add_file, delete, append/add_subdirectory,
// delete_child, writeattr, writeextattr, writesecurity, chown.
const mutatingRights = 1<<2 | 1<<4 | 1<<5 | 1<<6 | 1<<8 | 1<<10 | 1<<12 | 1<<13

var (
	aclMu                     sync.Mutex
	aclFr                     = new(ffi.Frame)
	aclArgs                   = new(aclBlock)
	fnSetLink, fnInit, fnFree uintptr
	aclErr                    error
)

// aclBlock is the memory C reads during an ACL call.
type aclBlock struct {
	path [1024]byte // the path, NUL-terminated (PATH_MAX on macOS)
}

func init() {
	lib, err := purego.Dlopen("/usr/lib/libSystem.B.dylib", purego.RTLD_NOW|purego.RTLD_GLOBAL)
	if err != nil {
		aclErr = err
		return
	}
	for name, p := range map[string]*uintptr{"acl_set_link_np": &fnSetLink, "acl_init": &fnInit, "acl_free": &fnFree} {
		if *p, err = purego.Dlsym(lib, name); err != nil {
			aclErr = fmt.Errorf("libSystem has no %s: %w", name, err)
			return
		}
	}
}

// readACL returns the entries (flags, rights) of path's ACL, not following a
// link: none for a file without one, an error for a path that is not there or a
// record that is not what it should be.
func readACL(path string) (entries [][2]uint32, err error) {
	p, err := syscall.BytePtrFromString(path)
	if err != nil {
		return nil, err
	}
	al := struct {
		count, reserved              uint16
		common, vol, dir, file, fork uint32
	}{count: 5, common: attrCmnReturnedAttrs | attrCmnExtendedSecurity}
	buf := make([]byte, 32768)
	_, _, e := syscall.Syscall6(syscall.SYS_GETATTRLIST, uintptr(unsafe.Pointer(p)), uintptr(unsafe.Pointer(&al)),
		uintptr(unsafe.Pointer(&buf[0])), uintptr(len(buf)), fsoptNoFollow, 0)
	runtime.KeepAlive(p)
	if e != 0 {
		return nil, e
	}
	// u32 length; attribute_set_t returned (5 u32, the common attributes first);
	// then, if there is one, the attrreference_t of the filesec
	if binary.NativeEndian.Uint32(buf[4:])&attrCmnExtendedSecurity == 0 {
		return nil, nil
	}
	total := int(binary.NativeEndian.Uint32(buf))
	off := 24 + int(int32(binary.NativeEndian.Uint32(buf[24:])))
	n := int(binary.NativeEndian.Uint32(buf[28:]))
	if total > len(buf) || off < 32 || n < filesecHeader+8 || off+n > total {
		return nil, errors.New("the file's security record is not the shape expected")
	}
	d := buf[off : off+n]
	if binary.NativeEndian.Uint32(d) != filesecMagic {
		return nil, errors.New("the file's security record has an unknown magic number")
	}
	count := binary.NativeEndian.Uint32(d[filesecHeader:])
	if count == filesecNoACL || count == 0 {
		return nil, nil
	}
	if uint64(count)*aceSize > uint64(len(d)-filesecHeader-8) {
		return nil, errors.New("the file's ACL is longer than its record")
	}
	for i := 0; i < int(count); i++ {
		a := d[filesecHeader+8+i*aceSize:]
		entries = append(entries, [2]uint32{binary.NativeEndian.Uint32(a[16:]), binary.NativeEndian.Uint32(a[20:])})
	}
	return entries, nil
}

// hasACL reports whether path has an ACL with entries. A failure to read it is
// an error, never "no".
func hasACL(path string) (bool, error) {
	if aclErr != nil {
		return false, aclErr
	}
	es, err := readACL(path)
	return len(es) > 0, err
}

// aclGrantsMutation reports whether the ACL of path has an allow entry that
// grants anyone a right to change what is in it or its owner or permissions.
// Deny entries and allows of rights to read only do not count: macOS puts
// "everyone deny delete" on home folders. Anything it cannot read fails closed.
func aclGrantsMutation(path string) (bool, error) {
	if aclErr != nil {
		return false, aclErr
	}
	es, err := readACL(path)
	if err != nil {
		return false, err
	}
	for _, e := range es {
		if e[0]&aceKindMask == acePermit && e[1]&mutatingRights != 0 {
			return true, nil
		}
	}
	return false, nil
}

// stripACL removes the extended ACL of path, if it has one, by setting an empty
// one.
func stripACL(path string) error {
	has, err := hasACL(path)
	if err != nil || !has {
		return err
	}
	if len(path) >= len(aclArgs.path) {
		return fmt.Errorf("the path %q is too long", path)
	}
	aclMu.Lock()
	defer aclMu.Unlock()
	empty := aclFr.Call1(fnInit, 0)
	if empty == 0 {
		return errors.New("acl_init failed")
	}
	defer aclFr.Call1(fnFree, empty)
	n := copy(aclArgs.path[:], path)
	aclArgs.path[n] = 0
	if r := int32(aclFr.Call3(fnSetLink, uintptr(unsafe.Pointer(&aclArgs.path[0])), aclTypeExtended, empty)); r != 0 {
		return errors.New("acl_set_link_np failed")
	}
	return nil
}

// dirHasACL is hasACL for the socket's directory: any extended ACL refuses it.
func dirHasACL(dir string) (bool, error) { return hasACL(dir) }
