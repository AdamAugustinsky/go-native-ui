// Package format writes the small texts apps show beside their content: how
// long ago something was ("5m", "3h"), counts with their noun, compact and
// grouped numbers, a short path, a schedule in words and dates, in English
// and Brazilian Portuguese (Locale). A3 and Zap each wrote their own; the
// words are a Locale, which rexui.SetStrings sets with the components'
// (rexui.Strings.Format), so an app picks its language once (D95).
//
// The functions are pure: they take the time "now" and read only the locale
// in use, so a test pins their output.
package format

import (
	"math"
	"strconv"
	"strings"
	"sync/atomic"
	"time"
)

// Locale is the words and conventions of one language. A field left empty
// takes English's (SetLocale).
type Locale struct {
	_ [0]func() // not comparable: nothing compares locales, and their generated == was ~2 KB in every app (D101)
	// Now is a time under a minute ago; Minutes, Hours, Days and Weeks the
	// units after a count ("5m", "3h", "2d", "4w"): Ago.
	Now, Minutes, Hours, Days, Weeks string
	// Decimal and Group are the decimal and the thousands separators;
	// Thousand, Million and Billion the compact suffixes ("12.3K", "12,3
	// mil"): Compact and Grouped.
	Decimal, Group, Thousand, Million, Billion string
	// Date and DateTime are time.Format layouts for a short date and a date
	// with the time ("02/01/06", "02/01/2006 15:04").
	Date, DateTime string
	// ZeroIsOne makes Plural take the singular for 0 as well as 1, as CLDR's
	// Portuguese plural rule does.
	ZeroIsOne bool

	// The schedule's words (Schedule): EveryMinute … EveryWeek are the
	// one-unit forms ("Every minute"); Every begins a count and Minutes2 …
	// Weeks2 end it ("Every 5 minutes"); Daily, Weekdays, Weekends and
	// Weekday[0..6] (Sunday first) begin a time of day, which At and the
	// clock follow ("Weekdays at 9:00"); MonthDay begins a monthly day
	// ("Monthly on day 5").
	EveryMinute, EveryHour, EveryDay, EveryWeek string
	Every, Minutes2, Hours2, Days2, Weeks2      string
	Daily, Weekdays, Weekends, MonthDay, At     string
	Weekday                                     [7]string
	// HourClock writes a time of day as Portuguese does ("9h", "9h30");
	// otherwise as "9:00".
	HourClock bool
}

// English is the default Locale.
var English = Locale{
	Now: "now", Minutes: "m", Hours: "h", Days: "d", Weeks: "w",
	Decimal: ".", Group: ",", Thousand: "K", Million: "M", Billion: "B",
	Date: "01/02/06", DateTime: "01/02/2006 15:04",
	EveryMinute: "Every minute", EveryHour: "Every hour", EveryDay: "Every day", EveryWeek: "Every week",
	Every: "Every ", Minutes2: " minutes", Hours2: " hours", Days2: " days", Weeks2: " weeks",
	Daily: "Every day", Weekdays: "Weekdays", Weekends: "Weekends", MonthDay: "Monthly on day ", At: " at ",
	Weekday: [7]string{"Every Sunday", "Every Monday", "Every Tuesday", "Every Wednesday", "Every Thursday", "Every Friday", "Every Saturday"},
}

// PtBR is Brazilian Portuguese, as T3 Code's rows and Intl's pt-BR write it.
var PtBR = Locale{
	Now: "agora", Minutes: "m", Hours: "h", Days: "d", Weeks: "sem",
	Decimal: ",", Group: ".", Thousand: " mil", Million: " mi", Billion: " bi",
	Date: "02/01/06", DateTime: "02/01/2006 15:04", ZeroIsOne: true,
	EveryMinute: "A cada minuto", EveryHour: "A cada hora", EveryDay: "Todo dia", EveryWeek: "Toda semana",
	Every: "A cada ", Minutes2: " minutos", Hours2: " horas", Days2: " dias", Weeks2: " semanas",
	Daily: "Todo dia", Weekdays: "De segunda a sexta", Weekends: "Sábado e domingo", MonthDay: "Todo dia ", At: " às ",
	Weekday:   [7]string{"Todo domingo", "Toda segunda", "Toda terça", "Toda quarta", "Toda quinta", "Toda sexta", "Todo sábado"},
	HourClock: true,
}

