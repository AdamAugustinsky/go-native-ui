//go:build windows

package install

import (
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
	"sync"
	"syscall"
	"time"
	"unsafe"

	"gonativeui/internal/launchguard"
	"gonativeui/internal/win32"
	"gonativeui/internal/winproc"
	"gonativeui/internal/winreg"
	"gonativeui/internal/winres"
)

// Windows (D76): per-user registration under HKEY_CURRENT_USER, the
// executable under %LOCALAPPDATA%\Programs\<Name>, the shortcut in the
// user's Start menu.

const (
	uninstallRoot = `Software\Microsoft\Windows\CurrentVersion\Uninstall\`
	aumidRoot     = `Software\Classes\AppUserModelId\`
	classesRoot   = `Software\Classes\`

	// Private values of the uninstall entry: the installed executable (what
	// Installed looks for) and the schemes Install registered.
	valExe     = "GnuiExecutable"
	valSchemes = "GnuiSchemes"

	// cleanupFlag starts the uninstall helper: os.Args is exactly {exe,
	// cleanupFlag, folder, handle of the app's process}.
	cleanupFlag = "-gnui-install-cleanup"
)

// init runs the uninstall helper when this process is one: it is the app's
// own executable copied to %TEMP%, and must do nothing of the app's.
func init() {
	if len(os.Args) == 4 && os.Args[1] == cleanupFlag {
		os.Exit(runCleanup(os.Args[2], os.Args[3]))
	}
}

func osSupported() error { return nil }

// self is the running executable's path.
func self() (string, error) {
	exe, err := os.Executable()
	if err != nil {
		return "", err
	}
	return filepath.Clean(exe), nil
}

// devBuild reports a go run or go test binary, which Install refuses.
func devBuild(exe string) bool {
	if launchguard.TestBinary() {
		return true
	}
	tmp := strings.ToLower(filepath.Clean(os.TempDir())) + `\`
	l := strings.ToLower(exe)
	return strings.HasPrefix(l, tmp) && strings.Contains(l, `\go-build`)
}

// versionOf reads the version resource of the executable at path, if any.
func versionOf(path string) (winres.Version, bool) {
	f, err := os.Open(path)
	if err != nil {
		return winres.Version{}, false
	}
	defer f.Close()
	st, err := f.Stat()
	if err != nil {
		return winres.Version{}, false
	}
	pf, err := winres.Read(f, st.Size())
	if err != nil {
		return winres.Version{}, false
	}
	r, ok := winres.Find(pf.Resources, winres.TypeVersion, 1)
	if !ok {
		return winres.Version{}, false
	}
	v, err := winres.ParseVersion(r.Data)
	return v, err == nil
}

var own struct {
	once sync.Once
	v    winres.Version
	ok   bool
}

func ownVersion() (winres.Version, bool) {
	own.once.Do(func() {
		if exe, err := self(); err == nil {
			own.v, own.ok = versionOf(exe)
		}
	})
	return own.v, own.ok
}

// osDefaults fills ID, Name, Publisher and Version from the executable's
// version resource (gnui package writes AppUserModelID, ProductName,
// CompanyName and ProductVersion).
func osDefaults(o Options) Options {
	v, ok := ownVersion()
	if !ok {
		return o
	}
	fill := func(dst *string, key string) {
		if *dst == "" {
			*dst = v.String(key)
		}
	}
	fill(&o.ID, "AppUserModelID")
	fill(&o.Name, "ProductName")
	fill(&o.Publisher, "CompanyName")
	fill(&o.Version, "ProductVersion")
	return o
}

// exeName is the installed executable's file name: the one gnui package gave
// it (OriginalFilename), so a download renamed "App (1).exe" installs as
// App.exe, or else the running one's.
func exeName(exe string) string {
	if v, ok := ownVersion(); ok {
		if n := v.String("OriginalFilename"); n != "" && !strings.ContainsAny(n, `\/:`) && strings.HasSuffix(strings.ToLower(n), ".exe") {
			return n
		}
	}
	return filepath.Base(exe)
}

// checkWindows refuses what cannot be a registry key or a folder name.
func checkWindows(o Options) error {
	if strings.ContainsAny(o.ID, `\/ `) || len(o.ID) > 128 {
		return fmt.Errorf("%w: ID %q", ErrOptions, o.ID)
	}
	if strings.ContainsAny(o.Name, `\/:*?"<>|`) || strings.TrimSpace(o.Name) != o.Name || strings.HasSuffix(o.Name, ".") {
		return fmt.Errorf("%w: Name %q is not a folder name", ErrOptions, o.Name)
	}
	return nil
}

