# Packaging

On Linux a program is a binary: the guide's section 12 has the cross-compile
commands and the release flags. On Windows it is one executable that installs
itself (`app/install`), and `gnui package -target windows` builds it from any
host with its icon, manifest and version resource (section 6). On macOS it is a **bundle**, a
`.app` with an `Info.plist`, an icon, resources and a code signature, and several
things the [services](services.md) do (notifications, login items) work only from
one. `gnui` makes it. It is pure Go, with no third-party code, and shells out
only to tools that ship with macOS (`codesign`, `security`, `plutil`, `hdiutil`,
`spctl`, `ditto`, `open`) plus `notarytool` and `stapler` from the Command Line
Tools. The schema and the signing scheme are in
[`cmd/gnui/README.md`](../../cmd/gnui/README.md); this chapter is the path through
them.

Four commands, one file:

| Command | What it does |
|---|---|
| `gnui doctor` | checks the toolchain, `gnui.json`, the `gnui-dev` identity and release identities |
| `gnui run` | a development bundle, signed with the stable `gnui-dev` identity, launched, its output streamed |
| `gnui package` | a release bundle: arm64, hardened, signed; optionally notarized, stapled and a `.dmg`; or, with `-target windows`, Windows executables and zips |
| `gnui sign` | signs a bundle that already exists, nested code first |

The blocks that are JSON are excerpts of the examples' own `gnui.json`, checked by
the test of the guide. The terminal output is real (macOS 27.2, Apple silicon, 2026-10-21)
and is not checked.

## 1. gnui.json

`gnui` reads a `gnui.json` next to the app's main package. Only `id` and `name` are
required, and the notes app has little more:

```json examples/notes/gnui.json
{
	"id": "dev.gonativeui.notes",
	"name": "Rex Notes",
	"version": "0.1.0",
	"category": "public.app-category.productivity"
}
```

The Pings example of the services chapter has the keys that a service needs: a
version, a minimum system and a login agent.

```json examples/services/gnui.json
{
	"id": "dev.gonativeui.services",
	"name": "Pings",
	"version": "0.1.0",
	"build": "1",
	"category": "public.app-category.social-networking",
	"minimumSystem": "14.0",
	"loginAgents": [{"name": "hidden", "arguments": ["--start-hidden"]}]
}
```

| Key | Meaning | Default |
|---|---|---|
| `id` | reverse-DNS bundle id | required |
| `name` | `CFBundleName`, and the `.app`'s file name | required |
| `main` | the Go package to build, relative to the file | `.` |
| `version`, `build` | `CFBundleShortVersionString` and `CFBundleVersion`: one to three dotted integers | `1.0.0`, `1` |
| `icon` | a square PNG, 1024 px recommended, made into an `.icns` | none |
| `minimumSystem` | `LSMinimumSystemVersion` | `14.0` |
| `resources`, `frameworks` | files copied into `Contents/Resources` and `Contents/Frameworks` (and signed, nested items first) | none |
| `nativeLibs` | per-platform library directories (`<dir>/<goos>-<goarch>/`, as `tools/libghostty` lays them out). macOS: the `darwin-arm64/*.dylib` files are copied into `Contents/Frameworks`, signed with the app, the licence into `Contents/Resources/Licenses`. Windows: the `windows-<arch>/*.dll` files (for that machine type) go beside the executable, signed with it, the licence as `<library>.LICENSE.txt` (§6) | none |
| `urlSchemes`, `documentTypes`, `exportedTypes` | the URLs and documents the app opens, which arrive as `EvOpenURLs` and `EvOpenFiles` | none |
| `usage` | the `NS…UsageDescription` texts that the system shows when asking for the camera, microphone, location… | none |
| `entitlements` | written to a generated entitlements plist and applied to the main bundle | none |
| `loginAgents` | bundled launch agents for `login.Agent` | none |
| `extraPlist` | merged into `Info.plist` last, and wins | none |

