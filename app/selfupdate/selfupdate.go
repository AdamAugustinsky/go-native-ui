// Package selfupdate replaces the running app with a newer copy of itself and
// undoes the change if the new copy does not start (DECISIONS.md D61).
//
// Downloading and checking the archive against the publisher's own signature
// is the app's job. This package takes the archive from there:
//
//	func main() {
//		selfupdate.RunHelperIfAsked() // first thing: never returns in the helper
//		...
//	}
//
//	// Settings → Updates
//	if err := selfupdate.Supported(); err != nil { /* say why, and offer a download */ }
//
//	// Restart to update
//	staged, err := selfupdate.Prepare(zip) // unpacked on the app's volume and verified
//	if err == nil {
//		err = staged.InstallAndRelaunch(nil) // the helper takes over
//	}
//	if err == nil {
//		a.Quit() // the helper waits for this process to end
//	}
//
//	// After the first frame of every run
//	selfupdate.Started()
//	if r := selfupdate.LastResult(); r.Status == selfupdate.RolledBack { /* tell the user r.Reason */ }
//
// # Supported
//
// Supported says why this copy cannot replace itself: it is not running from
// an .app bundle (or is one nested inside another bundle); macOS runs it from
// a temporary copy (App Translocation, an app opened straight from the
// Downloads folder or a disk image); it is on a read-only volume such as a
// mounted disk image; this user cannot write the bundle or the folder it is
// in; it is a development bundle (gnui run's, whose identifier ends in
// ".dev"); or it is signed ad hoc or not at all, so no other build can carry
// the same signature. A package manager's copy (a Homebrew cask) is the app's
// own check.
//
// # Prepare
//
// Prepare unpacks a zip of the .app, made with
//
//	ditto -c -k --keepParent Name.app Name-1.2.zip
//
// (gnui package -zip writes one), into a new private folder on the same volume
// as the running bundle (in "selfupdate" in the app's cache folder,
// app/paths.Cache, or, when that is on another volume, a hidden folder next to
// the bundle), with /usr/bin/ditto -x -k (symbolic links, modes and extended
// attributes kept; quarantine and access control lists dropped). It first
// removes updates staged earlier that no process holds any more.
// Before anything is written it reads the archive's directory and refuses
// absolute names, ".." and other unsafe names, file types other than folders,
// files and links, links that point outside the bundle, entries reached
// through a link, more than one top-level item, and archives that would
// unpack to more than 8 GiB or 200,000 entries. On disk it checks the same
// again, refuses hard links, and clears setuid, setgid and group or other
// write bits. Then the staged bundle must have the running app's
// CFBundleIdentifier and a valid code signature (Security.framework's
// SecStaticCodeCheckValidity with strict validation, every architecture,
// nested code, and links confined to the bundle) that satisfies the running
// app's own designated requirement (SecCodeCopySelf, then
// SecCodeCopyDesignatedRequirement). For a Developer ID app that requirement
// names its identifier and its team, so only the same publisher's build of
// the same app is accepted. Every rejection matches ErrRejected.
//
// The staged bundle must also be newer than the running app: its
// CFBundleShortVersionString, then its CFBundleVersion, compared as
// period-separated decimal integers (Apple's documented form; 1.2 is 1.2.0).
// An older or equal version, or one not in that form, is refused (ErrNotNewer,
// or ErrRejected when the versions cannot be compared) unless
// Options.AllowDowngrade is set, for a rollback the user asked for. The helper
// applies the same rule again before the swap.
//
// The staged copy carries no quarantine, so Gatekeeper does not assess it
// when it first opens (as with Sparkle): the publisher's signature over the
// archive, which the app checks, and the same-signer rule stand in for that.
//
// # The helper
//
// InstallAndRelaunch copies the running bundle whole into the staging folder
// (helper/<Name>.app: an APFS clone, without extended attributes, so without
// quarantine), starts the copy's executable with a private argument, waits
// for it to take the job (an error when it does not, say because the system
// stopped it at launch), then returns so that the app can quit. That process,
// outside the bundle it replaces, is the helper. It
//
//  1. waits for the app to exit (Options.QuitTimeout, default 60 s);
//  2. checks that the bundle is still the one the app ran from, and verifies
//     the staged copy again (signature, identifier and version);
//  3. swaps the staged bundle into place in one step (renamex_np with
//     RENAME_SWAP; on a file system without it, two renames that put the old
//     bundle back if the second fails), keeping the old bundle as a backup;
//  4. launches the new version with the arguments InstallAndRelaunch was
//     given, through Launch Services (NSWorkspace openApplicationAtURL:…: a
//     new instance, activated, with its Dock icon, like any app the user
//     opens, and with no system alert: a refusal comes back as an error);
//     when Launch Services gives no process id, the helper finds the process
//     by its executable (proc_pidpath); a Go test binary opens nothing this
//     way (internal/launchguard);
//  5. waits for it to call Started (Options.HealthTimeout, default 30 s);
//  6. on success removes the backup and the staging folder; otherwise it
//     stops the new version (by its process id: SIGTERM, then SIGKILL after
//     5 s), swaps the old bundle back, records why, and launches the old
//     version, whose LastResult reports RolledBack.
//
// A failure before the swap leaves the bundle as it was, launches it again
// and is reported as Failed. The helper logs one line per step to
// selfupdate.log in the app's log folder (app/paths.Logs), through app/log.
//
// RunHelperIfAsked must be the first thing main does (after Configure, when
// the app configures the package): the helper is the app's own executable, so
// it runs the app's main, and must not open windows, take the single-instance
// lock or set up the app's log first. It is not wired into app.Run, which
// starts after the app's own setup.
//
// The helper runs from a whole copy of the bundle, not from a copy of the
// executable alone: a Developer ID signature binds the bundle's Info.plist,
// and macOS kills an executable copied out of its bundle at exec ("invalid
// Info.plist"; an ad hoc signature does not show it). The copy validates as
// the bundle does, and has the app's identifier and team, which App
// Management requires of a process that modifies the bundle.
//
// # Started and LastResult
//
// Started marks this run as healthy: the new version calls it after its first
// frame. It does a little file I/O, and nothing at all when no update is
// waiting for it; call it once per run. LastResult describes the update that
// led to this run: Updated in the new version, RolledBack (with the reason) in
// the old version started again, Failed when nothing was changed, None
// otherwise. It is read once per process, the first time either is called.
// The record is kept in the app's data folder (app/paths.Data), in
// "selfupdate".
//
// # Windows
//
// A Windows app is one executable, built by gnui package -target windows
// (D77), whose version resource names the app (AppUserModelID) and its
// version (the four-part file version: gnui.json's version, then its build).
// Supported says why it cannot replace itself: no such version resource
// (ErrNotBundled), a go run or go test binary (ErrDevBundle), installed for
// all users under Program Files or its folder not writable (ErrNotWritable),
// or no valid Authenticode signature while Options.Unsigned is not set
// (ErrNotSigned).
//
// Prepare takes the new .exe, or the zip gnui package writes, and copies the
// executable into a private folder on the running executable's volume
// ("selfupdate" in app/paths.Cache, or a hidden folder next to the
// executable): a new file, so it carries no Mark of the Web and SmartScreen
// does not stop it. The zip holds, at its top and nothing else, one .exe and
// the files its version resource declares beside it (winres.FilesKey: the
// .dll files of gnui.json's nativeLibs and their .txt licences), which are
// staged with it; a bare .exe is an update only for a build that declares
// no files. Every declared file must be there and nothing else, each DLL
// for the same machine type and, when the app is signed, signed by the same
// publisher as the executable. An unsigned app's digest (PrepareSHA256) is
// the zip's, so it covers every file. The staged executable must be for this
// machine type, name the same AppUserModelID, be newer (the file version's
// first three parts, then the fourth; Options.AllowDowngrade as on macOS)
// and, when the running app is signed, pass WinVerifyTrust with the same
// publisher: the same leaf subject and a chain to the same root, so a
// renewed certificate (Azure Trusted Signing's last three days) still
// updates. An unsigned app sets Options.Unsigned and calls PrepareSHA256
// with the digest from its manifest; Prepare refuses it with ErrNotSigned.
//
// The helper is the running executable copied into the staging folder and
// started with a private argument and two inherited handles: the app's
// process, which it waits on, and the staging folder's lock. Once the app
// has exited it checks the executable is the one that staged the update,
// verifies the staged files again (the signatures, and each file's digest
// taken at Prepare), renames the old executable and the files beside it that
// the old version declares or the update brings into the staging folder
// (old), renames the new files and then the new executable into place,
// starts it with the arguments InstallAndRelaunch was given, and waits for
// Started (Options.HealthTimeout). On a timeout, or when the new version
// exits first, it ends the new version (TerminateProcess, after checking
// that the process runs the installed executable), undoes every rename (the
// old executable and its files back together) and starts it, and LastResult
// reports RolledBack. A rename that fails during the swap undoes the ones
// before it in the same way. The
// helper's own copy cannot be deleted while it runs: the next run's Started
// or LastResult, or the next Prepare, removes it.
//
// # Other systems
//
// On Linux every call returns ErrUnsupported (errors.Is
// errors.ErrUnsupported); RunHelperIfAsked and Started do nothing, and
// LastResult reports None.
package selfupdate

