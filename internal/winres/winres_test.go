package winres

import (
	"bytes"
	"testing"
)

func TestVersionRoundTrip(t *testing.T) {
	v := Version{File: [4]uint16{1, 2, 3, 4}, Product: [4]uint16{1, 2, 3, 0},
		Strings: []KV{{"CompanyName", "A3 Lab"}, {"ProductVersion", "1.2.3"}, {"OriginalFilename", "HyperPédia.exe"}}}
	got, err := ParseVersion(v.Encode())
	if err != nil {
		t.Fatal(err)
	}
	if got.File != v.File || got.Product != v.Product || len(got.Strings) != 3 || got.String("OriginalFilename") != "HyperPédia.exe" {
		t.Errorf("round trip: %+v", got)
	}
}

func TestParseQuad(t *testing.T) {
	if q, err := ParseQuad("2.3"); err != nil || q != [4]uint16{2, 3, 0, 0} {
		t.Errorf("ParseQuad(2.3) = %v, %v", q, err)
	}
	for _, bad := range []string{"", "1.2.3.4.5", "65536", "1.x", "1..2"} {
		if _, err := ParseQuad(bad); err == nil {
			t.Errorf("ParseQuad(%q) accepted", bad)
		}
	}
}

// TestMalformedInputIsAnError feeds truncated and corrupted data to the
// parsers, which must fail without a panic.
func TestMalformedInputIsAnError(t *testing.T) {
	enc := Version{Strings: []KV{{"A", "b"}}}.Encode()
	for i := 0; i < len(enc); i++ {
		ParseVersion(enc[:i])
		c := bytes.Clone(enc)
		c[i] ^= 0xFF
		ParseVersion(c)
	}
	obj, err := COFF(MachineAMD64, []Resource{{TypeManifest, 1, LangEnUS, []byte("<x/>")}})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := Read(bytes.NewReader(obj), int64(len(obj))); err == nil {
		t.Error("a COFF object read as a PE file")
	}
	if _, err := COFF(MachineAMD64, []Resource{{1, 1, 1, nil}, {1, 1, 1, nil}}); err == nil {
		t.Error("duplicate resources accepted")
	}
}

// TestSectionLayout reads back the resource tree the COFF writer lays out,
// with the section at address 0 (where the relocations would add the
// section's address).
func TestSectionLayout(t *testing.T) {
	in := []Resource{{TypeVersion, 1, LangEnUS, []byte("ver")}, {TypeIcon, 2, LangEnUS, []byte("i2")},
		{TypeIcon, 1, LangEnUS, []byte("i1")}, {TypeManifest, 1, LangEnUS, []byte("man")}}
	sec, relocs, err := section(in)
	if err != nil {
		t.Fatal(err)
	}
	if len(relocs) != len(in) {
		t.Errorf("%d relocations for %d resources", len(relocs), len(in))
	}
	out, err := walk(sec, 0, 0)
	if err != nil {
		t.Fatal(err)
	}
	for _, r := range in {
		got, ok := Find(out, r.Type, r.ID)
		if !ok || !bytes.Equal(got.Data, r.Data) || got.Lang != LangEnUS {
			t.Errorf("resource %d/%d: %+v", r.Type, r.ID, got)
		}
	}
}

func TestFiles(t *testing.T) {
	v := Version{Strings: []KV{{Key: FilesKey, Value: "ghostty-vt.dll|ghostty-vt.LICENSE.txt"}}}
	got, err := Files(v)
	if err != nil || len(got) != 2 || got[0] != "ghostty-vt.dll" || got[1] != "ghostty-vt.LICENSE.txt" {
		t.Errorf("Files = %q, %v", got, err)
	}
	if got, err := Files(Version{}); err != nil || got != nil {
		t.Errorf("Files of none = %q, %v", got, err)
	}
	for _, bad := range []string{"a.exe", `sub\a.dll`, "../a.dll", "a.dll|A.DLL", "a.dll.", ".dll", "a.so", "a.dll|"} {
		if _, err := Files(Version{Strings: []KV{{Key: FilesKey, Value: bad}}}); err == nil {
			t.Errorf("Files accepted %q", bad)
		}
	}
}