An unknown key is an error, and paths are relative to the file. An app that asks
for the camera, the microphone or the location needs its `usage` text, and
`gnui doctor` notes when `gnui.json` has none.

## 2. gnui doctor

Run it first, and once per Mac with `-setup-dev-cert`, which creates the
development identity (below). Then it is the answer to "why does my bundle not
sign?":

```sh
go run ./cmd/gnui doctor -setup-dev-cert      # once per Mac: create the gnui-dev identity
cd examples/services && go run ../../cmd/gnui doctor
```

```text
ok    macOS 27.2 on arm64
ok    go version go1.27.1 darwin/arm64 (cgo is always off for gnui builds)
ok    codesign: /usr/bin/codesign
…
ok    notarytool and stapler (Command Line Tools)
ok    gnui.json: Pings (dev.gonativeui.services), main .
ok    gnui-dev identity 6137CF72…06CFB4A6 in ~/Library/Keychains/gnui-dev.keychain-db
ok    gnui-dev certificate valid until 2036-10-19
ok    codesign signs and verifies with gnui-dev without a prompt
ok    the gnui-dev key trusts /usr/bin/codesign only (partitions apple-tool:,apple:,codesign:)
warn  no Developer ID Application identity in the keychains: gnui package signs ad-hoc and cannot notarize or build a signed dmg (needed only for distribution)

0 failed, 1 warnings
```

It signs something with `gnui-dev` as a real round trip, so a green "ok" means the
signature works. The exit status is `0` ok, `1` a failure, `2` a bad command line,
and `3` a step that needs your Apple identity (a Developer ID certificate,
`notarytool` credentials). The warning about the Developer ID is the one every
machine without a paid Apple developer account has; it matters only when you want
to distribute.

## 3. gnui run

`gnui run` builds a **development bundle** at `.gnui/dev/<Name>.app`, signs it with
`gnui-dev`, and launches it through LaunchServices (`open -n -W`), so that the Dock,
activation and the system's privacy checks treat it as an app. Its output is
streamed to the terminal, and Ctrl-C quits it.

```sh
cd examples/services && go run ../../cmd/gnui run
go run ../../cmd/gnui run -env KEY=VALUE -- -some-flag    # an environment and the app's own arguments
go run ../../cmd/gnui run -detach                                # launch and return; the logs are in .gnui/dev
```

- **The bundle id gets `.dev`:** `dev.gonativeui.services.dev`. A development bundle
  and the release app are separate apps to macOS, with separate grants and Dock
  tiles, on purpose.
- **It is a development build:** symbols are kept, there is no hardened runtime (so
  that a nested library loads without a Team ID) and no secure timestamp (no network
  needed). The entitlements of `gnui.json` are applied, so a sandboxed app is tested
  sandboxed. The debug overlay of the framework is on.
- **The identity is the point.** An ad-hoc signature's requirement is the hash of
  the code, so every rebuild is a different app to macOS and the permission, the
  Keychain item and the notification grant are asked again. `gnui-dev` is a
  self-signed code-signing certificate in a keychain of its own, and a bundle signed
  with it has the requirement `identifier "<bundle id>" and certificate root =
  H"<certificate hash>"`, which does not change when the code does.
  (`gnui run -adhoc` is the opposite, for when you want a fresh grant.)
- **The certificate does not have to be trusted.** `codesign` signs with it, the
  signature verifies strictly, and the requirement is satisfied by every rebuild.

`go test ./cmd/gnui` runs `gnui run` twice with different binaries and checks that
the designated requirement is the same, and that an ad-hoc build's is not. That a
notification grant given to the first build is then still there for the second is
the consequence of that, and has **not** been run by hand here.

