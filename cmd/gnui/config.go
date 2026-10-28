package main

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"path"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
)

// Config is the decoded gnui.json: everything gnui needs to turn a Go main
// package into a macOS application bundle. Unknown keys are an error, so a
// misspelt option is never silently ignored. Relative paths are relative to
// the directory holding gnui.json.
type Config struct {
	// ID is the bundle identifier, reverse-DNS ("dev.example.notes").
	ID string `json:"id"`
	// Name is the application's name: CFBundleName, the .app's file name.
	Name string `json:"name"`
	// DisplayName, when set, is the name the system shows in Name's place
	// (CFBundleName and CFBundleDisplayName: the menu bar, the About panel,
	// permission prompts), while the files keep Name: a renamed app whose
	// .app, update archive and disk image keep the names installed copies
	// and package managers look for.
	DisplayName string `json:"displayName"`
	// BundleName, when set, is the .app's file name in Name's place (the
	// Dock's tooltip, Finder and app switchers show it), while the update
	// archive and the disk image keep Name: installed copies' updaters look
	// for those, and accept any .app inside.
	BundleName string `json:"bundleName"`
	// Main is the Go package to build, relative to gnui.json (default ".").
	Main string `json:"main"`
	// Executable is the file name under Contents/MacOS (default: Name without spaces).
	Executable string `json:"executable"`
	// Version is CFBundleShortVersionString, one to three dotted integers (default "1.0.0").
	Version string `json:"version"`
	// Build is CFBundleVersion, one to three dotted integers (default "1").
	Build string `json:"build"`
	// Category is LSApplicationCategoryType ("public.app-category.productivity").
	Category string `json:"category"`
	// MinimumSystem is LSMinimumSystemVersion (default "14.0").
	MinimumSystem string `json:"minimumSystem"`
	// Icon is a square PNG, 1024 px recommended, turned into AppIcon.icns.
	Icon string `json:"icon"`
	// Resources are files or directories copied into Contents/Resources.
	Resources []Resource `json:"resources"`
	// Frameworks are .framework directories and .dylib files copied into
	// Contents/Frameworks and signed, nested items first.
	Frameworks []string `json:"frameworks"`
	// NativeLibs are directories of runtime-loaded native libraries laid
	// out per platform as <dir>/<goos>-<goarch>/ (tools/libghostty's cache,
	// D2): the darwin-arm64 .dylib files are copied into
	// Contents/Frameworks and signed with the app, and <dir>/LICENSE, when
	// present, into Contents/Resources/Licenses/<library>.txt.
	NativeLibs []string `json:"nativeLibs"`
	// URLSchemes are the custom URL schemes the application handles.
	URLSchemes []string `json:"urlSchemes"`
	// DocumentTypes are the file types the application opens.
	DocumentTypes []DocumentType `json:"documentTypes"`
	// ExportedTypes declares uniform type identifiers this application owns
	// (UTExportedTypeDeclarations), for the file types DocumentTypes name.
	ExportedTypes []TypeDeclaration `json:"exportedTypes"`
	// ImportedTypes declares identifiers that belong to someone else but
	// that this application relies on being known (UTImportedTypeDeclarations).
	ImportedTypes []TypeDeclaration `json:"importedTypes"`
	// Usage maps NS…UsageDescription Info.plist keys to the text macOS shows
	// in a permission prompt.
	Usage map[string]string `json:"usage"`
	// Entitlements are written to the generated entitlements file.
	Entitlements map[string]any `json:"entitlements"`
	// LoginAgents are bundled launch agents that run this application's own
	// executable, for app/login.
	LoginAgents []LoginAgent `json:"loginAgents"`
	// ExtraPlist is merged into Info.plist last and wins over everything gnui writes.
	ExtraPlist map[string]any `json:"extraPlist"`

	dir string // directory of gnui.json
}

// Resource is one entry of Config.Resources.
type Resource struct {
	// From is the source path, relative to gnui.json.
	From string `json:"from"`
	// To is the path under Contents/Resources (default: the base name of From).
	To string `json:"to"`
}

// DocumentType is one entry of Config.DocumentTypes.
type DocumentType struct {
	// Name is the type's display name (CFBundleTypeName).
	Name string `json:"name"`
	// Role is Editor, Viewer, Shell or None (default Viewer).
	Role string `json:"role"`
	// Extensions are file name extensions without the dot.
	Extensions []string `json:"extensions"`
	// UTIs are uniform type identifiers (LSItemContentTypes).
	UTIs []string `json:"utis"`
	// Rank is Owner, Default, Alternate or None (default Default).
	Rank string `json:"rank"`
}

