# gnui

Builds, signs, runs and checks Go Native UI applications on macOS (D47). Pure
Go, no cgo, no third-party code; it shells out only to tools that ship with
macOS (`codesign`, `security`, `plutil`, `hdiutil`, `spctl`, `ditto`, `open`)
plus `notarytool` and `stapler` from the Command Line Tools.

```sh
go run ./cmd/gnui doctor -setup-dev-cert      # once per Mac: create the gnui-dev identity
cd cmd/gallery && go run ../gnui run          # build, sign with gnui-dev, launch, stream the logs
cd cmd/gallery && go run ../gnui package      # release bundle in ./dist
```

## Commands

| command | what it does |
|---|---|
| `gnui package` | release `.app` from `gnui.json`: built with `-tags gnui_release` (no debug overlay Debug menu, chords or inspector; a plain `go build` and `gnui run` stay development builds), universal binary, `Info.plist`, `.icns`, resources, nested frameworks signed inside out, generated entitlements, hardened runtime; optional notarize + staple and `.dmg` |
| `gnui run` | development bundle `.gnui/dev/<Name>.app` (bundle id + `.dev`), signed with `gnui-dev`, launched through LaunchServices; output streamed |
| `gnui sign app.app` | signs an existing bundle, nested code first (the tool apps) |
| `gnui doctor` | checks the toolchain, `gnui.json`, the `gnui-dev` identity (trust, validity, a real signing round trip) and release identities |

Exit status: `0` ok, `1` failure, `2` bad command line, `3` a step that needs
the user's Apple identity (Developer ID certificate, notarytool credentials).

### `gnui package`

```
-c gnui.json          configuration (default ./gnui.json)
-o dir                output directory (default dist) -> dist/<Name>.app, dist/<Name>-<version>.dmg
-arch universal|arm64|amd64   default universal: darwin/arm64 and darwin/amd64 built with
                      -trimpath -ldflags "-s -w", merged into one Mach-O in Go
-identity id          signing identity or "-" for ad-hoc. A name, a part of one that matches
                      exactly one identity, or a SHA-1, resolved against
                      `security find-identity -v -p codesigning`; whether it can be notarized
                      is decided from the resolved certificate's common name ("Developer ID
                      Application: …"), however it was selected.
                      Default: $GNUI_SIGN_IDENTITY, else a valid "Developer ID Application"
                      identity from the keychains, else ad-hoc (and the report says so)
-dev-cert             sign with gnui-dev instead
-no-hardened          no hardened runtime (default: on)
-no-timestamp         no secure timestamp (default: on for a real identity)
-notarize             notarize with xcrun notarytool, wait, staple, check with spctl
-notary-profile name  notarytool keychain profile ($GNUI_NOTARY_PROFILE)
-dmg                  also build a .dmg (signed, and notarized and stapled with -notarize,
                      when the identity is a Developer ID)
-tags list            go build -tags
-ldflags flags        extra go build -ldflags
```

