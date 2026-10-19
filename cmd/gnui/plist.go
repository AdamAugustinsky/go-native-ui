package main

import (
	"bytes"
	"fmt"
	"math"
	"strconv"
	"strings"
)

// encodePlist renders v as an XML property list. Supported values: string,
// bool, int, int64, float64 (written as <integer> when whole), []string,
// []any, map[string]string and map[string]any. Dictionary keys are sorted, so
// the output is deterministic.
func encodePlist(v any) ([]byte, error) {
	var b bytes.Buffer
	b.WriteString(`<?xml version="1.0" encoding="UTF-8"?>` + "\n")
	b.WriteString(`<!DOCTYPE plist PUBLIC "-//Apple//DTD PLIST 1.0//EN" "http://www.apple.com/DTDs/PropertyList-1.0.dtd">` + "\n")
	b.WriteString(`<plist version="1.0">` + "\n")
	if err := writePlistValue(&b, v, 0); err != nil {
		return nil, err
	}
	b.WriteString("</plist>\n")
	return b.Bytes(), nil
}

func plistEscape(s string) string {
	var b strings.Builder
	for _, r := range s {
		switch {
		case r == '&':
			b.WriteString("&amp;")
		case r == '<':
			b.WriteString("&lt;")
		case r == '>':
			b.WriteString("&gt;")
		case r < 0x20 && r != '\t' && r != '\n' && r != '\r':
			// not representable in XML 1.0
		default:
			b.WriteRune(r)
		}
	}
	return b.String()
}

func writePlistValue(b *bytes.Buffer, v any, depth int) error {
	ind := strings.Repeat("\t", depth)
	switch x := v.(type) {
	case string:
		fmt.Fprintf(b, "%s<string>%s</string>\n", ind, plistEscape(x))
	case bool:
		if x {
			fmt.Fprintf(b, "%s<true/>\n", ind)
		} else {
			fmt.Fprintf(b, "%s<false/>\n", ind)
		}
	case int:
		fmt.Fprintf(b, "%s<integer>%d</integer>\n", ind, x)
	case int64:
		fmt.Fprintf(b, "%s<integer>%d</integer>\n", ind, x)
	case float64:
		if x == math.Trunc(x) && math.Abs(x) < 1<<53 {
			fmt.Fprintf(b, "%s<integer>%d</integer>\n", ind, int64(x))
		} else if math.IsNaN(x) || math.IsInf(x, 0) {
			return fmt.Errorf("plist: cannot encode %v", x)
		} else {
			fmt.Fprintf(b, "%s<real>%s</real>\n", ind, strconv.FormatFloat(x, 'g', -1, 64))
		}
	case []string:
		items := make([]any, len(x))
		for i, s := range x {
			items[i] = s
		}
		return writePlistValue(b, items, depth)
	case []any:
		if len(x) == 0 {
			fmt.Fprintf(b, "%s<array/>\n", ind)
			return nil
		}
		fmt.Fprintf(b, "%s<array>\n", ind)
		for _, e := range x {
			if err := writePlistValue(b, e, depth+1); err != nil {
				return err
			}
		}
		fmt.Fprintf(b, "%s</array>\n", ind)
	case map[string]string:
		m := make(map[string]any, len(x))
		for k, s := range x {
			m[k] = s
		}
		return writePlistValue(b, m, depth)
	case map[string]any:
		if len(x) == 0 {
			fmt.Fprintf(b, "%s<dict/>\n", ind)
			return nil
		}
		fmt.Fprintf(b, "%s<dict>\n", ind)
		for _, k := range sortedKeys(x) {
			fmt.Fprintf(b, "%s\t<key>%s</key>\n", ind, plistEscape(k))
			if err := writePlistValue(b, x[k], depth+1); err != nil {
				return err
			}
		}
		fmt.Fprintf(b, "%s</dict>\n", ind)
	default:
		return fmt.Errorf("plist: unsupported value of type %T (JSON null is not allowed in a property list)", v)
	}
	return nil
}

