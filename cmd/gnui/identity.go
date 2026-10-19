package main

import (
	"bytes"
	"crypto/rand"
	"crypto/rsa"
	"crypto/sha1"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/pem"
	"fmt"
	"math/big"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strings"
	"time"
)

// The development identity. A self-signed code-signing certificate named
// devIdentityName lives in a dedicated keychain, so every development bundle
// is signed by the same certificate and its designated requirement
// ("identifier … and certificate leaf = H"<sha1>"") is the same after every
// rebuild. macOS keys privacy (TCC), Keychain and notification grants on that
// requirement; an ad-hoc signature's requirement is the code hash, which
// changes with every build.
//
// The keychain has an empty password and is not added to the user's keychain
// search list: gnui passes it to codesign explicitly. It holds one key that
// signs development builds on this machine and nothing else; the login
// keychain and the system trust settings are never touched.
const devIdentityName = "gnui-dev"

// devKeychainPath returns the dedicated keychain: $GNUI_DEV_KEYCHAIN, or
// ~/Library/Keychains/gnui-dev.keychain-db.
func devKeychainPath() string {
	if p := os.Getenv("GNUI_DEV_KEYCHAIN"); p != "" {
		return p
	}
	home, err := os.UserHomeDir()
	if err != nil {
		home = "."
	}
	return filepath.Join(home, "Library", "Keychains", devIdentityName+".keychain-db")
}

// devCertPath is where the public certificate is exported, next to the keychain.
func devCertPath() string {
	return strings.TrimSuffix(devKeychainPath(), ".keychain-db") + ".pem"
}

// identityInfo describes one code-signing identity as `security find-identity` lists it.
type identityInfo struct {
	Hash   string // SHA-1 of the certificate, upper-case hex
	Name   string
	Valid  bool   // listed by find-identity -v: chain evaluates and is trusted
	Status string // text in parentheses for a non-valid identity, e.g. CSSMERR_TP_NOT_TRUSTED
}

var reIdentity = regexp.MustCompile(`^\s*\d+\)\s+([0-9A-Fa-f]{40})\s+"(.*)"(?:\s+\((.*)\))?\s*$`)

// listIdentities lists the code-signing identities in keychain (the default
// search list when keychain is empty). Identities that are not trusted are
// included with Valid false.
func listIdentities(keychain string) ([]identityInfo, error) {
	args := []string{"find-identity", "-p", "codesigning"}
	if keychain != "" {
		args = append(args, keychain)
	}
	all, err := runOut("security", args...)
	if err != nil {
		return nil, err
	}
	argsV := append([]string{"find-identity", "-v", "-p", "codesigning"}, args[3:]...)
	validOut, err := runOut("security", argsV...)
	if err != nil {
		return nil, err
	}
	valid := map[string]bool{}
	for _, l := range strings.Split(validOut, "\n") {
		if m := reIdentity.FindStringSubmatch(l); m != nil {
			valid[strings.ToUpper(m[1])] = true
		}
	}
	var out []identityInfo
	seen := map[string]bool{}
	for _, l := range strings.Split(all, "\n") {
		m := reIdentity.FindStringSubmatch(l)
		if m == nil {
			continue
		}
		h := strings.ToUpper(m[1])
		if seen[h] {
			continue
		}
		seen[h] = true
		out = append(out, identityInfo{Hash: h, Name: m[2], Valid: valid[h], Status: m[3]})
	}
	return out, nil
}

// findDevIdentity returns the gnui-dev identity of the dedicated keychain, or
// nil when there is none (no keychain, or no such identity in it).
func findDevIdentity() (*identityInfo, error) {
	kc := devKeychainPath()
	if _, err := os.Stat(kc); err != nil {
		return nil, nil
	}
	ids, err := listIdentities(kc)
	if err != nil {
		return nil, err
	}
	for i := range ids {
		if ids[i].Name == devIdentityName {
			return &ids[i], nil
		}
	}
	return nil, nil
}

// unlockDevKeychain unlocks the dedicated keychain (it relocks on reboot
// unless its settings say otherwise) so codesign never asks for a password.
func unlockDevKeychain() error {
	_, err := runOut("security", "unlock-keychain", "-p", "", devKeychainPath())
	return err
}