**Read this about the key:** it signs without a password, because build automation
needs that, so any process of your user on this Mac can sign code that satisfies the
requirement of your `.dev` bundles, and so inherits whatever privacy grants they
were given. Keep the keychain on the build machine, never copy it (to a test Mac,
say: copy the *bundle* instead), and grant development bundles only what
development needs. The README of `gnui` has the rest.

## 4. gnui package

`gnui package` is the release build:

```sh
cd examples/services && go run ../../cmd/gnui package -o dist
```

```text
packaging Pings 0.1.0 (dev.gonativeui.services) -> dist/Pings.app
building darwin/arm64
signing with an ad-hoc signature (no Developer ID Application identity found) (hardened runtime)
verified: codesign --verify --deep --strict
designated requirement: cdhash H"ee97c33f…"
built dist/Pings.app

note: signed with an ad-hoc signature (no Developer ID Application identity found), not a Developer ID: Gatekeeper rejects this bundle on other Macs.
skipped: notarize and staple: needs a Developer ID Application identity and notarytool credentials
```

What is in the result:

- **An arm64 executable.** macOS means Apple silicon (D55): there is no universal
  binary, and a build for darwin/amd64 stops with an error that names the decision.
  The binary is built with `-trimpath -ldflags "-s -w"`.
- **A release build.** `-tags gnui_release` leaves out the debug overlay, its Debug
  menu, its chords and the inspector. A plain `go build` and `gnui run` stay
  development builds.
- **`Info.plist`,** the `.icns` made from `icon`, the resources, the nested
  frameworks signed inside out, the generated entitlements, and the launch agents of
  `loginAgents` in `Contents/Library/LaunchAgents`.
- **The hardened runtime,** on by default (`-no-hardened` turns it off), and a
  secure timestamp for a real identity. `codesign -dv` says `flags=0x10002(adhoc,runtime)` for
  the bundle above: `runtime` is the hardened runtime.
- **A verified signature.** `gnui package` runs `codesign --verify --deep --strict`
  and fails if it does not pass.

The identity is, in this order: `-identity` (a name, a part of one that matches
exactly one identity, a SHA-1, or `-` for ad-hoc), `$GNUI_SIGN_IDENTITY`, a valid
"Developer ID Application" identity from your keychains, and ad-hoc. `-dev-cert`
signs with `gnui-dev` instead, for a bundle that stays on your Mac. The report says
what was skipped and why. The other flags:

```text
-c gnui.json   -o dir (default dist)   -tags list   -ldflags flags
-notarize      -notary-profile name    -dmg         -zip         -app bundle
-notary-keychain path
```

`-notary-keychain` (or `$GNUI_NOTARY_KEYCHAIN`) names the keychain the profile was
stored in with `store-credentials --keychain`, for a dedicated release keychain;
without it notarytool reads the login keychain.

`-dmg` also builds a disk image, and `-zip` the archive that `app/selfupdate`
installs (`ditto -c -k --keepParent`, after stapling).

`-app X.app` signs a bundle that was built earlier from the same `gnui.json`,
in place, then notarizes and packages it as above, without building. Use it
when the release identity lives on a Mac other than the one that builds:
copy the bundle, the `gnui.json` and a `gnui` binary there, and run
`gnui package -c gnui.json -app Zap.app -identity "Developer ID Application: …" -notarize -notary-profile <profile> -dmg -zip`.
The bundle's identifier and version must match the `gnui.json`'s. The
entitlements come from the `gnui.json`, so a `gnui-dev` bundle loses
`disable-library-validation` when a Developer ID signs it again. Sign from a
Terminal on that Mac: over SSH its login keychain is locked, and `codesign`
fails with `errSecInternalComponent`.

## 5. Notarisation is untested

> **Notarisation, stapling and the signed disk image have not been run end to end.**

They need a "Developer ID Application" certificate and `notarytool` credentials, and
the machine this was written on has neither: `gnui doctor` says so in its one
warning, and the output above is an ad-hoc build. What that means exactly:

