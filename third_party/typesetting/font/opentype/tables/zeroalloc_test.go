package tables

import "testing"

// A format with reserved bits set (0x01FF: 9 fields) must parse like
// upstream: every counted field is read, the 8 defined ones are used, and
// nothing indexes past the stack array (zeroalloc.patch regression).
func TestParseValueRecordReservedBits(t *testing.T) {
	// 4 placement/advance values, 4 device-table offsets of 0 (none), and
	// one value for reserved bit 8.
	data := []byte{1, 2, 3, 4, 5, 6, 7, 8, 0, 0, 0, 0, 0, 0, 0, 0, 0xAB, 0xCD}
	out, end, err := parseValueRecord(ValueFormat(0x01FF), data, 0)
	if err != nil {
		t.Fatal(err)
	}
	if end != 18 {
		t.Fatalf("consumed up to %d, want 18", end)
	}
	if out.XPlacement != 0x0102 || out.YPlacement != 0x0304 || out.XAdvance != 0x0506 || out.YAdvance != 0x0708 {
		t.Fatalf("values %+v", out)
	}
	all := make([]byte, 32)
	if _, end, err := parseValueRecord(ValueFormat(0xFFFF), all, 0); err != nil || end != 32 {
		t.Fatalf("all 16 bits: end %d err %v", end, err)
	}
	if allocs := testing.AllocsPerRun(100, func() { parseValueRecord(ValueFormat(0x000F), data, 0) }); allocs != 0 {
		t.Fatalf("parseValueRecord allocates %v per call", allocs)
	}
}
