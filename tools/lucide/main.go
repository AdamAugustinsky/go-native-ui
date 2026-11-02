// Command lucide refreshes third_party/lucide from a release of the Lucide
// icon set (https://lucide.dev): it downloads the lucide-static package from
// npm, checks it against the registry's integrity hash, compiles every icon's
// SVG elements to outlines (oksvg's path parser, so arcs become cubics) and
// writes icons.bin, LICENSE, VERSION and names.txt. Run it from the
// repository root:
//
//	go run ./tools/lucide                 # the latest release
//	go run ./tools/lucide -version 1.66.0
//	go run ./tools/lucide -only names.txt # a curated set: one name per line
//
// The embedded set is the full release (DECISIONS.md, the Lucide entry); -only
// exists for a build that must be smaller.
package main

import (
	"archive/tar"
	"bufio"
	"bytes"
	"compress/gzip"
	"crypto/sha512"
	"encoding/base64"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"log"
	"math"
	"net/http"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strconv"
	"strings"

	"gonativeui/third_party/lucide"
)

func main() {
	log.SetFlags(0)
	version := flag.String("version", "latest", "lucide-static version")
	out := flag.String("out", "third_party/lucide", "output directory")
	only := flag.String("only", "", "file of icon names to keep, one per line (default: all)")
	flag.Parse()

	meta, err := fetchMeta(*version)
	if err != nil {
		log.Fatal(err)
	}
	tgz, err := get(meta.Dist.Tarball)
	if err != nil {
		log.Fatal(err)
	}
	if err := checkIntegrity(tgz, meta.Dist.Integrity); err != nil {
		log.Fatal(err)
	}
	files, err := untar(tgz)
	if err != nil {
		log.Fatal(err)
	}
	var nodes map[string][][2]json.RawMessage
	if err := json.Unmarshal(files["package/icon-nodes.json"], &nodes); err != nil {
		log.Fatalf("icon-nodes.json: %v", err)
	}
	keep := func(string) bool { return true }
	if *only != "" {
		set, err := readNames(*only)
		if err != nil {
			log.Fatal(err)
		}
		keep = func(n string) bool { return set[n] }
	}

	var enc lucide.Encoder
	names := make([]string, 0, len(nodes))
	for n := range nodes {
		names = append(names, n)
	}
	sort.Strings(names)
	kept := 0
	bodies := map[string]string{} // an icon's SVG body → its name, to find aliases
	for _, n := range names {
		if body, ok := svgBody(files["package/icons/"+n+".svg"]); ok {
			bodies[body] = n
		}
		if !keep(n) {
			continue
		}
		if err := compile(&enc, nodes[n]); err != nil {
			log.Fatalf("%s: %v", n, err)
		}
		enc.Icon(n)
		kept++
	}
	// aliases: an SVG file in icons/ that is not in icon-nodes.json and draws
	// what one of them draws (lucide-static ships the deprecated names so)
	aliases := 0
	var all []string
	for f := range files {
		if strings.HasPrefix(f, "package/icons/") && strings.HasSuffix(f, ".svg") {
			all = append(all, f)
		}
	}
	sort.Strings(all)
	for _, f := range all {
		n := strings.TrimSuffix(strings.TrimPrefix(f, "package/icons/"), ".svg")
		if _, canon := nodes[n]; canon {
			continue
		}
		body, ok := svgBody(files[f])
		if !ok {
			continue
		}
		if to, ok := bodies[body]; ok && keep(to) && enc.Alias(n, to) {
			aliases++
		}
	}
	raw, err := enc.Bytes()
	if err != nil {
		log.Fatal(err)
	}
	z, err := lucide.Compress(raw)
	if err != nil {
		log.Fatal(err)
	}
	commit := tagCommit(meta.Version)
	write := func(name string, b []byte) {
		if err := os.WriteFile(filepath.Join(*out, name), b, 0o644); err != nil {
			log.Fatal(err)
		}
	}
	write("icons.bin", z)
	write("LICENSE", files["package/LICENSE"])
	set := "the full set"
	if *only != "" {
		set = "the names in " + filepath.Base(*only)
	}
	write("VERSION", fmt.Appendf(nil, "lucide-static %s\ncommit %s (github.com/lucide-icons/lucide, tag %s)\ntarball %s\nintegrity %s\nicons %d (%s), aliases %d\ninflated %d bytes, compressed %d bytes\n",
		meta.Version, commit, meta.Version, meta.Dist.Tarball, meta.Dist.Integrity, kept, set, aliases, len(raw), len(z)))
	var nl bytes.Buffer
	for _, n := range lucideNames(&enc, raw) {
		nl.WriteString(n)
		nl.WriteByte('\n')
	}
	write("names.txt", nl.Bytes())
	log.Printf("lucide %s (%s): %d icons, %d aliases, %d bytes inflated, %d compressed", meta.Version, commit, kept, aliases, len(raw), len(z))
}

