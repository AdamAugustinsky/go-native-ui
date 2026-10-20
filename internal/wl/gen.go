//go:build ignore

// Command gen turns Wayland protocol XML into static wl_interface /
// wl_message tables (internal/wl/protocol_linux.go), mirroring what
// wayland-scanner's "public-code" mode emits:
//
//   - signature = [since-version digits] then, per argument, an optional '?'
//     (allow-null) followed by i u f s o n a h;
//   - one types[] entry per argument: the referenced interface for typed
//     object/new_id arguments, otherwise NULL.
//
// Interfaces referenced but not defined in the inputs (wl_surface, wl_seat,
// wl_output, ...) are core interfaces; Link resolves them with dlsym from
// libwayland-client ("<name>_interface").
//
// Usage (from this directory):
//
//	go run gen.go -o protocol_linux.go -null zwp_tablet_tool_v2 \
//	    xdg-shell.xml xdg-decoration-unstable-v1.xml fractional-scale-v1.xml viewporter.xml \
//	    cursor-shape-v1.xml xdg-foreign-unstable-v2.xml text-input-unstable-v3.xml
//
// Sources (fetched 2026-10-16):
//
//	https://gitlab.freedesktop.org/wayland/wayland-protocols/-/raw/main/stable/xdg-shell/xdg-shell.xml
//	https://gitlab.freedesktop.org/wayland/wayland-protocols/-/raw/main/unstable/xdg-decoration/xdg-decoration-unstable-v1.xml
//	https://gitlab.freedesktop.org/wayland/wayland-protocols/-/raw/main/staging/fractional-scale/fractional-scale-v1.xml
//	https://gitlab.freedesktop.org/wayland/wayland-protocols/-/raw/main/stable/viewporter/viewporter.xml
//
// and, fetched 2026-10-18:
//
//	.../staging/cursor-shape/cursor-shape-v1.xml
//	.../unstable/xdg-foreign/xdg-foreign-unstable-v2.xml
//	.../unstable/text-input/text-input-unstable-v3.xml
//
// (cursor-shape's get_tablet_tool_v2 names zwp_tablet_tool_v2, which we do
// not bind; -null keeps that entry NULL.)
//
// A protocol added later goes in its own file with -tag, which suffixes the
// generated string pool, helper and Link (so the tables above need not be
// regenerated):
//
//	go run gen.go -o protocol_gestures_linux.go -tag Gestures pointer-gestures-unstable-v1.xml
//
// (fetched 2026-10-19 from
// .../unstable/pointer-gestures/pointer-gestures-unstable-v1.xml; call LinkGestures
// beside Link.)
package main

import (
	"bytes"
	"encoding/xml"
	"flag"
	"fmt"
	"go/format"
	"log"
	"os"
	"sort"
	"strings"
)

type protocol struct {
	Interfaces []iface `xml:"interface"`
}

type iface struct {
	Name     string `xml:"name,attr"`
	Version  int    `xml:"version,attr"`
	Requests []msg  `xml:"request"`
	Events   []msg  `xml:"event"`
}

type msg struct {
	Name  string `xml:"name,attr"`
	Since string `xml:"since,attr"`
	Args  []arg  `xml:"arg"`
}

type arg struct {
	Name      string `xml:"name,attr"`
	Type      string `xml:"type,attr"`
	Interface string `xml:"interface,attr"`
	AllowNull string `xml:"allow-null,attr"`
}

var sigChar = map[string]string{
	"int": "i", "uint": "u", "fixed": "f", "string": "s",
	"object": "o", "new_id": "n", "array": "a", "fd": "h",
}

func goName(s string) string {
	var b strings.Builder
	for _, p := range strings.Split(s, "_") {
		b.WriteString(strings.ToUpper(p[:1]) + p[1:])
	}
	return b.String()
}

func lowerFirst(s string) string { return strings.ToLower(s[:1]) + s[1:] }

func signature(m msg) string {
	var b strings.Builder
	if m.Since != "" && m.Since != "1" {
		b.WriteString(m.Since)
	}
	for _, a := range m.Args {
		c, ok := sigChar[a.Type]
		if !ok {
			log.Fatalf("unknown arg type %q", a.Type)
		}
		if a.Type == "new_id" && a.Interface == "" {
			log.Fatalf("untyped new_id in %s not supported", m.Name)
		}
		if a.AllowNull == "true" {
			b.WriteByte('?')
		}
		b.WriteString(c)
	}
	return b.String()
}