// TypeDeclaration is one entry of Config.ExportedTypes or Config.ImportedTypes:
// a uniform type identifier and the file name extensions and MIME types that
// map to it.
type TypeDeclaration struct {
	// Identifier is the UTI, reverse-DNS ("dev.example.note").
	Identifier string `json:"identifier"`
	// Description is the human-readable kind ("Note document").
	Description string `json:"description"`
	// ConformsTo lists the UTIs this one belongs to (default ["public.data"]).
	ConformsTo []string `json:"conformsTo"`
	// Extensions are file name extensions without the dot.
	Extensions []string `json:"extensions"`
	// MimeTypes are MIME types that map to the identifier.
	MimeTypes []string `json:"mimeTypes"`
}

// LoginAgent is one entry of Config.LoginAgents: a launch agent plist for
// SMAppService. Its label is "<bundle id>.<name>" and its program is the
// bundle's own executable, so a ".dev" bundle gets a ".dev" label.
type LoginAgent struct {
	// Name is the label's last component and the plist's file name part.
	Name string `json:"name"`
	// Arguments are passed to the executable after its name.
	Arguments []string `json:"arguments"`
}

var (
	reID      = regexp.MustCompile(`^[A-Za-z0-9]([A-Za-z0-9-]*[A-Za-z0-9])?(\.[A-Za-z0-9]([A-Za-z0-9-]*[A-Za-z0-9])?)+$`)
	reVersion = regexp.MustCompile(`^[0-9]+(\.[0-9]+){0,2}$`)
	reScheme  = regexp.MustCompile(`^[a-z][a-z0-9+.-]*$`)
	reAgent   = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9-]*$`)
	reEntKey  = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9.-]*$`)
	reUTI     = regexp.MustCompile(`^[A-Za-z0-9-]+(\.[A-Za-z0-9-]+)+$`)
	reExt     = regexp.MustCompile(`^[A-Za-z0-9_+-]+$`)
)

// LoadConfig reads and validates the gnui.json at path and fills the defaults.
func LoadConfig(cfgFile string) (*Config, error) {
	data, err := os.ReadFile(cfgFile)
	if err != nil {
		return nil, err
	}
	abs, err := filepath.Abs(cfgFile)
	if err != nil {
		return nil, err
	}
	var c Config
	dec := json.NewDecoder(bytes.NewReader(data))
	dec.DisallowUnknownFields()
	if err := dec.Decode(&c); err != nil {
		return nil, fmt.Errorf("%s: %w", cfgFile, err)
	}
	// Exactly one value: anything after it, even a stray "}" or "]", is an error.
	if err := dec.Decode(new(json.RawMessage)); err != io.EOF {
		return nil, fmt.Errorf("%s: unexpected data after the JSON object", cfgFile)
	}
	c.dir = filepath.Dir(abs)
	c.applyDefaults()
	if err := c.Validate(); err != nil {
		return nil, fmt.Errorf("%s: %w", cfgFile, err)
	}
	return &c, nil
}

func (c *Config) applyDefaults() {
	if c.Main == "" {
		c.Main = "."
	}
	if c.Executable == "" {
		c.Executable = strings.ReplaceAll(c.Name, " ", "")
	}
	if c.Version == "" {
		c.Version = "1.0.0"
	}
	if c.Build == "" {
		c.Build = "1"
	}
	if c.MinimumSystem == "" {
		c.MinimumSystem = "14.0"
	}
	for _, list := range [][]TypeDeclaration{c.ExportedTypes, c.ImportedTypes} {
		for i := range list {
			if len(list[i].ConformsTo) == 0 {
				list[i].ConformsTo = []string{"public.data"}
			}
		}
	}
}

// ResourceDest returns where resource r goes under Contents/Resources: its
// To, or the base name of its From.
func (r Resource) ResourceDest() string {
	if r.To != "" {
		return r.To
	}
	return filepath.Base(filepath.Clean(r.From))
}

