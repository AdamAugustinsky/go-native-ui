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
// Prepare does not compare versions: an older build signed by the same
// publisher passes. The app decides what is newer (Staged.Version) before it
// installs.
//
// The staged copy carries no quarantine, so Gatekeeper does not assess it
// when it first opens (as with Sparkle): the publisher's signature over the
// archive, which the app checks, and the same-signer rule stand in for that.
//
// # The helper
//
// InstallAndRelaunch copies the running executable into the staging folder
// and starts the copy with a private argument, then returns so that the app
// can quit. The copy, outside the bundle it replaces, is the helper. It
//
//  1. waits for the app to exit (Options.QuitTimeout, default 60 s);
//  2. checks that the bundle is still the one the app ran from, and verifies
//     the staged copy again;
//  3. swaps the staged bundle into place in one step (renamex_np with
//     RENAME_SWAP; on a file system without it, two renames that put the old
//     bundle back if the second fails), keeping the old bundle as a backup;
//  4. launches the new version with the arguments InstallAndRelaunch was
//     given, through Launch Services (NSWorkspace openApplicationAtURL:…: a
//     new instance, activated, with its Dock icon, like any app the user
//     opens, and with no system alert: a refusal comes back as an error);
//     when Launch Services gives no process id, the helper finds the process
//     by its executable (proc_pidpath);
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
// Because the helper is the main executable run from outside its bundle, an
// app whose entitlements need an embedded provisioning profile cannot use it
// (the system refuses to start the copy). The framework's apps need none.
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
// # Other systems
//
// On Windows and Linux every call returns ErrUnsupported (errors.Is
// errors.ErrUnsupported); RunHelperIfAsked and Started do nothing, and
// LastResult reports None.
package selfupdate

import (
	"errors"
	"fmt"
	"log/slog"
	"os"
	"sync"
	"time"
)

var (
	// ErrUnsupported: this system cannot update an app in place yet. It
	// matches errors.ErrUnsupported.
	ErrUnsupported = fmt.Errorf("selfupdate: %w", errors.ErrUnsupported)
	// ErrNotBundled: the app is not running from an .app bundle of its own.
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
	// requirement names this one build and no update can satisfy it.
	ErrNotSigned = errors.New("selfupdate: the app has no publisher signature, so no update can be verified against it")
	// ErrRejected: Prepare (or the helper, before the swap) refused the
	// archive or the bundle in it.
	ErrRejected = errors.New("selfupdate: the update was rejected")
)

// Options configures the package; the zero value is the release behaviour.
type Options struct {
	// Requirement, when set, replaces the running app's designated requirement
	// as the code requirement (csreq(1)'s language) a staged bundle must
	// satisfy, and turns off Supported's signature check. It is for tests,
	// whose ad-hoc signatures have designated requirements that name a single
	// build. Leave it empty in a release.
	Requirement string
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
	return o
}

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