func installDir(o Options) (string, error) {
	if o.Dir != "" {
		if !filepath.IsAbs(o.Dir) {
			return "", fmt.Errorf("%w: Dir %q is not absolute", ErrOptions, o.Dir)
		}
		return filepath.Clean(o.Dir), nil
	}
	local := os.Getenv("LOCALAPPDATA")
	if local == "" || !filepath.IsAbs(local) {
		return "", errors.New("install: %LOCALAPPDATA% is not set")
	}
	return filepath.Join(local, "Programs", o.Name), nil
}

func samePath(a, b string) bool { return strings.EqualFold(filepath.Clean(a), filepath.Clean(b)) }

// inside reports whether p is in the folder dir.
func inside(dir, p string) bool {
	return strings.HasPrefix(strings.ToLower(filepath.Clean(p)), strings.ToLower(filepath.Clean(dir))+`\`)
}

func osInstalled() (string, bool) {
	exe, err := self()
	if err != nil {
		return "", false
	}
	k, err := winreg.Open(strings.TrimSuffix(uninstallRoot, `\`))
	if err != nil {
		return "", false
	}
	defer k.Close()
	subs, _ := k.SubKeys()
	name := strings.ToLower(exeName(exe))
	other := ""
	for _, s := range subs {
		p, err := winreg.GetString(uninstallRoot+s, valExe)
		if err != nil || p == "" {
			continue
		}
		if samePath(p, exe) {
			return exe, true
		}
		if other == "" && strings.ToLower(filepath.Base(p)) == name {
			if _, err := os.Stat(p); err == nil {
				other = p
			}
		}
	}
	return other, other != ""
}

func osInstall(o Options) error {
	if err := checkWindows(o); err != nil {
		return err
	}
	exe, err := self()
	if err != nil {
		return err
	}
	if devBuild(exe) {
		return ErrDevBuild
	}
	dir, err := installDir(o)
	if err != nil {
		return err
	}
	dst := filepath.Join(dir, exeName(exe))
	if !samePath(exe, dst) {
		if err := os.MkdirAll(dir, 0o755); err != nil {
			return fmt.Errorf("install: %w", err)
		}
		if err := copyExe(exe, dst); err != nil {
			return fmt.Errorf("install: copying the executable: %w", err)
		}
	}
	if err := register(o, dir, dst); err != nil {
		return err
	}
	if !o.NoShortcut {
		if err := writeShortcut(shortcutPath(o.Name), dst, dir, o.ID); err != nil {
			return fmt.Errorf("install: the Start-menu shortcut: %w", err)
		}
	}
	if o.Relaunch && !samePath(exe, dst) {
		p, err := winproc.Start(dst, os.Args[1:], dir)
		if err != nil {
			return fmt.Errorf("install: starting the installed copy: %w", err)
		}
		p.Release()
	}
	return nil
}

// copyExe copies the executable to dst through a new file. An installed copy
// that is running is renamed aside first (Windows lets a running image be
// renamed, not replaced); copies set aside earlier are removed when they no
// longer run.
func copyExe(src, dst string) error {
	dir := filepath.Dir(dst)
	if olds, _ := filepath.Glob(filepath.Join(dir, "*.gnui-old-*")); len(olds) > 0 {
		for _, p := range olds {
			os.Remove(p)
		}
	}
	tmp := dst + ".gnui-new-" + strconv.Itoa(os.Getpid())
	in, err := os.Open(src)
	if err != nil {
		return err
	}
	defer in.Close()
	out, err := os.OpenFile(tmp, os.O_CREATE|os.O_TRUNC|os.O_WRONLY, 0o755)
	if err != nil {
		return err
	}
	_, err = io.Copy(out, in)
	if cerr := out.Close(); err == nil {
		err = cerr
	}
	if err != nil {
		os.Remove(tmp)
		return err
	}
	if err := os.Rename(tmp, dst); err == nil {
		return nil
	}
	aside := dst + ".gnui-old-" + strconv.FormatInt(time.Now().UnixNano(), 36)
	if err := winproc.Rename(dst, aside); err != nil {
		os.Remove(tmp)
		return err
	}
	if err := winproc.Rename(tmp, dst); err != nil {
		winproc.Rename(aside, dst)
		os.Remove(tmp)
		return err
	}
	os.Remove(aside) // fails while it runs; the next install removes it
	return nil
}

// register writes the AUMID key, the schemes and the uninstall entry.
func register(o Options, dir, exe string) error {
	k, err := winreg.Create(aumidRoot + o.ID)
	if err != nil {
		return fmt.Errorf("install: the AUMID key: %w", err)
	}
	err = k.SetString("DisplayName", o.Name)
	k.Close()
	if err != nil {
		return fmt.Errorf("install: the AUMID key: %w", err)
	}
	for _, s := range o.Schemes {
		if err := registerScheme(s, o.Name, exe); err != nil {
			return err
		}
	}
	k, err = winreg.Create(uninstallRoot + o.ID)
	if err != nil {
		return fmt.Errorf("install: the uninstall entry: %w", err)
	}
	defer k.Close()
	size := uint32(0)
	if st, err := os.Stat(exe); err == nil {
		size = uint32((st.Size() + 1023) / 1024)
	}
	cmd := `"` + exe + `" ` + UninstallArg
	for _, v := range []struct{ k, v string }{
		{"DisplayName", o.Name}, {"DisplayVersion", o.Version}, {"Publisher", o.Publisher},
		{"DisplayIcon", exe + ",0"}, {"InstallLocation", dir},
		{"UninstallString", cmd}, {"QuietUninstallString", cmd},
		{"InstallDate", time.Now().Format("20060102")},
		{valExe, exe}, {valSchemes, strings.Join(o.Schemes, ";")},
	} {
		if v.v == "" {
			continue
		}
		if err := k.SetString(v.k, v.v); err != nil {
			return fmt.Errorf("install: the uninstall entry: %w", err)
		}
	}
	for name, v := range map[string]uint32{"NoModify": 1, "NoRepair": 1, "EstimatedSize": size} {
		if err := k.SetDWORD(name, v); err != nil {
			return fmt.Errorf("install: the uninstall entry: %w", err)
		}
	}
	return nil
}

func osRegisterScheme(scheme string) error {
	exe, err := self()
	if err != nil {
		return err
	}
	name := scheme
	if v, ok := ownVersion(); ok && v.String("ProductName") != "" {
		name = v.String("ProductName")
	}
	return registerScheme(scheme, name, exe)
}

// registerScheme points HKCU\Software\Classes\<scheme> at exe: the shell
// starts it with the URL as its one argument.
func registerScheme(scheme, name, exe string) error {
	key := classesRoot + scheme
	for _, v := range []struct{ path, name, value string }{
		{key, "", "URL:" + name},
		{key, "URL Protocol", ""},
		{key + `\DefaultIcon`, "", `"` + exe + `",0`},
		{key + `\shell\open\command`, "", `"` + exe + `" "%1"`},
	} {
		k, err := winreg.Create(v.path)
		if err != nil {
			return fmt.Errorf("install: scheme %s: %w", scheme, err)
		}
		err = k.SetString(v.name, v.value)
		k.Close()
		if err != nil {
			return fmt.Errorf("install: scheme %s: %w", scheme, err)
		}
	}
	return nil
}

// shortcutPath is the Start-menu shortcut of the app called name.
func shortcutPath(name string) string {
	return filepath.Join(os.Getenv("APPDATA"), `Microsoft\Windows\Start Menu\Programs`, name+".lnk")
}

func osUninstall(o Options) error {
	if err := checkWindows(o); err != nil {
		return err
	}
	key := uninstallRoot + o.ID
	installed, _ := winreg.GetString(key, valExe)
	dir, _ := winreg.GetString(key, "InstallLocation")
	if o.Name == "" {
		o.Name, _ = winreg.GetString(key, "DisplayName")
	}
	if dir == "" && o.Name != "" && installed == "" {
		dir, _ = installDir(o)
	}
	schemes := append([]string(nil), o.Schemes...)
	if s, err := winreg.GetString(key, valSchemes); err == nil && s != "" {
		schemes = append(schemes, strings.Split(s, ";")...)
	}
	var errs []error
	if o.Name != "" {
		if err := os.Remove(shortcutPath(o.Name)); err != nil && !os.IsNotExist(err) {
			errs = append(errs, err)
		}
	}
	for _, s := range schemes {
		if !validScheme(s) {
			continue
		}
		// Only a scheme that still opens this app: another app may own it now.
		cmd, err := winreg.GetString(classesRoot+s+`\shell\open\command`, "")
		if err != nil || dir == "" || !strings.Contains(strings.ToLower(cmd), strings.ToLower(dir)+`\`) {
			continue
		}
		if err := winreg.DeleteTree(classesRoot + s); err != nil {
			errs = append(errs, err)
		}
	}
	for _, k := range []string{aumidRoot + o.ID, key} {
		if err := winreg.DeleteTree(k); err != nil {
			errs = append(errs, err)
		}
	}
	if err := errors.Join(errs...); err != nil {
		return fmt.Errorf("install: uninstall: %w", err)
	}
	// The folder, when it is the one Install made: it holds the executable
	// the entry named.
	if dir == "" || installed == "" || !samePath(filepath.Dir(installed), dir) || len(strings.Split(filepath.Clean(dir), `\`)) < 3 {
		return nil
	}
	exe, err := self()
	if err != nil {
		return err
	}
	if !inside(dir, exe) {
		if err := winproc.RemoveAll(dir); err != nil {
			return fmt.Errorf("install: uninstall: %w", err)
		}
		return nil
	}
	return startCleanup(exe, dir)
}

// startCleanup copies the running executable to a new folder in %TEMP% and
// starts it as the helper that removes dir once this process has exited.
func startCleanup(exe, dir string) error {
	tmp, err := os.MkdirTemp("", "gnui-uninstall-")
	if err != nil {
		return fmt.Errorf("install: uninstall: %w", err)
	}
	helper := filepath.Join(tmp, filepath.Base(exe))
	if err := copyFile(exe, helper); err != nil {
		os.RemoveAll(tmp)
		return fmt.Errorf("install: uninstall: %w", err)
	}
	h, err := winproc.SelfHandle()
	if err != nil {
		os.RemoveAll(tmp)
		return fmt.Errorf("install: uninstall: %w", err)
	}
	defer syscall.CloseHandle(h)
	p, err := winproc.Start(helper, []string{cleanupFlag, dir, strconv.FormatUint(uint64(h), 10)}, tmp, h)
	if err != nil {
		os.RemoveAll(tmp)
		return fmt.Errorf("install: uninstall: starting the helper: %w", err)
	}
	p.Release()
	return nil
}

func copyFile(src, dst string) error {
	in, err := os.Open(src)
	if err != nil {
		return err
	}
	defer in.Close()
	out, err := os.OpenFile(dst, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0o755)
	if err != nil {
		return err
	}
	_, err = io.Copy(out, in)
	if cerr := out.Close(); err == nil {
		err = cerr
	}
	return err
}

// runCleanup is the uninstall helper: it waits for the app to exit, removes
// its folder, and has cmd.exe remove the helper's own folder once it has
// exited too. It only acts from a gnui-uninstall- folder in %TEMP%, on a
// folder that holds an executable of its own name.
func runCleanup(dir, handle string) int {
	exe, err := self()
	if err != nil {
		return 2
	}
	tmp := filepath.Dir(exe)
	if !strings.HasPrefix(filepath.Base(tmp), "gnui-uninstall-") || !filepath.IsAbs(dir) {
		return 2
	}
	if _, err := os.Stat(filepath.Join(dir, filepath.Base(exe))); err != nil {
		return 2
	}
	h, err := winproc.ParseHandle(handle)
	if err != nil {
		return 2
	}
	if !winproc.Wait(h, 2*time.Minute) {
		return 1
	}
	syscall.CloseHandle(h)
	status := 0
	if winproc.RemoveAll(dir) != nil {
		status = 1
	}
	// cmd.exe waits about two seconds (ping) for this process to end, then
	// removes the helper's folder.
	cmd := exec.Command(filepath.Join(os.Getenv("SystemRoot"), "System32", "cmd.exe"))
	cmd.SysProcAttr = &syscall.SysProcAttr{
		HideWindow:    true,
		CreationFlags: 0x08000000 | 0x00000200, // CREATE_NO_WINDOW | CREATE_NEW_PROCESS_GROUP
		CmdLine:       `/d /c ping -n 3 127.0.0.1 >nul & rd /s /q "` + tmp + `"`,
	}
	cmd.Dir = os.Getenv("SystemRoot")
	if cmd.Start() == nil {
		cmd.Process.Release()
	}
	return status
}

// ---- The shortcut: IShellLinkW with System.AppUserModel.ID ----

var (
	clsidShellLink   = win32.GUID{Data1: 0x00021401, Data4: [8]byte{0xC0, 0, 0, 0, 0, 0, 0, 0x46}}                                                 // shobjidl_core.h CLSID_ShellLink
	iidShellLinkW    = win32.GUID{Data1: 0x000214F9, Data4: [8]byte{0xC0, 0, 0, 0, 0, 0, 0, 0x46}}                                                 // IShellLinkW
	iidPropertyStore = win32.GUID{Data1: 0x886D8EEB, Data2: 0x8CF2, Data3: 0x4446, Data4: [8]byte{0x8D, 0x02, 0xCD, 0xBA, 0x1D, 0xBD, 0xCF, 0x99}} // propsys.h
	iidPersistFile   = win32.GUID{Data1: 0x0000010B, Data4: [8]byte{0xC0, 0, 0, 0, 0, 0, 0, 0x46}}                                                 // objidl.h
	// propkey.h PKEY_AppUserModel_ID: {9F4C2855-9F79-4B39-A8D0-E1D42DE1D5F3}, 5.
	pkeyAppUserModelID = propertyKey{fmtid: win32.GUID{Data1: 0x9F4C2855, Data2: 0x9F79, Data3: 0x4B39, Data4: [8]byte{0xA8, 0xD0, 0xE1, 0xD4, 0x2D, 0xE1, 0xD5, 0xF3}}, pid: 5}
)

// Vtable slots: shobjidl_core.h IShellLinkW (SetWorkingDirectory 9, SetPath
// 20), propsys.h IPropertyStore (SetValue 6, Commit 7), objidl.h
// IPersistFile (Save 6).
const (
	vtLinkSetWorkingDir = 9
	vtLinkSetPath       = 20
	vtStoreSetValue     = 6
	vtStoreCommit       = 7
	vtFileSave          = 6
	vtLPWSTR            = 31 // VT_LPWSTR
	clsctxInproc        = 1
)

type propertyKey struct {
	fmtid win32.GUID
	pid   uint32
}

// propVariant is a PROPVARIANT holding a VT_LPWSTR (24 bytes on 64-bit).
type propVariant struct {
	vt       uint16
	reserved [3]uint16
	ptr      uintptr
	pad      uintptr
}

var (
	ole32         = win32.LoadSystem("ole32.dll")
	pCoInit       = win32.Proc(ole32, "CoInitializeEx")
	pCoUninit     = win32.Proc(ole32, "CoUninitialize")
	pCoCreate     = win32.Proc(ole32, "CoCreateInstance")
	errNoShortcut = errors.New("COM is unavailable")
)

// shortcutCall is the memory the COM calls point into: the heap, as
// internal/com's convention asks.
type shortcutCall struct {
	link, store, file uintptr
	target, wd, id    []uint16
	lnk               []uint16
	pv                propVariant
	key               propertyKey
}

// writeShortcut saves a shortcut at path to target, run in dir, carrying the
// AUMID id, on a thread of its own in a single-threaded apartment.
func writeShortcut(path, target, dir, id string) error {
	if pCoInit == 0 || pCoCreate == 0 {
		return errNoShortcut
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return err
	}
	c := &shortcutCall{key: pkeyAppUserModelID}
	var err error
	if c.target, err = syscall.UTF16FromString(target); err != nil {
		return err
	}
	if c.wd, err = syscall.UTF16FromString(dir); err != nil {
		return err
	}
	if c.id, err = syscall.UTF16FromString(id); err != nil {
		return err
	}
	if c.lnk, err = syscall.UTF16FromString(path); err != nil {
		return err
	}
	done := make(chan error, 1)
	go func() {
		runtime.LockOSThread()
		defer runtime.UnlockOSThread()
		hr, _, _ := syscall.SyscallN(pCoInit, 0, 0x2|0x4) // COINIT_APARTMENTTHREADED | COINIT_DISABLE_OLE1DDE
		if int32(hr) >= 0 {
			defer syscall.SyscallN(pCoUninit)
		}
		done <- c.save()
	}()
	return <-done
}

func (c *shortcutCall) save() error {
	step := func(what string, hr uintptr) error {
		if int32(hr) < 0 {
			return fmt.Errorf("%s: HRESULT 0x%08X", what, uint32(hr))
		}
		return nil
	}
	if err := step("CoCreateInstance(ShellLink)", win32.Call(pCoCreate, uintptr(unsafe.Pointer(&clsidShellLink)), 0, clsctxInproc,
		uintptr(unsafe.Pointer(&iidShellLinkW)), uintptr(unsafe.Pointer(&c.link)))); err != nil {
		return err
	}
	defer win32.Release(c.link)
	if err := step("SetPath", win32.Com(c.link, vtLinkSetPath, uintptr(unsafe.Pointer(&c.target[0])))); err != nil {
		return err
	}
	if err := step("SetWorkingDirectory", win32.Com(c.link, vtLinkSetWorkingDir, uintptr(unsafe.Pointer(&c.wd[0])))); err != nil {
		return err
	}
	if err := step("IPropertyStore", uintptr(uint32(win32.QueryInterface(c.link, &iidPropertyStore, &c.store)))); err != nil {
		return err
	}
	defer win32.Release(c.store)
	c.pv = propVariant{vt: vtLPWSTR, ptr: uintptr(unsafe.Pointer(&c.id[0]))}
	if err := step("SetValue(AppUserModel.ID)", win32.Com(c.store, vtStoreSetValue, uintptr(unsafe.Pointer(&c.key)), uintptr(unsafe.Pointer(&c.pv)))); err != nil {
		return err
	}
	if err := step("Commit", win32.Com(c.store, vtStoreCommit)); err != nil {
		return err
	}
	if err := step("IPersistFile", uintptr(uint32(win32.QueryInterface(c.link, &iidPersistFile, &c.file)))); err != nil {
		return err
	}
	defer win32.Release(c.file)
	err := step("Save", win32.Com(c.file, vtFileSave, uintptr(unsafe.Pointer(&c.lnk[0])), 1))
	runtime.KeepAlive(c)
	return err
}
