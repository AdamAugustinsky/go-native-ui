package gallery

import (
	"gonativeui/platform"
	"gonativeui/rexui"
	"gonativeui/theme"
	"gonativeui/ui"
	"gonativeui/widget"
)

// The alert and the file panels are the system's where it has them (macOS:
// NSAlert and the open and save panels as sheets, app.App.Alert and
// friends), and the drawn sheet of the "Delete…" button where it has not
// (Windows, Linux, an offscreen window).

// showAlert asks whether to delete the selected rows: the native alert, or
// the drawn sheet.
func (g *Gallery) showAlert() {
	if g.a != nil {
		t, err := g.a.Alert(platform.AlertSpec{
			Style:   platform.AlertWarning,
			Message: "Delete the selected rows?",
			Info:    "Deleting them cannot be undone.",
			Buttons: []string{"Cancel", "Delete"}, Destructive: []int{1},
		})
		if err == nil {
			g.alert = t
			return
		}
	}
	g.askDelete = true
}

// savePanel asks where to save a file.
func (g *Gallery) savePanel() {
	if g.a != nil {
		t, err := g.a.SavePanel(platform.SaveSpec{Name: "gallery.txt", Prompt: "Save", Start: g.opt.PanelStart})
		if err == nil {
			g.alert = t
			return
		}
	}
	g.setStatus("Chose “Save”")
}

// openPanel asks which file to open.
func (g *Gallery) openPanel() {
	if g.a != nil {
		t, err := g.a.OpenPanel(platform.OpenSpec{Files: true, Prompt: "Open", Start: g.opt.PanelStart})
		if err == nil {
			g.alert = t
			return
		}
	}
	g.setStatus("Chose “Open…”")
}

// dialogResults takes the answers of the native alert and panels, at the start
// of a frame.
func (g *Gallery) dialogResults() {
	if g.a == nil {
		return
	}
	for {
		r, ok := g.a.TakeDialog()
		if !ok {
			return
		}
		if r.Ticket != g.alert {
			continue
		}
		g.alert = 0
		switch {
		case r.Outcome == platform.DialogCancelled || (r.Kind == platform.DialogAlert && r.Button != 1):
			g.setStatus("Cancelled")
		case r.Kind == platform.DialogAlert:
			g.table.Sel.Clear()
			g.setStatus("Deleted")
		default:
			g.status = append(g.status[:0], "Chose "...)
			for i, p := range r.Paths {
				if i > 0 {
					g.status = append(g.status, ", "...)
				}
				g.status = append(g.status, p...)
			}
		}
	}
}

// dialog builds the drawn sheet of the "Delete…" button.
func (g *Gallery) dialog(c *ui.Context) {
	k := g.wk
	d := k.BeginDialog("delete", "Delete the selected rows?", &g.askDelete)
	if d.Visible {
		n := g.table.Sel.Count()
		k.Paragraph(c.Fmt().Int(n).Str(" rows are selected. Deleting them cannot be undone, and this text is long enough to wrap inside the dialog.").String())
		d.BeginActions()
		d.Action("Cancel", widget.ButtonDefault)
		if d.Action("Delete", widget.ButtonPrimary) {
			g.table.Sel.Clear()
			g.setStatus("Deleted")
		}
		d.EndActions()
	}
	k.EndDialog(&d)
}

// dialogsScene has the buttons that show the alert, the panels and the drawn
// sheet, and the answer.
func (g *Gallery) dialogsScene(w, h float32) {
	k := g.k
	v, f := g.beginForm(scDialogs, w, h)

	f.Section(k, "Alerts")
	f.Row(k, "Alert", rexui.RowOpts{})
	if rexui.Button(k, "Show Alert…", rexui.ButtonOpts{}).Clicked {
		g.showAlert()
	}
	f.EndRow(k)
	f.Row(k, "Sheet", rexui.RowOpts{})
	if rexui.Button(k, "Delete…", rexui.ButtonOpts{}).Clicked {
		g.askDelete = true
	}
	f.EndRow(k)
	f.EndSection(k, "The alert is the system’s where there is one, and Delete… the sheet this window draws.")

	f.Section(k, "Panels")
	f.Row(k, "Save", rexui.RowOpts{})
	if rexui.Button(k, "Save…", rexui.ButtonOpts{}).Clicked {
		g.savePanel()
	}
	f.EndRow(k)
	f.Row(k, "Open", rexui.RowOpts{})
	if rexui.Button(k, "Open…", rexui.ButtonOpts{}).Clicked {
		g.openPanel()
	}
	f.EndRow(k)
	f.EndSection(k, "")

	f.Section(k, "Result")
	f.Row(k, "Last answer", rexui.RowOpts{})
	g.bytesIn(keyGallery, 3, ui.Children(1), g.status, rexui.LabelOpts{Color: rexui.Of(theme.Label2)})
	f.EndRow(k)
	f.EndSection(k, "")

	g.endForm(&v, &f)
}