import (
	"errors"
	"fmt"
	"log/slog"
	"os"
	"sync"
	"sync/atomic"
	"time"

	"gonativeui/internal/launchguard"
)

var (
	// ErrUnsupported: this system cannot update an app in place yet. It
	// matches errors.ErrUnsupported.
	ErrUnsupported = fmt.Errorf("selfupdate: %w", errors.ErrUnsupported)
	// ErrNotBundled: the app is not running from an .app bundle of its own
	// (on Windows: its executable has no version resource naming the app,
	// which gnui package writes).
	ErrNotBundled = errors.New("selfupdate: the app is not running from an application bundle")
	// ErrTranslocated: macOS runs the app from a temporary read-only copy
	// (App Translocation). Moving the app to Applications ends it.
	ErrTranslocated = errors.New("selfupdate: macOS is running the app from a temporary copy; move it to the Applications folder")
	// ErrReadOnly: the app is on a read-only volume, such as a mounted disk
	// image.
	ErrReadOnly = errors.New("selfupdate: the app is on a read-only volume")
	// ErrNotWritable: this user cannot replace the bundle or write the folder
	// it is in.
	ErrNotWritable = errors.New("selfupdate: this user cannot replace the app")
	// ErrDevBundle: a development bundle (gnui run), updated by rebuilding.
	ErrDevBundle = errors.New("selfupdate: a development bundle is not updated in place")
	// ErrNotSigned: the app is signed ad hoc or not at all, so its designated
	// requirement names this one build and no update can satisfy it (on
	// Windows: it has no valid Authenticode signature, and Options.Unsigned
	// is not set or Prepare was called instead of PrepareSHA256).
	ErrNotSigned = errors.New("selfupdate: the app has no publisher signature, so no update can be verified against it")
	// ErrRejected: Prepare (or the helper, before the swap) refused the
	// archive or the bundle in it.
	ErrRejected = errors.New("selfupdate: the update was rejected")
	// ErrNotNewer: the staged bundle is not newer than the running app (and
	// Options.AllowDowngrade is not set). It matches ErrRejected too.
	ErrNotNewer = fmt.Errorf("%w: it is not newer than the running app", ErrRejected)
)