// infoPlist builds the Info.plist dictionary of the bundle for c. dev selects
// the development variant (bundle id plus ".dev").
func infoPlist(c *Config, dev bool) map[string]any {
	d := map[string]any{
		"CFBundleDevelopmentRegion":     "en",
		"CFBundleExecutable":            c.Executable,
		"CFBundleIdentifier":            c.BundleID(dev),
		"CFBundleInfoDictionaryVersion": "6.0",
		"CFBundleName":                  c.Name,
		"CFBundleDisplayName":           c.Name,
		"CFBundlePackageType":           "APPL",
		"CFBundleShortVersionString":    c.Version,
		"CFBundleVersion":               c.Build,
		"CFBundleSupportedPlatforms":    []any{"MacOSX"},
		"LSMinimumSystemVersion":        c.MinimumSystem,
		"NSHighResolutionCapable":       true,
		"NSPrincipalClass":              "NSApplication",
	}
	if c.Icon != "" {
		d["CFBundleIconFile"] = "AppIcon"
	}
	if c.Category != "" {
		d["LSApplicationCategoryType"] = c.Category
	}
	if len(c.URLSchemes) > 0 {
		schemes := make([]any, len(c.URLSchemes))
		for i, s := range c.URLSchemes {
			schemes[i] = s
		}
		d["CFBundleURLTypes"] = []any{map[string]any{
			"CFBundleURLName":    c.BundleID(dev),
			"CFBundleURLSchemes": schemes,
			"CFBundleTypeRole":   "Viewer",
		}}
	}
	if len(c.DocumentTypes) > 0 {
		var types []any
		for _, t := range c.DocumentTypes {
			role := t.Role
			if role == "" {
				role = "Viewer"
			}
			rank := t.Rank
			if rank == "" {
				rank = "Default"
			}
			e := map[string]any{
				"CFBundleTypeName": t.Name,
				"CFBundleTypeRole": role,
				"LSHandlerRank":    rank,
			}
			if len(t.Extensions) > 0 {
				e["CFBundleTypeExtensions"] = t.Extensions
			}
			if len(t.UTIs) > 0 {
				e["LSItemContentTypes"] = t.UTIs
			}
			types = append(types, e)
		}
		d["CFBundleDocumentTypes"] = types
	}
	if len(c.ExportedTypes) > 0 {
		d["UTExportedTypeDeclarations"] = typeDeclarations(c.ExportedTypes)
	}
	if len(c.ImportedTypes) > 0 {
		d["UTImportedTypeDeclarations"] = typeDeclarations(c.ImportedTypes)
	}
	for k, v := range c.Usage {
		d[k] = v
	}
	for k, v := range c.ExtraPlist {
		d[k] = v
	}
	return d
}

// typeDeclarations builds UTExportedTypeDeclarations / UTImportedTypeDeclarations.
func typeDeclarations(list []TypeDeclaration) []any {
	var out []any
	for _, t := range list {
		conforms := t.ConformsTo
		if len(conforms) == 0 {
			conforms = []string{"public.data"}
		}
		tags := map[string]any{}
		if len(t.Extensions) > 0 {
			tags["public.filename-extension"] = t.Extensions
		}
		if len(t.MimeTypes) > 0 {
			tags["public.mime-type"] = t.MimeTypes
		}
		e := map[string]any{
			"UTTypeIdentifier":       t.Identifier,
			"UTTypeConformsTo":       conforms,
			"UTTypeTagSpecification": tags,
		}
		if t.Description != "" {
			e["UTTypeDescription"] = t.Description
		}
		out = append(out, e)
	}
	return out
}

// entitlementsPlist returns the generated entitlements, or nil for none.
func entitlementsPlist(c *Config) map[string]any {
	if len(c.Entitlements) == 0 {
		return nil
	}
	return c.Entitlements
}

// agentPlist builds the launchd plist of login agent a, as SMAppService
// expects it under Contents/Library/LaunchAgents: the program is the bundle's
// own executable and the label derives from the bundle id in use.
func agentPlist(c *Config, a LoginAgent, dev bool) map[string]any {
	args := []any{c.Executable}
	for _, s := range a.Arguments {
		args = append(args, s)
	}
	return map[string]any{
		"Label":                       c.AgentLabel(a, dev),
		"BundleProgram":               "Contents/MacOS/" + c.Executable,
		"ProgramArguments":            args,
		"AssociatedBundleIdentifiers": []any{c.BundleID(dev)},
		"RunAtLoad":                   true,
		"LimitLoadToSessionType":      "Aqua",
		"ProcessType":                 "Interactive",
	}
}