type npmMeta struct {
	Version string `json:"version"`
	Dist    struct {
		Tarball   string `json:"tarball"`
		Integrity string `json:"integrity"`
	} `json:"dist"`
}

func fetchMeta(version string) (npmMeta, error) {
	var m npmMeta
	b, err := get("https://registry.npmjs.org/lucide-static/" + version)
	if err != nil {
		return m, err
	}
	if err := json.Unmarshal(b, &m); err != nil {
		return m, fmt.Errorf("npm metadata: %w", err)
	}
	if m.Dist.Tarball == "" || !strings.HasPrefix(m.Dist.Integrity, "sha512-") {
		return m, errors.New("npm metadata: no tarball or sha512 integrity")
	}
	return m, nil
}

func get(url string) ([]byte, error) {
	resp, err := http.Get(url)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	if resp.StatusCode != 200 {
		return nil, fmt.Errorf("%s: %s", url, resp.Status)
	}
	return io.ReadAll(io.LimitReader(resp.Body, 64<<20))
}

func checkIntegrity(b []byte, integrity string) error {
	sum := sha512.Sum512(b)
	if "sha512-"+base64.StdEncoding.EncodeToString(sum[:]) != integrity {
		return errors.New("tarball does not match the registry's integrity hash")
	}
	return nil
}

// tagCommit is the commit of the release's tag on GitHub, "unknown" if the
// API does not say.
func tagCommit(version string) string {
	b, err := get("https://api.github.com/repos/lucide-icons/lucide/git/ref/tags/" + version)
	if err != nil {
		return "unknown"
	}
	var r struct {
		Object struct {
			SHA string `json:"sha"`
		} `json:"object"`
	}
	if json.Unmarshal(b, &r) != nil || r.Object.SHA == "" {
		return "unknown"
	}
	return r.Object.SHA
}

// untar reads the files of the package we use: icon-nodes.json, LICENSE and
// icons/*.svg.
func untar(tgz []byte) (map[string][]byte, error) {
	zr, err := gzip.NewReader(bytes.NewReader(tgz))
	if err != nil {
		return nil, err
	}
	tr := tar.NewReader(zr)
	files := map[string][]byte{}
	for {
		h, err := tr.Next()
		if err == io.EOF {
			break
		}
		if err != nil {
			return nil, err
		}
		n := h.Name
		if h.Typeflag != tar.TypeReg || !(n == "package/icon-nodes.json" || n == "package/LICENSE" || strings.HasPrefix(n, "package/icons/") && strings.HasSuffix(n, ".svg")) {
			continue
		}
		b, err := io.ReadAll(io.LimitReader(tr, 16<<20))
		if err != nil {
			return nil, err
		}
		files[n] = b
	}
	if files["package/icon-nodes.json"] == nil || files["package/LICENSE"] == nil {
		return nil, errors.New("the package has no icon-nodes.json or LICENSE")
	}
	return files, nil
}

var classAttr = regexp.MustCompile(`\s*class="[^"]*"`)

// svgBody is an SVG file without its licence comment and class attribute,
// which are all an alias's file changes.
func svgBody(b []byte) (string, bool) {
	if b == nil {
		return "", false
	}
	s := string(b)
	if i := strings.Index(s, "<svg"); i >= 0 {
		s = s[i:]
	}
	return classAttr.ReplaceAllString(s, ""), true
}

func readNames(path string) (map[string]bool, error) {
	f, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	defer f.Close()
	set := map[string]bool{}
	sc := bufio.NewScanner(f)
	for sc.Scan() {
		if n := strings.TrimSpace(sc.Text()); n != "" && !strings.HasPrefix(n, "#") {
			set[n] = true
		}
	}
	return set, sc.Err()
}