// Options configures the package; the zero value is the release behaviour.
type Options struct {
	// Requirement, when set, replaces the running app's designated requirement
	// as the code requirement (csreq(1)'s language) a staged bundle must
	// satisfy, and turns off Supported's signature check. It is for tests,
	// whose ad-hoc signatures have designated requirements that name a single
	// build, and is honoured only in a Go test binary (an executable named
	// pkg.test, internal/launchguard). Anywhere else it is ignored, with one
	// warning in the log, and the app's own designated requirement applies.
	Requirement string
	// Unsigned lets an app without an Authenticode signature update itself on
	// Windows (a3-kb until its signing certificate exists), through
	// PrepareSHA256 only: the update must have the SHA-256 the app read from
	// a manifest it trusts (fetched over HTTPS from its own server, say).
	// That digest is then all that stands between the download and the
	// user's machine: anyone who can change the manifest can ship code.
	// Signing is the fix; a signed app ignores this option. It does nothing
	// on macOS, where a bundle must be signed by a Developer ID.
	Unsigned bool
	// AllowDowngrade lets Prepare stage, and the helper install, a bundle that
	// is not newer than the running app (a rollback the user asked for). The
	// signature rules still apply.
	AllowDowngrade bool
	// HealthTimeout is how long the helper waits for the new version to call
	// Started before it rolls back. 0 means 30 s.
	HealthTimeout time.Duration
	// QuitTimeout is how long the helper waits for the app to exit after
	// InstallAndRelaunch. 0 means 60 s.
	QuitTimeout time.Duration
	// Launcher starts the bundle at path with args and returns the process id.
	// nil means Launch Services (NSWorkspace openApplicationAtURL:…, a new
	// instance, activated). The helper calls it; tests give one that runs the
	// bundle's executable directly.
	Launcher func(bundle string, args []string) (pid int, err error)
	// Logger receives the app's side of the steps (Prepare,
	// InstallAndRelaunch, Started). nil means slog.Default().
	Logger *slog.Logger
}