func main() {
	out := flag.String("o", "protocol_linux.go", "output file")
	tag := flag.String("tag", "", "suffix for the generated pool, helper, null table and Link, so a second file can sit beside protocol_linux.go")
	nullList := flag.String("null", "", "comma-separated interfaces that are referenced but not defined and not exported by libwayland-client: their types[] entries stay NULL (libwayland only needs types for new_id arguments)")
	flag.Parse()
	null := map[string]bool{}
	for _, n := range strings.Split(*nullList, ",") {
		if n != "" {
			null[n] = true
		}
	}

	var all []iface
	for _, f := range flag.Args() {
		data, err := os.ReadFile(f)
		if err != nil {
			log.Fatal(err)
		}
		var p protocol
		if err := xml.Unmarshal(data, &p); err != nil {
			log.Fatalf("%s: %v", f, err)
		}
		all = append(all, p.Interfaces...)
	}
	own := map[string]bool{}
	for _, i := range all {
		own[i.Name] = true
	}

	// String pool: every name and signature once, NUL separated.
	var pool strings.Builder
	offs := map[string]int{}
	str := func(s string) int {
		if o, ok := offs[s]; ok {
			return o
		}
		o := pool.Len()
		offs[s] = o
		pool.WriteString(s)
		pool.WriteByte(0)
		return o
	}
	sFn, strsName, nullName := "s"+*tag, "strs"+*tag, "nullTypes"+*tag
	lit := func(s string) string { return fmt.Sprintf("%s(%d)", sFn, str(s)) }

	var body bytes.Buffer
	core := map[string]bool{}
	type ref struct {
		arr    string
		idx    int
		target string
	}
	var refs []ref
	maxArgs := 0

	for _, i := range all {
		g := goName(i.Name)
		fmt.Fprintf(&body, "// %s: version %d, %d requests, %d events.\n", i.Name, i.Version, len(i.Requests), len(i.Events))
		fmt.Fprintf(&body, "var %s = Interface{Name: %s, Version: %d, MethodCount: %d, Methods: %s, EventCount: %d, Events: %s}\n\n",
			g, lit(i.Name), i.Version, len(i.Requests), msgsPtr(lowerFirst(g)+"Methods", len(i.Requests)), len(i.Events), msgsPtr(lowerFirst(g)+"Events", len(i.Events)))
		for _, kind := range []struct {
			arr  string
			list []msg
			doc  string
		}{{lowerFirst(g) + "Methods", i.Requests, "request"}, {lowerFirst(g) + "Events", i.Events, "event"}} {
			if len(kind.list) == 0 {
				continue
			}
			fmt.Fprintf(&body, "var %s = [%d]Message{\n", kind.arr, len(kind.list))
			var decls bytes.Buffer
			for n, m := range kind.list {
				if len(m.Args) > maxArgs {
					maxArgs = len(m.Args)
				}
				types := "&" + nullName + "[0]"
				var tname string
				for k, a := range m.Args {
					if a.Interface == "" {
						continue
					}
					if tname == "" {
						tname = fmt.Sprintf("%sT%d_%s", kind.arr, n, m.Name)
						fmt.Fprintf(&decls, "var %s [%d]uintptr\n", tname, len(m.Args))
						types = "&" + tname + "[0]"
					}
					if null[a.Interface] {
						continue
					}
					refs = append(refs, ref{tname, k, a.Interface})
					if !own[a.Interface] {
						core[a.Interface] = true
					}
				}
				fmt.Fprintf(&body, "\t{%s, %s, %s}, // %s %d %s(%s)\n", lit(m.Name), lit(signature(m)), types, kind.doc, n, m.Name, signature(m))
			}
			body.WriteString("}\n\n")
			body.Write(decls.Bytes())
			body.WriteString("\n")
		}
	}
	if maxArgs > 16 {
		log.Fatalf("nullTypes too small for %d args", maxArgs)
	}

	var b bytes.Buffer
	b.WriteString("// Code generated by gen.go from Wayland protocol XML (see gen.go for the\n// sources and dates); DO NOT EDIT.\n\n//go:build linux\n\npackage wl\n\nimport \"unsafe\"\n\n")
	fmt.Fprintf(&b, "// %s is the NUL-separated pool of every interface name, message name and\n// signature. %s(off) is a pointer to the C string at offset off (read-only data).\nconst %s = %q\n\n", strsName, sFn, strsName, pool.String())
	fmt.Fprintf(&b, "func %s(off uintptr) *byte { return (*byte)(unsafe.Add(unsafe.Pointer(unsafe.StringData(%s)), off)) }\n\n", sFn, strsName)
	fmt.Fprintf(&b, "// %s backs every message without typed object arguments.\nvar %s [16]uintptr\n\n", nullName, nullName)
	b.Write(body.Bytes())

	var coreNames []string
	for n := range core {
		coreNames = append(coreNames, n)
	}
	sort.Strings(coreNames)
	fmt.Fprintf(&b, "// Link%s fills the types[] arrays. sym resolves a data symbol in libwayland-client.\n", *tag)
	fmt.Fprintf(&b, "func Link%s(sym func(name string) uintptr) error {\n", *tag)
	for _, n := range coreNames {
		fmt.Fprintf(&b, "\t%s := sym(%q)\n\tif %s == 0 {\n\t\treturn errMissing(%q)\n\t}\n", "c"+goName(n), n+"_interface", "c"+goName(n), n+"_interface")
	}
	for _, r := range refs {
		if own[r.target] {
			fmt.Fprintf(&b, "\t%s[%d] = %s.Addr()\n", r.arr, r.idx, goName(r.target))
		} else {
			fmt.Fprintf(&b, "\t%s[%d] = c%s\n", r.arr, r.idx, goName(r.target))
		}
	}
	b.WriteString("\treturn nil\n}\n")

	src, err := format.Source(b.Bytes())
	if err != nil {
		os.WriteFile(*out+".broken", b.Bytes(), 0o644)
		log.Fatal(err)
	}
	if err := os.WriteFile(*out, src, 0o644); err != nil {
		log.Fatal(err)
	}
}

func msgsPtr(arr string, n int) string {
	if n == 0 {
		return "nil"
	}
	return "&" + arr + "[0]"
}