// checkRelPath reports why p is not a safe relative path inside a bundle
// directory: empty, absolute, "." or ".." components, or backslashes.
func checkRelPath(p string) error {
	if p == "" || p == "." || p == ".." {
		return fmt.Errorf("%q is not a usable path", p)
	}
	if filepath.IsAbs(p) || strings.HasPrefix(p, "/") || strings.Contains(p, `\`) || strings.ContainsRune(p, 0) {
		return fmt.Errorf("%q must be a relative path inside the bundle", p)
	}
	for _, part := range strings.Split(path.Clean(p), "/") {
		if part == "" || part == "." || part == ".." {
			return fmt.Errorf("%q must not contain . or .. components", p)
		}
	}
	for _, part := range strings.Split(p, "/") {
		if part == ".." {
			return fmt.Errorf("%q must not contain .. components", p)
		}
	}
	return nil
}

// Warnings lists things that are legal but probably wrong.
func (c *Config) Warnings() []string {
	declared := map[string]bool{}
	for _, t := range c.ExportedTypes {
		declared[t.Identifier] = true
	}
	for _, t := range c.ImportedTypes {
		declared[t.Identifier] = true
	}
	var w []string
	for _, d := range c.DocumentTypes {
		for _, u := range d.UTIs {
			if !declared[u] && !strings.HasPrefix(u, "public.") && !strings.HasPrefix(u, "com.apple.") {
				w = append(w, fmt.Sprintf("document type %q uses %s, which gnui.json does not declare in exportedTypes or importedTypes: macOS will not know the identifier unless another application declares it", d.Name, u))
			}
		}
	}
	return w
}

// Validate reports the first problem in c.
func (c *Config) Validate() error {
	badName := func(s string) bool {
		return s == "" || s == "." || s == ".." || strings.ContainsAny(s, "/\x00:") || strings.HasPrefix(s, ".")
	}
	if !reID.MatchString(c.ID) {
		return fmt.Errorf("id %q must be a reverse-DNS bundle identifier such as dev.example.app", c.ID)
	}
	if badName(c.Name) {
		return fmt.Errorf("name %q must be a plain file name (no slash or colon, not starting with a dot)", c.Name)
	}
	if c.BundleName != "" && badName(c.BundleName) {
		return fmt.Errorf("bundleName %q must be a plain file name (no slash or colon, not starting with a dot)", c.BundleName)
	}
	if badName(c.Executable) {
		return fmt.Errorf("executable %q must be a plain file name", c.Executable)
	}
	for _, f := range []struct{ key, v string }{{"version", c.Version}, {"build", c.Build}, {"minimumSystem", c.MinimumSystem}} {
		if !reVersion.MatchString(f.v) {
			return fmt.Errorf("%s %q must be one to three dotted integers", f.key, f.v)
		}
	}
	if c.Category != "" && !strings.HasPrefix(c.Category, "public.app-category.") {
		return fmt.Errorf("category %q must start with public.app-category.", c.Category)
	}
	for i, r := range c.Resources {
		if r.From == "" {
			return fmt.Errorf("resources[%d]: from is empty", i)
		}
		if r.To != "" {
			if err := checkRelPath(r.To); err != nil {
				return fmt.Errorf("resources[%d]: to: %w", i, err)
			}
		} else if err := checkRelPath(r.ResourceDest()); err != nil {
			return fmt.Errorf("resources[%d]: from %q has no usable file name (%v): give a to", i, r.From, err)
		}
	}
	for i, f := range c.Frameworks {
		if !strings.HasSuffix(f, ".framework") && !strings.HasSuffix(f, ".dylib") {
			return fmt.Errorf("frameworks[%d]: %q must be a .framework directory or a .dylib file", i, f)
		}
		if err := checkRelPath(filepath.Base(filepath.Clean(f))); err != nil {
			return fmt.Errorf("frameworks[%d]: %w", i, err)
		}
	}
	for i, d := range c.NativeLibs {
		if d == "" {
			return fmt.Errorf("nativeLibs[%d]: empty", i)
		}
	}
	for _, s := range c.URLSchemes {
		if !reScheme.MatchString(s) {
			return fmt.Errorf("urlSchemes: %q is not a valid URL scheme (lower case letters, digits, + . -)", s)
		}
	}
	for i, d := range c.DocumentTypes {
		if d.Name == "" {
			return fmt.Errorf("documentTypes[%d]: name is empty", i)
		}
		if len(d.Extensions) == 0 && len(d.UTIs) == 0 {
			return fmt.Errorf("documentTypes[%d] (%s): give extensions or utis", i, d.Name)
		}
		switch d.Role {
		case "", "Editor", "Viewer", "Shell", "None":
		default:
			return fmt.Errorf("documentTypes[%d] (%s): role %q must be Editor, Viewer, Shell or None", i, d.Name, d.Role)
		}
		switch d.Rank {
		case "", "Owner", "Default", "Alternate", "None":
		default:
			return fmt.Errorf("documentTypes[%d] (%s): rank %q must be Owner, Default, Alternate or None", i, d.Name, d.Rank)
		}
	}
	for kind, list := range map[string][]TypeDeclaration{"exportedTypes": c.ExportedTypes, "importedTypes": c.ImportedTypes} {
		seenUTI := map[string]bool{}
		for i, t := range list {
			if !reUTI.MatchString(t.Identifier) {
				return fmt.Errorf("%s[%d]: identifier %q must be a reverse-DNS type identifier", kind, i, t.Identifier)
			}
			if kind == "exportedTypes" && (strings.HasPrefix(t.Identifier, "public.") || strings.HasPrefix(t.Identifier, "com.apple.")) {
				return fmt.Errorf("exportedTypes[%d]: %s belongs to Apple; export an identifier of your own", i, t.Identifier)
			}
			if seenUTI[t.Identifier] {
				return fmt.Errorf("%s: duplicate identifier %q", kind, t.Identifier)
			}
			seenUTI[t.Identifier] = true
			if len(t.Extensions) == 0 && len(t.MimeTypes) == 0 {
				return fmt.Errorf("%s[%d] (%s): give extensions or mimeTypes", kind, i, t.Identifier)
			}
			for _, e := range t.Extensions {
				if !reExt.MatchString(e) {
					return fmt.Errorf("%s[%d] (%s): extension %q must be letters and digits without the dot", kind, i, t.Identifier, e)
				}
			}
			for _, m := range t.MimeTypes {
				if !strings.Contains(m, "/") || strings.ContainsAny(m, " \t") {
					return fmt.Errorf("%s[%d] (%s): %q is not a MIME type", kind, i, t.Identifier, m)
				}
			}
			for _, u := range t.ConformsTo {
				if !reUTI.MatchString(u) {
					return fmt.Errorf("%s[%d] (%s): conformsTo %q is not a type identifier", kind, i, t.Identifier, u)
				}
			}
		}
	}
	for k, v := range c.Usage {
		if !strings.HasPrefix(k, "NS") || !strings.HasSuffix(k, "UsageDescription") {
			return fmt.Errorf("usage: key %q must look like NSCameraUsageDescription", k)
		}
		if strings.TrimSpace(v) == "" {
			return fmt.Errorf("usage: %s has no text", k)
		}
	}
	for k := range c.Entitlements {
		if !reEntKey.MatchString(k) {
			return fmt.Errorf("entitlements: bad key %q", k)
		}
	}
	seen := map[string]bool{}
	for i, a := range c.LoginAgents {
		if !reAgent.MatchString(a.Name) {
			return fmt.Errorf("loginAgents[%d]: name %q must be letters, digits and dashes", i, a.Name)
		}
		if seen[a.Name] {
			return fmt.Errorf("loginAgents: duplicate name %q", a.Name)
		}
		seen[a.Name] = true
	}
	return nil
}

// Path resolves p against the directory of gnui.json.
func (c *Config) Path(p string) string {
	if filepath.IsAbs(p) {
		return p
	}
	return filepath.Join(c.dir, p)
}

// BundleID returns the bundle identifier in use: the declared id, or the id
// plus ".dev" for a development bundle.
func (c *Config) BundleID(dev bool) string {
	if dev {
		return c.ID + ".dev"
	}
	return c.ID
}

// AgentLabel returns the launchd label of a login agent for the bundle id in use.
func (c *Config) AgentLabel(a LoginAgent, dev bool) string {
	return c.BundleID(dev) + "." + a.Name
}

// sortedKeys returns the keys of m in order.
func sortedKeys[V any](m map[string]V) []string {
	keys := make([]string, 0, len(m))
	for k := range m {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	return keys
}

// AppFile is the .app's file name: BundleName, or Name, with ".app".
func (c *Config) AppFile() string {
	if c.BundleName != "" {
		return c.BundleName + ".app"
	}
	return c.Name + ".app"
}

// ShownName is the name the system shows: DisplayName, or Name.
func (c *Config) ShownName() string {
	if c.DisplayName != "" {
		return c.DisplayName
	}
	return c.Name
}