var config struct {
	sync.Mutex
	o Options
}

// Configure sets the options. An app that configures the package does so
// before RunHelperIfAsked, so that its helper runs with the same options.
// Options.AllowDowngrade is the exception: it is read by Prepare, and the
// helper takes it from the staged update.
func Configure(o Options) {
	config.Lock()
	config.o = o
	config.Unlock()
}

func options() Options {
	config.Lock()
	defer config.Unlock()
	o := config.o
	if o.HealthTimeout <= 0 {
		o.HealthTimeout = 30 * time.Second
	}
	if o.QuitTimeout <= 0 {
		o.QuitTimeout = 60 * time.Second
	}
	if o.Logger == nil {
		o.Logger = slog.Default()
	}
	if o.Requirement != "" && !isTestBinary() {
		// Dropped here, where every use reads it, rather than refused by
		// Configure: an error nobody checked would leave the override in.
		if requirementWarned.CompareAndSwap(false, true) {
			o.Logger.Warn("selfupdate: Options.Requirement is ignored outside a Go test binary; the app's own designated requirement applies")
		}
		o.Requirement = ""
	}
	return o
}

// isTestBinary is launchguard.TestBinary; a test replaces it.
var isTestBinary = launchguard.TestBinary

var requirementWarned atomic.Bool

// Status is the outcome of the update that led to this run.
type Status uint8

// The outcomes.
const (
	// None: no update led to this run.
	None Status = iota
	// Updated: this run is the new version an update installed.
	Updated
	// RolledBack: the new version did not start in time, and this run is the
	// old version, put back.
	RolledBack
	// Failed: the update stopped before it changed anything (or could not be
	// undone: Reason says where the old version is).
	Failed
)

// String names the status.
func (s Status) String() string {
	switch s {
	case Updated:
		return "updated"
	case RolledBack:
		return "rolled back"
	case Failed:
		return "failed"
	}
	return "none"
}

// Result describes the update that led to this run.
type Result struct {
	Status Status    // what happened
	From   string    // the version before the update (CFBundleShortVersionString)
	To     string    // the version the update installed or tried to
	Reason string    // why it rolled back or failed; empty otherwise
	Time   time.Time // when the helper recorded it
}

// Staged is a verified update, unpacked next to the running app. Its methods
// are safe for concurrent use.
type Staged struct {
	mu      sync.Mutex
	dir     string // the staging folder
	app     string // the staged bundle, dir/new/<Name>.app
	version string
	build   string
	used    bool
	self    self
	lock    *os.File // holds the staging folder's lock until the helper has it
	anyVer  bool     // staged with Options.AllowDowngrade
	win     winStaged
}

// Path is the staged bundle.
func (s *Staged) Path() string { return s.app }

// Version is the staged bundle's CFBundleShortVersionString.
func (s *Staged) Version() string { return s.version }

// Build is the staged bundle's CFBundleVersion.
func (s *Staged) Build() string { return s.build }

// self is the running bundle.
type self struct {
	bundle  string // resolved path of the .app
	exe     string // resolved path of the running executable
	id      string // CFBundleIdentifier
	version string // CFBundleShortVersionString
	build   string // CFBundleVersion
}