Without a Developer ID identity the bundle is still built, signed ad-hoc and
verified (`codesign --verify --deep --strict`), and the output lists what was
skipped (notarize and staple, the dmg's signature and notarization) with the
three steps to get an identity. `-notarize` without one stops *before*
building, with exit status 3.

What it does not do: Mac App Store packaging, provisioning profiles and
sandbox container setup (the entitlements you list are written as they are).

### `gnui run`

```
-c gnui.json     -o dir (default .gnui/dev next to gnui.json)
-env KEY=VALUE   environment of the launched app (repeatable)
-detach          launch and return (logs: <dir>/<Name>.out.log, <Name>.err.log)
-adhoc           sign ad-hoc (grants are void after every rebuild)
-tags, -ldflags  go build flags
-- args          passed to the app
```

The bundle is built for this Mac's architecture only, with symbols kept, no
hardened runtime (a nested dylib loads without a Team ID) and no timestamp (no
network). Entitlements from `gnui.json` are applied, so a sandboxed app is
tested sandboxed. The app is started with `open -n -W` (LaunchServices: Dock,
activation and TCC attribute it as an app); Ctrl-C quits it.

### `gnui sign`

`gnui sign [-identity id | -adhoc] [-hardened] [-entitlements file] path.app`.
Default identity: `gnui-dev`. `tools/resign-tools.sh` builds GnuiShot,
BounceRec and AppKitRef and signs them this way.

## `gnui.json`

Unknown keys are an error. Paths are relative to the file. Only `id` and `name`
are required.

```json
{
	"id": "dev.example.notes",
	"name": "Notes",
	"main": ".",
	"executable": "Notes",
	"version": "1.4.0",
	"build": "87",
	"category": "public.app-category.productivity",
	"minimumSystem": "14.0",
	"icon": "icon.png",
	"resources": [{"from": "assets", "to": "assets"}],
	"frameworks": ["vendor/libfoo.dylib", "vendor/Bar.framework"],
	"urlSchemes": ["notes"],
	"documentTypes": [{"name": "Note", "role": "Editor", "extensions": ["note"], "utis": ["dev.example.note"], "rank": "Owner"}],
	"exportedTypes": [{"identifier": "dev.example.note", "description": "Note document", "conformsTo": ["public.data"], "extensions": ["note"]}],
	"usage": {"NSMicrophoneUsageDescription": "Notes records voice memos."},
	"entitlements": {"com.apple.security.cs.disable-library-validation": true},
	"loginAgents": [{"name": "helper", "arguments": ["--start-hidden"]}],
	"extraPlist": {"LSUIElement": false}
}
```

| key | meaning | default |
|---|---|---|
| `id` | reverse-DNS bundle id | required |
| `name` | `CFBundleName`, the `.app` file name | required |
| `main` | Go package to build, relative to `gnui.json` | `.` |
| `executable` | file under `Contents/MacOS` | `name` without spaces |
| `version`, `build` | `CFBundleShortVersionString`, `CFBundleVersion`: one to three dotted integers | `1.0.0`, `1` |
| `category` | `LSApplicationCategoryType` (`public.app-category.…`) | none |
| `minimumSystem` | `LSMinimumSystemVersion` | `14.0` |
| `icon` | square PNG (1024 px recommended) → `AppIcon.icns` with 16…512@2x entries; sizes above the source are left out | none |
| `resources` | files/directories copied to `Contents/Resources`; `to` is a relative path inside it with no `.` or `..` components (default: the base name of `from`, so `from: "."` needs a `to`). Symbolic links in a copied directory are kept only if they are relative and stay inside it (`Versions/Current -> A` does, `/etc` or `../x` is an error), and a destination is refused if any existing component of it is a link | none |
| `frameworks` | `.framework` directories and `.dylib` files copied to `Contents/Frameworks` (same link rules) and signed, nested items first. Each nested item keeps the entitlements and identifier it already has (`--preserve-metadata=entitlements,identifier`); `entitlements` below are written to the main bundle only | none |
| `urlSchemes` | `CFBundleURLTypes` | none |
| `documentTypes` | `CFBundleDocumentTypes`; role Editor/Viewer/Shell/None, rank Owner/Default/Alternate/None. A `utis` entry that is not `public.…` or `com.apple.…` must be declared below, or macOS does not know it (`gnui doctor` warns) | none |
| `exportedTypes` | `UTExportedTypeDeclarations`: type identifiers this app owns, each with `identifier`, `description`, `conformsTo` (default `["public.data"]`) and the `extensions` and `mimeTypes` that map to it. Identifiers under `public.` and `com.apple.` cannot be exported. LaunchServices learns a new type when the app is launched | none |
| `importedTypes` | `UTImportedTypeDeclarations`: same fields, for types owned by someone else that you rely on | none |
| `usage` | `NS…UsageDescription` keys and their text | none |
| `entitlements` | written to a generated entitlements plist and applied to the main bundle | none |
| `loginAgents` | bundled launch agents (below) | none |
| `extraPlist` | merged into `Info.plist` last, wins | none |

### Login agents

Each `loginAgents` entry writes
`Contents/Library/LaunchAgents/<label>.plist` for `SMAppService` (`app/login`).
The label is **`<bundle id in use>.<name>`**, the program is the bundle's own
executable (`BundleProgram`), `ProgramArguments` is the executable name plus
`arguments`, and `AssociatedBundleIdentifiers` is the bundle id in use. So the
development bundle `dev.example.notes.dev` registers
`dev.example.notes.dev.helper`, and never collides with the installed app's
`dev.example.notes.helper`.

## The development identity (`gnui-dev`)

An ad-hoc signature's designated requirement is `cdhash H"…"`, the hash of
the code, so every rebuild is a different app to macOS and privacy (TCC),
Keychain and notification grants are asked for again. `gnui-dev` is a
self-signed code-signing certificate; a bundle signed with it has the
requirement `identifier "<bundle id>" and certificate root = H"<certificate
SHA-1>"`, which does not change when the code does.

* **Where.** A dedicated keychain, `~/Library/Keychains/gnui-dev.keychain-db`
  (mode 600; `$GNUI_DEV_KEYCHAIN` overrides the path, and the tests use a
  temporary one). It has an empty password, no auto-lock, and is **not** added to the keychain search
  list: gnui passes `--keychain` to `codesign`. The login keychain and the
  system trust settings are never touched. (macOS 15.7, checked on a GitHub
  runner, still wants the keychain on the search list before `codesign`
  finds the key: signing then fails with "The specified item could not be
  found in the keychain" and gnui says so. `gnui doctor -search-list` adds it
  to the user search list, the one preference gnui ever changes, and only on
  request. macOS 27.2, checked here, does not need it.)
* **How.** `gnui doctor -setup-dev-cert` generates an RSA-2048 key and a
  10-year certificate (`CN=gnui-dev`, key usage digitalSignature, extended key
  usage codeSigning, not a CA) in Go (`crypto/x509`), imports them with
  `security import -T /usr/bin/codesign` and `set-key-partition-list -S
  apple-tool:,apple:,codesign:`. The access list (`-T`) is what restricts the
  key: `codesign` is the only program that uses it without a prompt, and
  `security export` is not (a `-T /usr/bin/security` would allow it; an older
  gnui had that). The partition list is what `codesign` needs to work: measured
  on macOS 27.2, `apple-tool:,codesign:` alone works for a keychain in `/tmp`
  but fails with `errSecInternalComponent` in `~/Library/Keychains`, which needs
  `apple:` as well, and writes the public certificate to `~/Library/Keychains/gnui-dev.pem`. It
  does nothing if the identity exists. No secret is in the repository; the key
  exists only in that keychain.
* **Trust.** The certificate does not have to be trusted. `security
  find-identity -v` lists it as `CSSMERR_TP_NOT_TRUSTED`, but `codesign` signs
  with it, the signature verifies strictly, the app launches, and the
  requirement above (an exact certificate hash, not `anchor trusted`) is
  satisfied by every rebuild; `gnui doctor` checks this with a real signing
  round trip. Trusting it changes nothing for those, and costs an account
  password at the keyboard: `security add-trusted-cert -p codeSign -r trustRoot
  -k ~/Library/Keychains/gnui-dev.keychain-db ~/Library/Keychains/gnui-dev.pem`.
* **Security: read this.** The key signs without a password, because build
  automation needs that, so **any process of the same user on the build machine
  can sign code with it** by running `codesign`. Code signed that way satisfies
  the designated requirement of your `.dev` bundles (same identifier, same
  certificate), so it inherits whatever privacy grants those bundles have been
  given on a machine that trusts the certificate. Consequences:
  * The key lives **only on the build machine**. Never copy the keychain, the
    key or a PKCS#12 export to the test Mac (or any Mac where grants are
    given): sign there with a bundle you copy, never with the key.
  * Grant `.dev` builds only what development needs (not Full Disk Access, not
    Accessibility, not Screen Recording unless you are testing exactly that).
  * Anyone who gets the keychain file can use the key too (empty password), so
    it is kept at mode 600 and never put in a repository, backup you share or
    synced folder. `gnui doctor` checks the access list (`/usr/bin/codesign`
    only) and says this again.
  * Recreate the identity (delete the keychain, run setup) if it may have leaked;
    grants given to the old certificate then no longer apply, which is the point.
  An older gnui also trusted `/usr/bin/security`; `gnui doctor` fails on such a
  key and prints the two commands that replace it.
* **Moving it.** A grant belongs to a certificate hash. To get the same grants
  on another Mac, sign here and copy the **bundle**; do not create a second
  identity there. To reset, delete the keychain (`security delete-keychain`)
  and run setup again, after which every grant is asked for once more.
* **Expiry.** `gnui doctor` warns 30 days before the certificate expires.

Grants for a bundle signed with it are keyed on the bundle id, so a `.dev`
bundle and the release app are separate apps to macOS (separate grants, separate
Dock tiles). That is deliberate.

## Tests

`go test ./cmd/gnui` packages `testdata/hello` and the real gallery as signed
universal bundles, checks the structure, `Info.plist`, `.icns`, login-agent
plist, `codesign --verify --deep --strict`, the entitlements and the hardened
runtime flag in the signature, runs `gnui run` twice with different binaries
and checks that the designated requirement is the same (and that an ad-hoc
build's is not), and builds a dmg. The signing tests use a temporary keychain.
