package widgettest

import (
	"go/ast"
	"go/parser"
	"go/token"
	"io/fs"
	"os"
	"path/filepath"
	"regexp"
	"runtime"
	"strconv"
	"strings"
	"testing"

	"gonativeui/theme"
)

// GridOpts configure LintSpacingGrid.
type GridOpts struct {
	// Dir is the folder whose Go files (and its subfolders', tests excluded)
	// are read; "" is the folder of the test that calls LintSpacingGrid.
	Dir string
	// OnGrid reports whether a literal, in points, is a step of the app's
	// grid; nil is theme.OnGrid (0, 2 and multiples of 4).
	OnGrid func(pt float32) bool
	// Scale names the identifiers that hold the display scale (default sc,
	// scale): a literal multiplied by one, or by a call or field named Scale
	// (k.Scale()), is a length in points scaled to pixels, so it is checked
	// (theme.Snap(5*sc) is off the grid).
	Scale []string
}

// The calls, option fields and names that space a layout.
var (
	gridCalls  = map[string]bool{"NextGap": true, "NextPadding": true, "NextPad": true, "Space": true, "NextSpace": true, "Gap": true, "Padding": true}
	gridFields = map[string]bool{"Gap": true, "GapX": true, "GapY": true, "Pad": true, "PadX": true, "PadY": true, "Inset": true, "InsetX": true, "InsetY": true, "Margin": true, "Gutter": true}
	gridName   = regexp.MustCompile(`(?i)(gap|pad|padding|inset|margin|gutter)(pt)?$`)
)

// LintSpacingGrid holds an app's screens to its spacing grid
// (theme.GridUnit): it fails t for every literal a gap, a padding, an inset
// or a margin is built from that is not a step. It reads the literals of
//   - the calls that space a layout (NextGap, NextPadding, NextPad, Space and
//     their kin), directly or through Pt;
//   - the fields of option structs that do (GapX, PadY, Inset…);
//   - the constants and variables named for a gap, a padding, an inset, a
//     margin or a gutter.
//
// A literal that is a factor (x*2, w/3) is not a space and is skipped, unless
// what it multiplies is the scale (GridOpts.Scale). A line that must stay off
// the grid (a size measured from AppKit, a glyph's optical centre) says so
// with a "grid:ok" comment and its reason.
//
//	func TestSpacingGrid(t *testing.T) { widgettest.LintSpacingGrid(t, widgettest.GridOpts{}) }
func LintSpacingGrid(t testing.TB, o GridOpts) {
	t.Helper()
	if o.Dir == "" {
		o.Dir = "."
		if _, file, _, ok := runtime.Caller(1); ok && filepath.IsAbs(file) {
			o.Dir = filepath.Dir(file)
		}
	}
	bad, err := SpacingGridOffenders(o)
	if err != nil {
		t.Fatal(err)
	}
	for _, b := range bad {
		t.Errorf("off the spacing grid: %s", b)
	}
	if len(bad) > 0 {
		t.Logf("%d literals off the grid; mark a deliberate one with a grid:ok comment", len(bad))
	}
}

// SpacingGridOffenders is what LintSpacingGrid reports: each literal off the
// grid as "file:line:col: value in what".
func SpacingGridOffenders(o GridOpts) ([]string, error) {
	if o.Dir == "" {
		o.Dir = "."
	}
	onGrid := o.OnGrid
	if onGrid == nil {
		onGrid = theme.OnGrid
	}
	scale := map[string]bool{"sc": true, "scale": true}
	if len(o.Scale) > 0 {
		scale = map[string]bool{}
		for _, s := range o.Scale {
			scale[s] = true
		}
	}
	if _, err := os.Stat(o.Dir); err != nil {
		return nil, err
	}
	fset := token.NewFileSet()
	var bad []string
	err := filepath.WalkDir(o.Dir, func(path string, d fs.DirEntry, err error) error {
		if err != nil || d.IsDir() || !strings.HasSuffix(path, ".go") || strings.HasSuffix(path, "_test.go") {
			return err
		}
		f, err := parser.ParseFile(fset, path, nil, parser.ParseComments)
		if err != nil {
			return err
		}
		waived := map[int]bool{}
		for _, cg := range f.Comments {
			for _, c := range cg.List {
				if strings.Contains(c.Text, "grid:ok") {
					waived[fset.Position(c.Pos()).Line] = true
				}
			}
		}
		check := func(e ast.Expr, what string) {
			gridLits(e, scale, func(l *ast.BasicLit) {
				v, err := strconv.ParseFloat(l.Value, 32)
				if err != nil || onGrid(float32(v)) {
					return
				}
				p := fset.Position(l.Pos())
				if !waived[p.Line] {
					bad = append(bad, p.String()+": "+l.Value+" in "+what)
				}
			})
		}
		ast.Inspect(f, func(n ast.Node) bool {
			switch n := n.(type) {
			case *ast.CallExpr:
				if s, ok := n.Fun.(*ast.SelectorExpr); ok && gridCalls[s.Sel.Name] {
					for _, a := range n.Args {
						check(a, s.Sel.Name)
					}
				}
			case *ast.KeyValueExpr:
				if id, ok := n.Key.(*ast.Ident); ok && gridFields[id.Name] {
					check(n.Value, id.Name)
				}
			case *ast.ValueSpec:
				for i, name := range n.Names {
					if i < len(n.Values) && gridName.MatchString(name.Name) {
						check(n.Values[i], name.Name)
					}
				}
			}
			return true
		})
		return nil
	})
	return bad, err
}

// isScale reports whether e is the display scale: an identifier named in
// names, or a field or call named Scale (k.Scale(), r.Scale).
func isScale(e ast.Expr, names map[string]bool) bool {
	switch e := e.(type) {
	case *ast.ParenExpr:
		return isScale(e.X, names)
	case *ast.Ident:
		return names[e.Name]
	case *ast.SelectorExpr:
		return e.Sel.Name == "Scale" || names[e.Sel.Name]
	case *ast.CallExpr:
		if s, ok := e.Fun.(*ast.SelectorExpr); ok {
			return s.Sel.Name == "Scale"
		}
	}
	return false
}

// gridLits calls fn for each number literal e is built from, but not for the
// factors of a product or a quotient, unless the other side is the scale.
func gridLits(e ast.Expr, scale map[string]bool, fn func(*ast.BasicLit)) {
	switch e := e.(type) {
	case *ast.BasicLit:
		if e.Kind == token.INT || e.Kind == token.FLOAT {
			fn(e)
		}
	case *ast.ParenExpr:
		gridLits(e.X, scale, fn)
	case *ast.UnaryExpr:
		gridLits(e.X, scale, fn)
	case *ast.BinaryExpr:
		if e.Op == token.MUL || e.Op == token.QUO {
			for _, side := range [2][2]ast.Expr{{e.X, e.Y}, {e.Y, e.X}} {
				if _, lit := side[0].(*ast.BasicLit); !lit {
					gridLits(side[0], scale, fn)
				} else if e.Op == token.MUL && isScale(side[1], scale) {
					gridLits(side[0], scale, fn) // 5*sc: points, scaled
				}
			}
			return
		}
		gridLits(e.X, scale, fn)
		gridLits(e.Y, scale, fn)
	case *ast.CallExpr:
		// e.Pt(8), k.Pt(8), float32(8), max(a, 8), theme.Snap(5*sc)
		for _, a := range e.Args {
			gridLits(a, scale, fn)
		}
	}
}
