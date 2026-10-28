//go:build windows

package selfupdate

import (
	"fmt"
	"syscall"
	"unsafe"

	"gonativeui/internal/win32"
)

// Authenticode (D76): WinVerifyTrust checks an executable's embedded
// signature and its chain to a root the machine trusts; the signer's chain,
// read from the verification's state, gives the publisher. Two signatures
// are the same publisher when the leaf certificates have the same subject
// and their chains end at the same root, so a renewed certificate (Azure
// Trusted Signing's last a few days, through rotating intermediates) still
// updates. Revocation is not checked: it would need the network, and a
// certificate revoked after signing is out of this check's reach anyway.

// publisher identifies a signer.
type publisher struct {
	subject string // the leaf certificate's subject, X.500 form
	root    string // the chain's root certificate's subject
}

func (p *publisher) same(q *publisher) bool {
	return p != nil && q != nil && p.subject == q.subject && p.root == q.root
}

var (
	wintrust           = win32.LoadSystem("wintrust.dll")
	crypt32            = win32.LoadSystem("crypt32.dll")
	pWinVerifyTrust    = win32.Proc(wintrust, "WinVerifyTrust")
	pProvDataFromState = win32.Proc(wintrust, "WTHelperProvDataFromStateData")
	pProvSignerFromCh  = win32.Proc(wintrust, "WTHelperGetProvSignerFromChain")
	pCertGetNameString = win32.Proc(crypt32, "CertGetNameStringW")

	// WINTRUST_ACTION_GENERIC_VERIFY_V2 (softpub.h).
	actionVerifyV2 = win32.GUID{Data1: 0x00AAC56B, Data2: 0xCD44, Data3: 0x11D0, Data4: [8]byte{0x8C, 0xC2, 0x00, 0xC0, 0x4F, 0xC2, 0x95, 0xEE}}
)

// wintrustFileInfo is WINTRUST_FILE_INFO (wintrust.h), 32 bytes on 64-bit.
type wintrustFileInfo struct {
	cbStruct uint32
	_        uint32
	path     *uint16
	file     uintptr
	known    uintptr
}

// wintrustData is WINTRUST_DATA (wintrust.h), 88 bytes on 64-bit.
type wintrustData struct {
	cbStruct          uint32
	_                 uint32
	policyCallback    uintptr
	sipClientData     uintptr
	uiChoice          uint32
	revocationChecks  uint32
	unionChoice       uint32
	_                 uint32
	file              *wintrustFileInfo
	stateAction       uint32
	_                 uint32
	stateData         uintptr
	urlReference      *uint16
	provFlags         uint32
	uiContext         uint32
	signatureSettings uintptr
}

const (
	wtdUINone          = 2
	wtdRevokeNone      = 0
	wtdChoiceFile      = 1
	wtdStateVerify     = 1
	wtdStateClose      = 2
	wtdRevocationNone  = 0x10 // WTD_REVOCATION_CHECK_NONE
	wtdCacheOnlyURL    = 0x1000
	certNameRDN        = 2 // CERT_NAME_RDN_TYPE
	certX500NameStr    = 3 // CERT_X500_NAME_STR
	certNameStrReverse = 0x02000000
)

// trustCall keeps the structures of one verification on the heap.
type trustCall struct {
	fi   wintrustFileInfo
	data wintrustData
	path []uint16
	str  uint32
}

