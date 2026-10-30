// Package goconst reads the value of a string constant out of a Go source
// file, for the tools that describe a framework tree without building it.
package goconst

import (
	"fmt"
	"go/ast"
	"go/parser"
	"go/token"
	"strconv"
)

// String is the value of the string constant name, declared with a single
// literal in file.
func String(file, name string) (string, error) {
	f, err := parser.ParseFile(token.NewFileSet(), file, nil, parser.SkipObjectResolution)
	if err != nil {
		return "", err
	}
	for _, d := range f.Decls {
		g, ok := d.(*ast.GenDecl)
		if !ok || g.Tok != token.CONST {
			continue
		}
		for _, s := range g.Specs {
			v := s.(*ast.ValueSpec)
			for i, n := range v.Names {
				if n.Name != name || i >= len(v.Values) {
					continue
				}
				lit, ok := v.Values[i].(*ast.BasicLit)
				if !ok || lit.Kind != token.STRING {
					return "", fmt.Errorf("%s: %s is not a single string literal", file, name)
				}
				return strconv.Unquote(lit.Value)
			}
		}
	}
	return "", fmt.Errorf("%s: no constant %s", file, name)
}
