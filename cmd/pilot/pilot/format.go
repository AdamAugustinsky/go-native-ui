package pilot

import (
	"time"

	"gonativeui/ui"
)

// appendSize writes a file size the way Finder does: "932 B", "12.3 KB",
// "123 KB", "1.2 MB" (units of 1000). It allocates nothing.
func appendSize(f *ui.Fmt, n int64) {
	if n < 1000 {
		f.Int64(n).Str(" B")
		return
	}
	v := float64(n)
	for _, u := range [...]string{" KB", " MB", " GB", " TB", " PB"} {
		v /= 1000
		if v < 999.5 || u == " PB" {
			if v < 100 {
				f.Float(v, 1)
			} else {
				f.Float(v, 0)
			}
			f.Str(u)
			return
		}
	}
}

func pad2(f *ui.Fmt, v int) {
	if v < 10 {
		f.Byte('0')
	}
	f.Int(v)
}

// appendDate writes a modification time as "2026-10-17 14:03" in loc.
func appendDate(f *ui.Fmt, mtimeNs int64, loc *time.Location) {
	t := time.Unix(0, mtimeNs).In(loc)
	y, m, d := t.Date()
	h, mi, _ := t.Clock()
	f.Int(y).Byte('-')
	pad2(f, int(m))
	f.Byte('-')
	pad2(f, d)
	f.Byte(' ')
	pad2(f, h)
	f.Byte(':')
	pad2(f, mi)
}

// appendCount writes n with thousands separators: 1,234,567.
func appendCount(f *ui.Fmt, n int) {
	if n < 1000 {
		f.Int(n)
		return
	}
	var d [8]int // groups of three, least significant first
	k := 0
	for ; n >= 1000; n /= 1000 {
		d[k] = n % 1000
		k++
	}
	f.Int(n)
	for k--; k >= 0; k-- {
		f.Byte(',')
		f.Byte(byte('0' + d[k]/100))
		pad2(f, d[k]%100)
	}
}