// setupDevIdentity creates the dedicated keychain and the gnui-dev identity
// when they do not exist. It reports whether it created anything. Nothing here
// needs a password or a dialog.
func setupDevIdentity(logf func(string, ...any)) (created bool, err error) {
	kc := devKeychainPath()
	if id, err := findDevIdentity(); err != nil {
		return false, err
	} else if id != nil {
		return false, unlockDevKeychain()
	}
	if err := os.MkdirAll(filepath.Dir(kc), 0o755); err != nil {
		return false, err
	}
	if _, err := os.Stat(kc); err != nil {
		logf("creating keychain %s", kc)
		if _, err := runOut("security", "create-keychain", "-p", "", kc); err != nil {
			return false, err
		}
	}
	// No auto-lock timeout and no lock on sleep.
	if _, err := runOut("security", "set-keychain-settings", kc); err != nil {
		return false, err
	}
	if err := unlockDevKeychain(); err != nil {
		return false, err
	}

	logf("generating a self-signed RSA-2048 code-signing certificate %q (valid 10 years)", devIdentityName)
	keyPEM, certPEM, err := newDevCertificate()
	if err != nil {
		return false, err
	}
	tmp, err := os.MkdirTemp("", "gnui-dev-*")
	if err != nil {
		return false, err
	}
	defer os.RemoveAll(tmp)
	keyFile := filepath.Join(tmp, "key.pem")
	certFile := filepath.Join(tmp, "cert.pem")
	if err := os.WriteFile(keyFile, keyPEM, 0o600); err != nil {
		return false, err
	}
	if err := os.WriteFile(certFile, certPEM, 0o600); err != nil {
		return false, err
	}
	logf("importing the key and certificate into the dedicated keychain")
	if _, err := runOut("security", "import", keyFile, "-k", kc, "-f", "openssl", "-t", "priv", "-T", "/usr/bin/codesign", "-T", "/usr/bin/security"); err != nil {
		return false, err
	}
	if _, err := runOut("security", "import", certFile, "-k", kc, "-f", "pemseq", "-t", "cert"); err != nil {
		return false, err
	}
	// Let Apple's tools use the key without a prompt.
	if _, err := runOut("security", "set-key-partition-list", "-S", "apple-tool:,apple:,codesign:", "-s", "-k", "", kc); err != nil {
		return false, err
	}
	if err := os.WriteFile(devCertPath(), certPEM, 0o644); err != nil {
		return false, err
	}
	if id, err := findDevIdentity(); err != nil || id == nil {
		return false, fmt.Errorf("the identity was imported but security does not list it (%v)", err)
	}
	return true, nil
}

// newDevCertificate returns a PEM PKCS#1 private key (the form `security import -f openssl` reads) and its self-signed
// code-signing certificate.
func newDevCertificate() (keyPEM, certPEM []byte, err error) {
	key, err := rsa.GenerateKey(rand.Reader, 2048)
	if err != nil {
		return nil, nil, err
	}
	serial, err := rand.Int(rand.Reader, new(big.Int).Lsh(big.NewInt(1), 127))
	if err != nil {
		return nil, nil, err
	}
	now := time.Now()
	tpl := &x509.Certificate{
		SerialNumber:          serial,
		Subject:               pkix.Name{CommonName: devIdentityName, Organization: []string{"Go Native UI development"}},
		NotBefore:             now.Add(-time.Hour),
		NotAfter:              now.AddDate(10, 0, 0),
		KeyUsage:              x509.KeyUsageDigitalSignature,
		ExtKeyUsage:           []x509.ExtKeyUsage{x509.ExtKeyUsageCodeSigning},
		BasicConstraintsValid: true,
		IsCA:                  false,
	}
	der, err := x509.CreateCertificate(rand.Reader, tpl, tpl, &key.PublicKey, key)
	if err != nil {
		return nil, nil, err
	}
	keyPEM = pem.EncodeToMemory(&pem.Block{Type: "RSA PRIVATE KEY", Bytes: x509.MarshalPKCS1PrivateKey(key)})
	certPEM = pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: der})
	return keyPEM, certPEM, nil
}

// certInfo parses a PEM certificate file and returns its SHA-1 and validity.
func certInfo(pemFile string) (sha string, notBefore, notAfter time.Time, err error) {
	data, err := os.ReadFile(pemFile)
	if err != nil {
		return "", time.Time{}, time.Time{}, err
	}
	blk, _ := pem.Decode(data)
	if blk == nil {
		return "", time.Time{}, time.Time{}, fmt.Errorf("%s: no PEM certificate", pemFile)
	}
	crt, err := x509.ParseCertificate(blk.Bytes)
	if err != nil {
		return "", time.Time{}, time.Time{}, err
	}
	sum := sha1.Sum(crt.Raw)
	return strings.ToUpper(fmt.Sprintf("%x", sum[:])), crt.NotBefore, crt.NotAfter, nil
}

// trustCommand is the one command the user runs, at their keyboard, to mark
// the gnui-dev certificate trusted for code signing. It changes the user's
// trust settings, so macOS asks for the account password in a dialog.
func trustCommand() string {
	return fmt.Sprintf("security add-trusted-cert -p codeSign -r trustRoot -k %q %q", devKeychainPath(), devCertPath())
}

// signSpec says how to sign: with which identity, from which keychain.
type signSpec struct {
	Identity string // name or SHA-1, or "-" for ad-hoc
	Keychain string // optional
	Label    string // for messages
}

func (s signSpec) adhoc() bool { return s.Identity == "-" }

// devSignSpec returns the signSpec for the development identity, creating
// nothing: it fails with the exact next step when the identity is missing.
func devSignSpec() (signSpec, error) {
	id, err := findDevIdentity()
	if err != nil {
		return signSpec{}, err
	}
	if id == nil {
		return signSpec{}, fmt.Errorf("the %s signing identity does not exist yet: run `gnui doctor -setup-dev-cert`", devIdentityName)
	}
	if err := unlockDevKeychain(); err != nil {
		return signSpec{}, err
	}
	return signSpec{Identity: id.Hash, Keychain: devKeychainPath(), Label: devIdentityName}, nil
}

// runOut runs a tool and returns its combined output; on failure the error
// carries that output.
func runOut(name string, args ...string) (string, error) {
	var buf bytes.Buffer
	cmd := exec.Command(name, args...)
	cmd.Stdout = &buf
	cmd.Stderr = &buf
	err := cmd.Run()
	out := buf.String()
	if err != nil {
		msg := strings.TrimSpace(out)
		if msg == "" {
			msg = err.Error()
		}
		return out, fmt.Errorf("%s %s: %s", name, strings.Join(args, " "), msg)
	}
	return out, nil
}