// signerOf verifies the signature of the executable at path and returns its
// publisher, or why it has none.
func signerOf(path string) (*publisher, error) {
	if pWinVerifyTrust == 0 || pProvDataFromState == 0 || pProvSignerFromCh == 0 || pCertGetNameString == 0 {
		return nil, fmt.Errorf("WinVerifyTrust is unavailable")
	}
	c := &trustCall{}
	var err error
	if c.path, err = syscall.UTF16FromString(path); err != nil {
		return nil, err
	}
	c.fi = wintrustFileInfo{cbStruct: uint32(unsafe.Sizeof(c.fi)), path: &c.path[0]}
	c.data = wintrustData{
		cbStruct: uint32(unsafe.Sizeof(c.data)), uiChoice: wtdUINone, revocationChecks: wtdRevokeNone,
		unionChoice: wtdChoiceFile, file: &c.fi, stateAction: wtdStateVerify,
		provFlags: wtdRevocationNone | wtdCacheOnlyURL,
	}
	hwnd := ^uintptr(0) // INVALID_HANDLE_VALUE: no UI
	r, _, _ := syscall.SyscallN(pWinVerifyTrust, hwnd, uintptr(unsafe.Pointer(&actionVerifyV2)), uintptr(unsafe.Pointer(&c.data)))
	defer func() {
		c.data.stateAction = wtdStateClose
		syscall.SyscallN(pWinVerifyTrust, hwnd, uintptr(unsafe.Pointer(&actionVerifyV2)), uintptr(unsafe.Pointer(&c.data)))
	}()
	if hr := uint32(r); hr != 0 {
		return nil, fmt.Errorf("%s: %s", path, trustText(hr))
	}
	prov, _, _ := syscall.SyscallN(pProvDataFromState, c.data.stateData)
	if prov == 0 {
		return nil, fmt.Errorf("%s: no provider data", path)
	}
	sgnr, _, _ := syscall.SyscallN(pProvSignerFromCh, prov, 0, 0, 0)
	if sgnr == 0 {
		return nil, fmt.Errorf("%s: no signer", path)
	}
	// CRYPT_PROVIDER_SGNR (wintrust.h): csCertChain at 12, pasCertChain at
	// 16, the signer first and the root last. Each CRYPT_PROVIDER_CERT starts
	// with its cbStruct (the array's stride) and has pCert at 8.
	n := *(*uint32)(win32.Ptr(sgnr + 12))
	chain := *(*uintptr)(win32.Ptr(sgnr + 16))
	if n == 0 || chain == 0 {
		return nil, fmt.Errorf("%s: an empty certificate chain", path)
	}
	certSize := uintptr(*(*uint32)(win32.Ptr(chain)))
	certAt := func(i uint32) uintptr { return *(*uintptr)(win32.Ptr(chain + uintptr(i)*certSize + 8)) }
	p := &publisher{subject: c.name(certAt(0)), root: c.name(certAt(n - 1))}
	if p.subject == "" {
		return nil, fmt.Errorf("%s: the signer has no subject", path)
	}
	return p, nil
}

// name is the certificate's subject in X.500 form.
func (c *trustCall) name(cert uintptr) string {
	if cert == 0 {
		return ""
	}
	c.str = certX500NameStr | certNameStrReverse
	buf := make([]uint16, 1024)
	k, _, _ := syscall.SyscallN(pCertGetNameString, cert, certNameRDN, 0, uintptr(unsafe.Pointer(&c.str)),
		uintptr(unsafe.Pointer(&buf[0])), uintptr(len(buf)))
	if k <= 1 {
		return ""
	}
	return syscall.UTF16ToString(buf[:k])
}

// trustText names WinVerifyTrust's common results.
func trustText(hr uint32) string {
	switch hr {
	case 0x800B0100:
		return "no signature (TRUST_E_NOSIGNATURE)"
	case 0x800B0109:
		return "the chain ends at a root this machine does not trust (CERT_E_UNTRUSTEDROOT)"
	case 0x80096010:
		return "the signature does not match the file (TRUST_E_BAD_DIGEST)"
	case 0x800B0101:
		return "the certificate has expired (CERT_E_EXPIRED)"
	case 0x800B0111:
		return "the publisher is explicitly distrusted (TRUST_E_EXPLICIT_DISTRUST)"
	case 0x800B010A:
		return "the certificate chain is incomplete (CERT_E_CHAINING)"
	}
	return fmt.Sprintf("WinVerifyTrust 0x%08X", hr)
}
