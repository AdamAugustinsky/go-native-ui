//go:build darwin

package live

import (
	"strconv"
	"testing"

	"gonativeui/app"
	"gonativeui/menu"
	"gonativeui/theme/rex"
	"gonativeui/ui"
	"gonativeui/widget"
	"gonativeui/widget/widgettest"
)

// cmdClear is the probe's one menu command, File ▸ Clear (⌘K).
const cmdClear menu.ID = 1

// probe is a small application: a button, a text field, a long list and a
// native menu with one command. It counts what reached it.
type probe struct {
	k       *widget.Kit
	buf     ui.TextBuffer
	list    widget.ListState
	press   int
	cleared int
	rows    int
	dark    bool // the appearance the last frame saw
	focused bool // the window had the keyboard focus in the last frame
}

func (p *probe) Init(a *app.App) error {
	p.k = widget.NewKit(a.UI, a.Text, rex.Theme())
	p.rows = 200
	m, err := menu.NewModel([]menu.Command{{ID: cmdClear, Title: "Clear", Shortcut: menu.Shortcut{Key: 'K', Primary: true}}},
		[]menu.Menu{{Title: "File", Items: []menu.Item{{Kind: menu.ItemCommand, Command: cmdClear}}}}, []int{0})
	if err != nil {
		return err
	}
	p.k.SetMenu(m)
	a.SetMenu(m)
	return nil
}

func (p *probe) Build(c *ui.Context) {
	k := p.k
	k.Begin(c)
	p.dark = c.SystemAppearance().Dark
	p.focused = c.Input().Focused
	if k.Command(cmdClear) {
		p.cleared++
	}
	k.BeginWindow()
	k.Row("top")
	if k.Button("Press").Clicked {
		p.press++
	}
	k.TextField("Name", &p.buf, "")
	k.EndRow()
	k.NextHeight(widget.Fill)
	l := k.BeginList("rows", &p.list, p.rows)
	for r := l.First; r < l.Last; r++ {
		name := []byte("row " + strconv.Itoa(r))
		l.RowNamed(r, p.list.Item(r), name)
		k.LabelBytes(name)
	}
	k.EndList(&l)
	k.EndWindow()
	k.End()
}

func probeConfig() app.Config { return app.Config{Title: "probe", Width: 480, Height: 360} }

func startProbe(t *testing.T) (*widgettest.LiveApp, *probe) {
	t.Helper()
	p := &probe{}
	return widgettest.StartLive(t, probeConfig(), p), p
}

// firstRow is the name of the first row of the list in the last frame.
func firstRow(l *widgettest.LiveApp) string {
	tr := l.Tree()
	for _, n := range tr.Nodes {
		if n.Role == ui.RoleListItem {
			return string(tr.Text(n.Name))
		}
	}
	return ""
}
