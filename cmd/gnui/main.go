// Command gnui builds, signs, runs and checks Go Native UI applications on
// macOS. It reads a gnui.json next to the application's main package and uses
// only tools that ship with macOS (codesign, security, plutil, hdiutil, spctl)
// plus notarytool and stapler from the Command Line Tools.
//
//	gnui package [-c gnui.json] [-o dir] [-identity id] [-dev-cert]
//	             [-no-hardened] [-notarize -notary-profile name] [-dmg] [-zip] [-tags list]
//	gnui run     [-c gnui.json] [-env KEY=VAL] [-detach] [-adhoc] [-tags list] [-- app arguments]
//	gnui sign    [-identity id|-dev|-adhoc] [-hardened] [-entitlements file] path.app
//	gnui doctor  [-c gnui.json] [-setup-dev-cert]
//
// package builds a release bundle: an arm64 binary (macOS means Apple
// silicon, DECISIONS.md D55), Info.plist, an .icns icon, resources, nested
// frameworks signed inside out, generated entitlements and the hardened
// runtime; notarizing, stapling and the signed disk image need a Developer ID
// identity and say so when they do not have one.
//
// run builds a development bundle (bundle id plus ".dev") at
// .gnui/dev/<Name>.app, signs it with the stable gnui-dev identity, launches
// it through LaunchServices and streams its output.
//
// doctor checks the toolchain and the gnui-dev identity; -setup-dev-cert
// creates the identity.
//
// See cmd/gnui/README.md for the gnui.json schema and the signing scheme.
package main

import (
	"flag"
	"fmt"
	"io"
	"os"
)

const usage = `gnui - build, sign, run and check Go Native UI applications (macOS)

usage:
  gnui package [flags]    build a release .app (and optionally notarize, staple, .dmg)
  gnui run [flags] [-- args]
                          build a development bundle, sign it with gnui-dev, launch it
  gnui sign [flags] app   sign an existing bundle (the tool apps) with gnui-dev or another identity
  gnui doctor [flags]     check the toolchain and the signing identities

Every command reads -c gnui.json (default: ./gnui.json) where it needs one.
Run "gnui <command> -h" for the flags.
`

func main() {
	os.Exit(run(os.Args[1:], os.Stdout, os.Stderr))
}

// run executes one gnui command line and returns the process exit status:
// 0 success, 1 failure, 2 a usage error, 3 a step that needs an Apple
// identity the user has not provided.
func run(args []string, stdout, stderr io.Writer) int {
	if len(args) == 0 {
		fmt.Fprint(stderr, usage)
		return 2
	}
	var err error
	switch args[0] {
	case "package":
		err = cmdPackage(args[1:], stdout, stderr)
	case "run":
		err = cmdRun(args[1:], stdout, stderr)
	case "sign":
		err = cmdSign(args[1:], stdout, stderr)
	case "doctor":
		err = cmdDoctor(args[1:], stdout, stderr)
	case "-h", "-help", "--help", "help":
		fmt.Fprint(stdout, usage)
		return 0
	default:
		fmt.Fprintf(stderr, "gnui: unknown command %q\n\n%s", args[0], usage)
		return 2
	}
	switch e := err.(type) {
	case nil:
		return 0
	case usageError:
		fmt.Fprintf(stderr, "gnui %s: %v\n", args[0], e.error)
		return 2
	case needIdentityError:
		fmt.Fprintf(stderr, "gnui %s: %v\n", args[0], e.error)
		return 3
	default:
		if err == flag.ErrHelp {
			return 0
		}
		fmt.Fprintf(stderr, "gnui %s: %v\n", args[0], err)
		return 1
	}
}

// usageError marks a bad command line.
type usageError struct{ error }

// needIdentityError marks a step that cannot run without the user's Apple
// identity (Developer ID certificate, notarytool credentials).
type needIdentityError struct{ error }

// newFlagSet returns a FlagSet that reports errors through the returned
// error rather than exiting.
func newFlagSet(name string, stderr io.Writer) *flag.FlagSet {
	fs := flag.NewFlagSet("gnui "+name, flag.ContinueOnError)
	fs.SetOutput(stderr)
	return fs
}

// parseFlags parses args and wraps failures as usageError.
func parseFlags(fs *flag.FlagSet, args []string) error {
	if err := fs.Parse(args); err != nil {
		if err == flag.ErrHelp {
			return err
		}
		return usageError{err}
	}
	return nil
}