- **Tested:** the arm64 binary, the bundle's structure, the `Info.plist`, the
  `.icns`, the login-agent plist, `codesign --verify --deep --strict`, the
  entitlements and the hardened-runtime flag in the signature, an unsigned `.dmg`, and the stable requirement of `gnui run`, by `go test ./cmd/gnui`
  with a temporary keychain.
- **Not tested:** `-notarize` (the submission to Apple, the wait, the verdict),
  stapling the ticket, `spctl`'s verdict on a notarized bundle, and a `.dmg` signed
  and notarized with a Developer ID. The code paths exist and stop before building
  with exit status `3` when the identity is missing; whether they pass with a real
  identity is to be found out the first time someone has one.

The steps to get one, which `gnui package` prints when it needs them:

```sh
# 1. create a "Developer ID Application" certificate (Xcode: Settings > Accounts, or developer.apple.com) and install it
# 2. store the notarization credentials once
xcrun notarytool store-credentials <profile> --apple-id <you> --team-id <TEAMID>
# 3. package, notarize, staple and build the disk image
go run ../../cmd/gnui package -identity "Developer ID Application: Name (TEAMID)" -notarize -notary-profile <profile> -dmg
```

## 6. Windows executables

`gnui package -target` takes `darwin/arm64` (the default), `windows/amd64`,
`windows/arm64`, or `windows` for both, comma-separated. The Windows builds need
no Windows machine and no Windows SDK: `gnui` writes the resource object itself
(D77).

```sh
cd examples/services && go run ../../cmd/gnui package -target windows
```

```text
packaging Pings 0.1.0 (dev.gonativeui.services) for windows/amd64 -> dist/windows-amd64/Pings.exe
zip: dist/Pings-0.1.0-windows-amd64.zip
packaging Pings 0.1.0 (dev.gonativeui.services) for windows/arm64 -> dist/windows-arm64/Pings.exe
zip: dist/Pings-0.1.0-windows-arm64.zip

note: the Windows executables are unsigned: SmartScreen warns on a first download, and app/selfupdate needs Options.Unsigned with a verified digest. Configure windows.sign (or -win-sign) to sign them.
```

Each executable is a release build (`-trimpath`, `-s -w`, `gnui_release`, as on
macOS) linked with `-H windowsgui`, so it opens no console window, and with these
resources:

- **The icon:** `icon`, downscaled to 16, 20, 24, 32, 40, 48, 64 and 256 px (DIBs
  below 256, a PNG at 256), as the executable's first icon group.
- **The manifest:** Per-Monitor v2 DPI awareness, Windows 10 and 11 as supported
  systems, UTF-8 as the process code page, long paths, Common Controls 6, and
  `asInvoker`, so it never asks for elevation.
- **`VERSIONINFO`:** the file version is `version` with `build` as its fourth part
  (`0.1.0` and `1` make `0.1.0.1`; each part at most 65535), `ProductVersion` is
  `version`, `ProductName` the display name, `OriginalFilename` the executable's
  name, and `AppUserModelID` the `id` (or `windows.aumid`). `app/install` and
  `app/selfupdate` read these: the installed copy keeps `OriginalFilename` even if
  the browser renamed the download, an empty `install.Options` field takes its
  value from here, and an update must carry the same `AppUserModelID` and a
  higher file version.

The `.syso` is written into the main package's folder for the build and removed
after it (`go build -overlay` does not carry `.syso` files); a main package with a
`.syso` of its own is refused, since its resources would collide.