var cur atomic.Pointer[Locale]

func init() { SetLocale(English) }

// SetLocale makes l the words of every function; a field left empty keeps
// English's. rexui.SetStrings calls it with Strings.Format.
func SetLocale(l Locale) {
	fill := func(v *string, en string) {
		if *v == "" {
			*v = en
		}
	}
	e := &English
	for _, p := range [...]struct {
		v  *string
		en string
	}{
		{&l.Now, e.Now}, {&l.Minutes, e.Minutes}, {&l.Hours, e.Hours}, {&l.Days, e.Days}, {&l.Weeks, e.Weeks},
		{&l.Decimal, e.Decimal}, {&l.Group, e.Group}, {&l.Thousand, e.Thousand}, {&l.Million, e.Million}, {&l.Billion, e.Billion},
		{&l.Date, e.Date}, {&l.DateTime, e.DateTime},
		{&l.EveryMinute, e.EveryMinute}, {&l.EveryHour, e.EveryHour}, {&l.EveryDay, e.EveryDay}, {&l.EveryWeek, e.EveryWeek},
		{&l.Every, e.Every}, {&l.Minutes2, e.Minutes2}, {&l.Hours2, e.Hours2}, {&l.Days2, e.Days2}, {&l.Weeks2, e.Weeks2},
		{&l.Daily, e.Daily}, {&l.Weekdays, e.Weekdays}, {&l.Weekends, e.Weekends}, {&l.MonthDay, e.MonthDay}, {&l.At, e.At},
	} {
		fill(p.v, p.en)
	}
	for i := range l.Weekday {
		fill(&l.Weekday[i], e.Weekday[i])
	}
	cur.Store(&l)
}

// Current is the Locale in use.
func Current() Locale { return *cur.Load() }

func loc() *Locale { return cur.Load() }

// Ago is how long before now t was, as a list row writes it: Now under a
// minute, then whole minutes, hours, days and weeks ("5m", "3h", "2d",
// "4sem"); "" for the zero time.
func Ago(now, t time.Time) string {
	if t.IsZero() {
		return ""
	}
	l := loc()
	d := now.Sub(t)
	switch {
	case d < time.Minute:
		return l.Now
	case d < time.Hour:
		return strconv.Itoa(int(d/time.Minute)) + l.Minutes
	case d < 24*time.Hour:
		return strconv.Itoa(int(d/time.Hour)) + l.Hours
	case d < 7*24*time.Hour:
		return strconv.Itoa(int(d/(24*time.Hour))) + l.Days
	}
	return strconv.Itoa(int(d/(7*24*time.Hour))) + l.Weeks
}

// Count is n and its noun, one or many by the locale's plural rule ("1
// file", "3 files"; "0 arquivo" and "1 arquivo" in Portuguese).
func Count(n int, one, many string) string {
	return strconv.Itoa(n) + " " + Plural(n, one, many)
}

// Plural is one or many for n, by the locale's plural rule.
func Plural(n int, one, many string) string {
	if n == 1 || n == -1 || n == 0 && loc().ZeroIsOne {
		return one
	}
	return many
}

// Compact is n in the compact notation ("950", "12.3K", "1.2M"; "12,3 mil",
// "1,2 mi"): one decimal at most, dropped when it is 0.
func Compact(n int64) string {
	l := loc()
	f := float64(n)
	unit := ""
	switch a := max(n, -n); {
	case a >= 1_000_000_000:
		f, unit = f/1e9, l.Billion
	case a >= 1_000_000:
		f, unit = f/1e6, l.Million
	case a >= 1_000:
		f, unit = f/1e3, l.Thousand
	default:
		return strconv.FormatInt(n, 10)
	}
	s := strconv.FormatFloat(math.Round(f*10)/10, 'f', -1, 64)
	return strings.Replace(s, ".", l.Decimal, 1) + unit
}

// Grouped is n with the thousands grouped ("18,400"; "18.400").
func Grouped(n int64) string {
	s := strconv.FormatInt(n, 10)
	neg := strings.HasPrefix(s, "-")
	if neg {
		s = s[1:]
	}
	g := loc().Group
	for i := len(s) - 3; i > 0; i -= 3 {
		s = s[:i] + g + s[i:]
	}
	if neg {
		return "-" + s
	}
	return s
}

