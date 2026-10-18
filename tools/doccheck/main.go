// Command doccheck reports exported identifiers of the framework's public
// packages that have no doc comment (phase 5 exit criterion 1): top-level
// functions, methods of exported types, types, constants and variables, and
// the exported fields of exported structs. A constant or variable in a group
// counts as documented by the group's comment; a field, by a comment on its
// line. A comment that heads an unbroken run of fields or of constants (no
// blank line between them) documents the whole run, as godoc shows it above
// them; functions, methods and types each need their own. It exits 1 when it
// finds any.
//
//	go run ./tools/doccheck [dir ...]
//
// With no arguments it checks every package directory under the module root
// except cmd, tools, probes, third_party, internal and testdata.
package main

import (
	"fmt"
	"go/ast"
	"go/parser"
	"go/token"
	"io/fs"
	"os"
	"path/filepath"
	"sort"
	"strings"
)

func main() {
	dirs := os.Args[1:]
	if len(dirs) == 0 {
		dirs = publicDirs(".")
	}
	var missing []string
	for _, d := range dirs {
		missing = append(missing, checkDir(d)...)
	}
	sort.Strings(missing)
	for _, m := range missing {
		fmt.Println(m)
	}
	if len(missing) > 0 {
		fmt.Fprintf(os.Stderr, "doccheck: %d exported identifiers without a doc comment\n", len(missing))
		os.Exit(1)
	}
}

// publicDirs lists the directories under root that hold the framework's
// public packages.
func publicDirs(root string) []string {
	skip := map[string]bool{"cmd": true, "tools": true, "probes": true, "third_party": true, "internal": true, "testdata": true, "bin": true, "docs": true}
	seen := map[string]bool{}
	filepath.WalkDir(root, func(p string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if d.IsDir() {
			if p != root && (skip[d.Name()] || strings.HasPrefix(d.Name(), ".")) {
				return filepath.SkipDir
			}
			return nil
		}
		if strings.HasSuffix(p, ".go") && !strings.HasSuffix(p, "_test.go") {
			seen[filepath.Dir(p)] = true
		}
		return nil
	})
	var out []string
	for d := range seen {
		out = append(out, d)
	}
	sort.Strings(out)
	return out
}

func checkDir(dir string) []string {
	fset := token.NewFileSet()
	pkgs, err := parser.ParseDir(fset, dir, func(fi fs.FileInfo) bool { return !strings.HasSuffix(fi.Name(), "_test.go") }, parser.ParseComments)
	if err != nil {
		return []string{fmt.Sprintf("%s: %v", dir, err)}
	}
	var out []string
	report := func(pos token.Pos, what string) {
		p := fset.Position(pos)
		out = append(out, fmt.Sprintf("%s:%d: %s", p.Filename, p.Line, what))
	}
	for name, pkg := range pkgs {
		if name == "main" || strings.HasSuffix(name, "_test") {
			continue
		}
		for _, f := range pkg.Files {
			for _, decl := range f.Decls {
				switch d := decl.(type) {
				case *ast.FuncDecl:
					if !d.Name.IsExported() || d.Doc != nil {
						continue
					}
					if d.Recv != nil {
						if t := recvType(d.Recv); t == "" || !ast.IsExported(t) {
							continue
						} else {
							report(d.Pos(), "method "+t+"."+d.Name.Name)
							continue
						}
					}
					report(d.Pos(), "func "+d.Name.Name)
				case *ast.GenDecl:
					valRun, valEnd := false, 0 // as for fields: a documented run of specs
					for _, spec := range d.Specs {
						switch s := spec.(type) {
						case *ast.TypeSpec:
							if !s.Name.IsExported() {
								continue
							}
							if s.Doc == nil && d.Doc == nil && s.Comment == nil {
								report(s.Pos(), "type "+s.Name.Name)
							}
							if st, ok := s.Type.(*ast.StructType); ok {
								run := false // the previous field is documented and ends on the line before
								prevEnd := 0
								for _, fld := range st.Fields.List {
									line := fset.Position(fld.Pos()).Line
									doc := fld.Doc != nil || fld.Comment != nil || (run && line == prevEnd+1)
									run, prevEnd = doc, fset.Position(fld.End()).Line
									if doc {
										continue
									}
									for _, n := range fld.Names {
										if n.IsExported() {
											report(n.Pos(), "field "+s.Name.Name+"."+n.Name)
										}
									}
								}
							}
						case *ast.ValueSpec:
							line := fset.Position(s.Pos()).Line
							doc := d.Doc != nil || s.Doc != nil || s.Comment != nil || (valRun && line == valEnd+1)
							valRun, valEnd = doc, fset.Position(s.End()).Line
							if doc {
								continue
							}
							for _, n := range s.Names {
								if n.IsExported() {
									report(n.Pos(), d.Tok.String()+" "+n.Name)
								}
							}
						}
					}
				}
			}
		}
	}
	return out
}

func recvType(fl *ast.FieldList) string {
	if fl == nil || len(fl.List) == 0 {
		return ""
	}
	t := fl.List[0].Type
	for {
		switch x := t.(type) {
		case *ast.StarExpr:
			t = x.X
		case *ast.IndexExpr:
			t = x.X
		case *ast.IndexListExpr:
			t = x.X
		case *ast.Ident:
			return x.Name
		default:
			return ""
		}
	}
}
