package format

import (
	"testing"
	"time"
)

// in runs f with l in use, then puts English back.
func in(t *testing.T, l Locale, f func()) {
	t.Helper()
	SetLocale(l)
	defer SetLocale(English)
	f()
}

func TestAgo(t *testing.T) {
	now := time.Date(2026, 11, 2, 12, 0, 0, 0, time.UTC)
	cases := []struct {
		ago    time.Duration
		pt, en string
	}{
		{30 * time.Second, "agora", "now"},
		{5 * time.Minute, "5m", "5m"},
		{3*time.Hour + 59*time.Minute, "3h", "3h"},
		{2 * 24 * time.Hour, "2d", "2d"},
		{29 * 24 * time.Hour, "4sem", "4w"},
	}
	for _, c := range cases {
		in(t, PtBR, func() {
			if got := Ago(now, now.Add(-c.ago)); got != c.pt {
				t.Errorf("pt-BR Ago(%v) = %q, want %q", c.ago, got, c.pt)
			}
		})
		if got := Ago(now, now.Add(-c.ago)); got != c.en {
			t.Errorf("English Ago(%v) = %q, want %q", c.ago, got, c.en)
		}
	}
	if Ago(now, time.Time{}) != "" {
		t.Error("the zero time has an age")
	}
}

func TestNumbers(t *testing.T) {
	in(t, PtBR, func() {
		for n, want := range map[int64]string{950: "950", 12_345: "12,3 mil", 1_200_000: "1,2 mi", 2_000_000_000: "2 bi", -12_345: "-12,3 mil"} {
			if got := Compact(n); got != want {
				t.Errorf("pt-BR Compact(%d) = %q, want %q", n, got, want)
			}
		}
		if got := Grouped(18_400); got != "18.400" {
			t.Errorf("pt-BR Grouped = %q", got)
		}
		if got := Count(0, "arquivo", "arquivos"); got != "0 arquivo" {
			t.Errorf("pt-BR Count(0) = %q", got)
		}
	})
	if got := Compact(12_345); got != "12.3K" {
		t.Errorf("Compact = %q", got)
	}
	if got := Grouped(-1_234_567); got != "-1,234,567" {
		t.Errorf("Grouped = %q", got)
	}
	if got := Count(0, "file", "files"); got != "0 files" {
		t.Errorf("Count(0) = %q", got)
	}
}

func TestShortPath(t *testing.T) {
	for _, c := range [][3]string{
		{"/Users/ada/src", "/Users/ada", "~/src"},
		{"/Users/ada", "/Users/ada/", "~"},
		{"/Users/adam/src", "/Users/ada", "/Users/adam/src"}, // a prefix that is not the folder
		{"/opt/x", "", "/opt/x"},
	} {
		if got := ShortPath(c[0], c[1]); got != c[2] {
			t.Errorf("ShortPath(%q, %q) = %q, want %q", c[0], c[1], got, c[2])
		}
	}
}

func TestSchedule(t *testing.T) {
	pt := map[string]string{
		"every 1m":     "A cada minuto",
		"every 5m":     "A cada 5 minutos",
		"every 120m":   "A cada 2 horas",
		"every 168h":   "Toda semana",
		"every 14d":    "A cada 2 semanas",
		"every 3d":     "A cada 3 dias",
		"0 9 * * 1-5":  "De segunda a sexta às 9h",
		"30 9 * * *":   "Todo dia às 9h30",
		"5 18 * * 0,6": "Sábado e domingo às 18h05",
		"0 8 * * 7":    "Todo domingo às 8h",
		"0 8 15 * *":   "Todo dia 15 às 8h",
	}
	in(t, PtBR, func() {
		for s, want := range pt {
			if got, ok := Schedule(s); !ok || got != want {
				t.Errorf("pt-BR Schedule(%q) = %q, %v; want %q", s, got, ok, want)
			}
		}
	})
	if got, _ := Schedule("30 9 * * 1-5"); got != "Weekdays at 9:30" {
		t.Errorf("Schedule = %q", got)
	}
	for _, s := range []string{"*/5 * * * *", "every 0h", "0 9 * 1 *", "nonsense"} {
		if got, ok := Schedule(s); ok || got != s {
			t.Errorf("Schedule(%q) = %q, %v; want it kept", s, got, ok)
		}
	}
}

func TestDates(t *testing.T) {
	d := time.Date(2026, 11, 2, 15, 4, 0, 0, time.UTC)
	in(t, PtBR, func() {
		if Date(d) != "02/11/26" || DateTime(d) != "02/11/2026 15:04" {
			t.Errorf("pt-BR dates: %q %q", Date(d), DateTime(d))
		}
	})
	if Date(d) != "11/02/26" {
		t.Errorf("Date = %q", Date(d))
	}
}

func TestSetLocaleKeepsEnglish(t *testing.T) {
	in(t, Locale{Now: "jetzt"}, func() {
		now := time.Now()
		if Ago(now, now) != "jetzt" || Ago(now, now.Add(-2*time.Hour)) != "2h" {
			t.Error("a partial locale lost its own word or English's")
		}
	})
}

func TestClip(t *testing.T) {
	if got := Clip("  olá mundo  ", 3); got != "olá…" {
		t.Errorf("Clip = %q", got)
	}
	if got := Clip("curto", 10); got != "curto" {
		t.Errorf("Clip = %q", got)
	}
}
