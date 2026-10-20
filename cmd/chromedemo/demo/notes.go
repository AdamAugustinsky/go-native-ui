package demo

import (
	"strings"

	"gonativeui/rexui"
	"gonativeui/theme"
	"gonativeui/ui"
	"gonativeui/widget/base"
)

// note is a row of the notes list; header, when set, names the group it
// starts.
type note struct {
	header, title, date, preview string
}

// notes are the scene's list (tools/appkitscenes: notesScene).
var notes = [...]note{
	{"", "Launch meeting", "09:41", "Agenda: review the launch checklist"},
	{"", "Grocery list", "08:15", "Oat milk, lemons, rice, coffee beans"},
	{"Previous 7 Days", "Trip ideas", "Friday", "Lisbon in spring, the coast road"},
	{"", "Book notes", "Thursday", "Chapter 3: the habit loop"},
	{"", "Recipe: lentil soup", "Tuesday", "Onion, carrot, celery, red lentils"},
	{"", "Gift ideas", "Monday", "A good notebook, a plant"},
	{"Previous 30 Days", "Workout plan", "02/10/2026", "Mon: run 5 km, Wed: strength"},
	{"", "Podcast list", "28/09/2026", "Design, history and science"},
	{"", "Garden", "25/09/2026", "Plant the bulbs before the first frost"},
	{"", "Quotes", "21/09/2026", "Simplicity is a great virtue"},
}

// noteTitle and noteBody are the open note (the scene's notesBody), and
// noteHeadings the lines set as headings.
const (
	noteTitle = "Launch meeting"
	noteBody  = "Agenda\n" +
		"• Review the launch checklist and owners\n" +
		"• Confirm the design freeze date\n" +
		"• Decide on the beta group size\n\n" +
		"Decisions\n" +
		"The beta opens to the waiting list in two waves of five hundred people each. The second wave starts once the crash rate stays under a tenth of a percent for three days.\n\n" +
		"Pricing stays as proposed. The annual plan keeps its discount, and the monthly plan gets a seven-day trial.\n\n" +
		"Open questions\n" +
		"Who owns the support inbox during the first week? Do we need a status page before the beta, or is the in-app banner enough?\n\n" +
		"Action items\n" +
		"Sam drafts the announcement and the FAQ by Thursday. Priya prepares the onboarding flow and the empty states. Jordan checks the analytics events against the funnel we agreed on.\n\n" +
		"Notes from the design review\n" +
		"The sidebar felt heavy in dark mode. We will try the lighter selection, smaller section headers and more space above each group. The toolbar keeps three groups: formatting, insert and share.\n\n" +
		"The editor should keep a comfortable measure of about seventy characters. Headings use the title style, and the body stays at the system size so it matches the rest of the app.\n\n" +
		"Next meeting\n" +
		"Thursday at 10:00, same room. Bring the updated timeline and the list of risks.\n\n" +
		"Risks\n" +
		"The migration of old notes may take longer than planned on large libraries. The sync service needs a load test before the second wave. Translations for three languages are still missing.\n\n" +
		"Follow-up\n" +
		"Collect feedback from the first wave through the in-app form and summarise it every Friday.\n"
)

var noteHeadings = [...]string{"Agenda", "Decisions", "Open questions", "Action items", "Notes from the design review", "Next meeting", "Risks", "Follow-up"}

// noteText makes the open note's rich text: the title in Title 1, the
// headings in 15-pt semibold, the rest body text (the scene's text view).
func (d *Demo) noteText() {
	d.richBody = []byte(noteTitle + "\n" + noteBody)
	d.spans = append(d.spans[:0], ui.Span{Off: 0, Len: uint32(len(noteTitle) + 1), Style: 1})
	for _, h := range noteHeadings {
		if i := strings.Index(noteBody, h+"\n"); i >= 0 {
			d.spans = append(d.spans, ui.Span{Off: uint32(len(noteTitle) + 1 + i), Len: uint32(len(h) + 1), Style: 2})
		}
	}
}

// The editor's text inset, in points: the text view's container inset (24,
// 12) and its line fragment padding (5).
const noteInsetX, noteInsetY = 24 + 5, 12

// notesEditor is the open note in a scroll view filling the content pane (the
// current parent), w by h.
func (d *Demo) notesEditor(w, h float32) {
	k := d.k
	c, r := k.Context(), k.Theme()
	s := r.Scale
	k.NextSize(ui.Px(w), ui.Px(h))
	v := k.BeginScroll("Note", &d.content, base.ScrollVertical)
	if d.scrolled > 0 {
		// the scene asks for 160 and gets its end, 114 (the text view at
		// y -74, 40 at rest; with overlay scrollers, wider, 98)
		c.SetScroll(v.Inner, 0, float64(theme.Snap(114*s)))
		d.scrolled--
	}
	_, sy := c.Scroll(v.Inner)
	d.under = sy > 0.5
	px, py := theme.Snap(noteInsetX*s), theme.Snap(noteInsetY*s)
	c.NextSize(ui.Pct(1), ui.Children(1))
	// the text ends in a newline: NSTextView lays out the empty line after
	// it (its extra line fragment), a body line more below the text
	c.NextPadding(px, py, px, py+r.Styles[theme.StyleBody].Line)
	c.Push(c.Box(0, "note page"))
	c.NextSize(ui.Pct(1), ui.TextSize(0))
	c.NextTextColor(r.Colors[theme.Text])
	c.RichText(ui.HashString(0, "note"), d.richBody, d.spans, &d.rich, ui.RichOpts{})
	c.Pop()
	k.CloseScrollContent(&v)
	rexui.ScrollBars(k, &v)
	k.CloseScroll(&v)
}