// ShortPath is p with home, when p is it or under it, as "~" ("~/src").
// An empty home leaves p.
func ShortPath(p, home string) string {
	home = strings.TrimSuffix(home, "/")
	if home == "" {
		return p
	}
	if p == home || strings.HasPrefix(p, home+"/") {
		return "~" + p[len(home):]
	}
	return p
}

// Clip is s without the space at its ends, cut to n characters with an
// ellipsis when it is longer.
func Clip(s string, n int) string {
	s = strings.TrimSpace(s)
	if r := []rune(s); len(r) > n {
		return string(r[:n]) + "…"
	}
	return s
}

// Date is t as a short date ("02/01/06" in Portuguese).
func Date(t time.Time) string { return t.Format(loc().Date) }

// DateTime is t's date and time ("02/01/2006 15:04" in Portuguese).
func DateTime(t time.Time) string { return t.Format(loc().DateTime) }

// Schedule is a schedule in words: "every <n><m|h|d>" ("A cada 5 minutos",
// "Toda semana") or a cron line that runs at a fixed time daily, on
// weekdays, at weekends, on one weekday or on a day of the month ("De
// segunda a sexta às 9h", "Every day at 9:30"). Anything else comes back as
// it was, and ok is false.
func Schedule(s string) (text string, ok bool) {
	f := strings.Fields(s)
	switch {
	case len(f) == 2 && f[0] == "every":
		if t := every(f[1]); t != "" {
			return t, true
		}
	case len(f) == 5 && f[3] == "*":
		if t := cron(f[0], f[1], f[2], f[4]); t != "" {
			return t, true
		}
	}
	return s, false
}

// every is "<n><m|h|d>" in words ("" when it is not one).
func every(d string) string {
	l := loc()
	if len(d) < 2 {
		return ""
	}
	n, err := strconv.Atoi(d[:len(d)-1])
	if err != nil || n <= 0 {
		return ""
	}
	count := func(one, many string) string {
		if n == 1 {
			return one
		}
		return l.Every + strconv.Itoa(n) + many
	}
	switch d[len(d)-1] {
	case 'm':
		if n%60 == 0 {
			return every(strconv.Itoa(n/60) + "h")
		}
		return count(l.EveryMinute, l.Minutes2)
	case 'h':
		if n%24 == 0 {
			return every(strconv.Itoa(n/24) + "d")
		}
		return count(l.EveryHour, l.Hours2)
	case 'd':
		switch {
		case n == 7:
			return l.EveryWeek
		case n%7 == 0:
			return l.Every + strconv.Itoa(n/7) + l.Weeks2
		}
		return count(l.EveryDay, l.Days2)
	}
	return ""
}

// cron is a cron line at a fixed time in words ("" otherwise).
func cron(min, hour, dom, dow string) string {
	l := loc()
	m, err1 := strconv.Atoi(min)
	h, err2 := strconv.Atoi(hour)
	if err1 != nil || err2 != nil || m < 0 || m > 59 || h < 0 || h > 23 {
		return ""
	}
	at := l.At + Clock(h, m)
	if dom != "*" {
		d, err := strconv.Atoi(dom)
		if err != nil || d < 1 || d > 31 || dow != "*" {
			return ""
		}
		return l.MonthDay + strconv.Itoa(d) + at
	}
	switch dow {
	case "*":
		return l.Daily + at
	case "1-5":
		return l.Weekdays + at
	case "0,6", "6,0", "6-7", "6,7":
		return l.Weekends + at
	}
	if d, err := strconv.Atoi(dow); err == nil && d >= 0 && d <= 7 {
		return l.Weekday[d%7] + at
	}
	return ""
}

// Clock is a time of day, h hours and m minutes: "9h" and "9h05" in
// Portuguese, "9:00" and "9:05" in English.
func Clock(h, m int) string {
	two := func(v int) string {
		if v < 10 {
			return "0" + strconv.Itoa(v)
		}
		return strconv.Itoa(v)
	}
	if loc().HourClock {
		if m == 0 {
			return strconv.Itoa(h) + "h"
		}
		return strconv.Itoa(h) + "h" + two(m)
	}
	return strconv.Itoa(h) + ":" + two(m)
}