**Native libraries.** Each `nativeLibs` directory gives the `.dll` files of its
`windows-<arch>` folder (at least one; `gnui` checks each is for the target's
machine type) and, when the directory has a `LICENSE`, that file as
`<library>.LICENSE.txt` (`ghostty-vt.dll` brings `ghostty-vt.LICENSE.txt`). They
are copied beside the executable in `dist/windows-<arch>/`, signed with the same
command as the executable, and named in its version resource as `GnuiFiles`
(`ghostty-vt.dll|ghostty-vt.LICENSE.txt`). That list is the contract with the
runtime: `app/install` copies exactly these files with the executable and refuses
to install without them, `app/selfupdate` requires exactly these beside the new
executable in an update (each DLL for the same machine and, for a signed app,
signed by the same publisher) and swaps and rolls them back with it, and
`term` loads `ghostty-vt.dll` from beside the executable first. The list lives in
the executable rather than in a file beside it, so it is covered by the
executable's signature, and a stray DLL in the download folder is never installed.

The zip holds the executable and those files at its top, the form
`selfupdate.Prepare` takes besides a bare `.exe` (which updates only an app that
declares no files). The optional `windows` object of `gnui.json` (here the packaging
test's) holds `publisher`, `aumid`, `description`, `copyright` and `sign`:

```json cmd/gnui/testdata/winprobe/gnui.json
"windows": {"publisher": "Go Native UI tests", "copyright": "none"}
```

A release configuration with Azure Trusted Signing through jsign would add:

```text
"sign": "jsign --storetype TRUSTEDSIGNING --keystore weu.codesigning.azure.net --storepass ${AZURE_TOKEN} --alias account/profile --tsaurl http://timestamp.acs.microsoft.com --tsmode RFC3161 {file}"
```

**Signing** runs a command of your choice on each executable: `windows.sign`,
`-win-sign`, or `$GNUI_WINDOWS_SIGN`. The command is split into words (quotes
group, backslashes are plain); `{file}` becomes the executable's path and `$NAME`
or `${NAME}` an environment variable, so secrets stay out of `gnui.json`. Without
one the executables are unsigned, the default until the Windows certificate
exists. The tools that fit:

- **jsign** (any host, Java): `--storetype TRUSTEDSIGNING` signs with Azure
  Trusted Signing, the token from `az account get-access-token --resource
  https://codesigning.azure.net`.
- **signtool** (Windows): `signtool sign /fd SHA256 /tr http://timestamp.acs.microsoft.com /td SHA256 /dlib Azure.CodeSigning.Dlib.dll /dmdf metadata.json {file}`
  with the Trusted Signing client, or `/f cert.pfx /p ${PFX_PASS}` with a file.
- **osslsigncode** (macOS, Linux) writes a new file: give it `-in {file} -out {file}.signed`
  and `gnui` moves the signed copy over the executable.

Always ask for an RFC 3161 timestamp: Trusted Signing's certificates last three
days, and a timestamped signature stays valid after its certificate expires.
After the command `gnui` checks that the executable now has a certificate table;
whether the signature is valid is Windows' verdict (`signtool verify /pa`, or
`Get-AuthenticodeSignature`). A signed app updates only to builds of the same
publisher (`app/selfupdate`, D76).

**Tested:** the resources, the machine type, the GUI subsystem and the
certificate table read back from both executables on every OS, by `go test
./cmd/gnui` with a stand-in signing tool; on the Windows runners the build for the
runner's architecture is run, and reports Per-Monitor v2 awareness, code page
65001, and the resources found with `FindResourceW`. **Not tested:** a real
signature (no Windows certificate yet), and the icon's look in Explorer.

## 7. What gnui does not do

- **No Mac App Store** packaging, no provisioning profiles, and no sandbox container
  setup. The entitlements that you list are written as they are.
- **No Windows installer package** (MSI, MSIX): the executable installs itself per
  user (`app/install`, D76). **No Linux packaging:** `go build` (the guide's
  section 12).
- **No Intel Macs** (D55).

To check a bundle yourself, without `gnui`:

```sh
codesign --verify --deep --strict --verbose=2 dist/Pings.app
codesign -dv dist/Pings.app          # the flags, the identifier, the signature kind
spctl --assess --type execute dist/Pings.app    # Gatekeeper's verdict
```