// compile adds the elements of one icon to enc.
func compile(enc *lucide.Encoder, elems [][2]json.RawMessage) error {
	for _, el := range elems {
		var tag string
		var attrs map[string]string
		if err := json.Unmarshal(el[0], &tag); err != nil {
			return err
		}
		if err := json.Unmarshal(el[1], &attrs); err != nil {
			return err
		}
		d, err := toPath(tag, attrs)
		if err != nil {
			return err
		}
		segs, err := parsePath(d)
		if err != nil {
			return fmt.Errorf("%s: %w", tag, err)
		}
		var flags byte
		if f := attrs["fill"]; f != "" && f != "none" {
			flags |= lucide.FlagFill
		}
		enc.Begin(flags)
		for _, sg := range segs {
			pts := make([]int32, len(sg.pts))
			for i, v := range sg.pts {
				q := math.Round(v * 64)
				if q < -64*lucide.Grid || q > 2*64*lucide.Grid {
					return fmt.Errorf("%s %q: coordinate %g out of range", tag, d, v)
				}
				pts[i] = int32(q)
			}
			enc.Op(sg.op, pts...)
		}
		enc.End()
	}
	return nil
}

// toPath writes an SVG element as path data.
func toPath(tag string, a map[string]string) (string, error) {
	num := func(k string) float64 {
		v, _ := strconv.ParseFloat(a[k], 64)
		return v
	}
	f := func(v float64) string { return strconv.FormatFloat(v, 'f', -1, 64) }
	switch tag {
	case "path":
		return a["d"], nil
	case "line":
		return fmt.Sprintf("M%s %sL%s %s", f(num("x1")), f(num("y1")), f(num("x2")), f(num("y2"))), nil
	case "polyline", "polygon":
		pts := strings.Fields(strings.ReplaceAll(a["points"], ",", " "))
		if len(pts) < 4 || len(pts)%2 != 0 {
			return "", fmt.Errorf("%s: points %q", tag, a["points"])
		}
		d := "M" + pts[0] + " " + pts[1]
		for i := 2; i < len(pts); i += 2 {
			d += "L" + pts[i] + " " + pts[i+1]
		}
		if tag == "polygon" {
			d += "Z"
		}
		return d, nil
	case "circle", "ellipse":
		cx, cy := num("cx"), num("cy")
		rx, ry := num("r"), num("r")
		if tag == "ellipse" {
			rx, ry = num("rx"), num("ry")
		}
		return fmt.Sprintf("M%s %sA%s %s 0 1 0 %s %sA%s %s 0 1 0 %s %sZ",
			f(cx-rx), f(cy), f(rx), f(ry), f(cx+rx), f(cy), f(rx), f(ry), f(cx-rx), f(cy)), nil
	case "rect":
		x, y, w, h := num("x"), num("y"), num("width"), num("height")
		rx, ry := num("rx"), num("ry")
		if _, ok := a["ry"]; !ok {
			ry = rx
		}
		if _, ok := a["rx"]; !ok {
			rx = ry
		}
		rx, ry = math.Min(rx, w/2), math.Min(ry, h/2)
		if rx <= 0 || ry <= 0 {
			return fmt.Sprintf("M%s %sH%sV%sH%sZ", f(x), f(y), f(x+w), f(y+h), f(x)), nil
		}
		arc := func(ex, ey float64) string { return fmt.Sprintf("A%s %s 0 0 1 %s %s", f(rx), f(ry), f(ex), f(ey)) }
		return "M" + f(x+rx) + " " + f(y) +
			"H" + f(x+w-rx) + arc(x+w, y+ry) +
			"V" + f(y+h-ry) + arc(x+w-rx, y+h) +
			"H" + f(x+rx) + arc(x, y+h-ry) +
			"V" + f(y+ry) + arc(x+rx, y) + "Z", nil
	}
	return "", fmt.Errorf("unknown element %q", tag)
}

// lucideNames lists the names in raw (the format of package lucide).
func lucideNames(_ *lucide.Encoder, raw []byte) []string {
	n := int(raw[4]) | int(raw[5])<<8
	p := 6
	out := make([]string, 0, n)
	for range n {
		l := int(raw[p])
		out = append(out, string(raw[p+1:p+1+l]))
		p += 1 + l + 6
	}
	return out
}
