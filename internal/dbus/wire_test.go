package dbus

import (
	"bytes"
	"encoding/binary"
	"reflect"
	"strings"
	"testing"
)

func enc(t *testing.T, sig string, args ...any) []byte {
	t.Helper()
	var e encoder
	if err := e.values(sig, args); err != nil {
		t.Fatalf("encode %q: %v", sig, err)
	}
	return e.b
}

// Byte-exact encodings worked out from the specification's alignment rules.
func TestEncodeGolden(t *testing.T) {
	u32 := func(v uint32) []byte { return binary.LittleEndian.AppendUint32(nil, v) }
	cat := func(parts ...[]byte) []byte { return bytes.Join(parts, nil) }
	for _, c := range []struct {
		name string
		sig  string
		args []any
		want []byte
	}{
		{"string then uint32", "su", []any{"hi", uint32(7)},
			cat(u32(2), []byte("hi\x00"), []byte{0}, u32(7))},
		{"array of structs starts on 8", "a(yu)", []any{[]any{Struct{byte(1), uint32(2)}}},
			cat(u32(8), u32(0), []byte{1, 0, 0, 0}, u32(2))},
		{"empty array still pads", "a(yu)", []any{[]any{}}, cat(u32(0), u32(0))},
		{"variant", "v", []any{V(uint32(5))}, []byte{1, 'u', 0, 0, 5, 0, 0, 0}},
		{"bool and byte", "by", []any{true, byte(9)}, []byte{1, 0, 0, 0, 9}},
		{"int16 then int64", "nx", []any{int16(-2), int64(1)},
			cat([]byte{0xfe, 0xff, 0, 0, 0, 0, 0, 0}, []byte{1, 0, 0, 0, 0, 0, 0, 0})},
		{"signature", "g", []any{Signature("a{sv}")}, []byte{5, 'a', '{', 's', 'v', '}', 0}},
		{"byte array", "ay", []any{[]byte{1, 2, 3}}, cat(u32(3), []byte{1, 2, 3})},
		{"dict entry", "a{su}", []any{[]any{DictEntry{"k", uint32(1)}}},
			cat(u32(12), u32(0), u32(1), []byte("k\x00"), []byte{0, 0}, u32(1))},
	} {
		if got := enc(t, c.sig, c.args...); !bytes.Equal(got, c.want) {
			t.Errorf("%s: got % x\nwant % x", c.name, got, c.want)
		}
	}
}

// The Hello call as every D-Bus client sends it (header length 0x6e).
func TestHelloGolden(t *testing.T) {
	m := &Message{Type: TypeMethodCall, Serial: 1, Destination: busName, Path: busPath, Interface: busName, Member: "Hello"}
	got, err := m.Marshal()
	if err != nil {
		t.Fatal(err)
	}
	str := func(code, sig byte, s string) []byte {
		b := []byte{code, 1, sig, 0, byte(len(s)), 0, 0, 0}
		return append(append(b, s...), 0)
	}
	pad := func(n int) []byte { return make([]byte, n) }
	want := bytes.Join([][]byte{
		{'l', 1, 0, 1, 0, 0, 0, 0, 1, 0, 0, 0, 0x6e, 0, 0, 0},
		str(1, 'o', "/org/freedesktop/DBus"), pad(2),
		str(6, 's', "org.freedesktop.DBus"), pad(3),
		str(2, 's', "org.freedesktop.DBus"), pad(3),
		str(3, 's', "Hello"), pad(2),
	}, nil)
	if !bytes.Equal(got, want) {
		t.Fatalf("got\n% x\nwant\n% x", got, want)
	}
	back, err := parseMessage(got)
	if err != nil || !reflect.DeepEqual(back, m) {
		t.Fatalf("round trip: %+v, %v", back, err)
	}
}

func TestRoundTrip(t *testing.T) {
	for _, c := range []struct {
		sig  string
		args []any
	}{
		{"ybnqiuxtd", []any{byte(1), true, int16(-3), uint16(4), int32(-5), uint32(6), int64(-7), uint64(8), 1.5}},
		{"sog", []any{"héllo", ObjectPath("/a/b"), Signature("a{sv}")}},
		{"as", []any{[]any{"a", "bc", ""}}},
		{"a{sv}", []any{[]any{
			DictEntry{"one", V(uint32(1))},
			DictEntry{"two", V("deux")},
			DictEntry{"three", Variant{"(ddd)", Struct{0.1, 0.2, 0.3}}},
			DictEntry{"nested", V(V(int32(-1)))},
		}}},
		{"(ddd)", []any{Struct{1.0, 0.5, 0.25}}},
		{"a(sa{sv})", []any{[]any{Struct{"x", []any{DictEntry{"k", V(true)}}}, Struct{"y", []any{}}}}},
		{"aay", []any{[]any{[]byte{1}, []byte{}, []byte{2, 3}}}},
		{"v", []any{Variant{"ai", []any{int32(1), int32(2)}}}},
	} {
		b := enc(t, c.sig, c.args...)
		d := &decoder{b: b}
		got, err := d.values(c.sig)
		if err != nil {
			t.Errorf("%s: %v", c.sig, err)
			continue
		}
		if d.off != len(b) {
			t.Errorf("%s: consumed %d of %d bytes", c.sig, d.off, len(b))
		}
		// The decoder returns []byte for ay and Struct for structs.
		if !reflect.DeepEqual(got, c.args) {
			t.Errorf("%s: got %#v\nwant %#v", c.sig, got, c.args)
		}
	}
	// []string is accepted when sending an array of strings.
	if a, b := enc(t, "as", []string{"a", "bc"}), enc(t, "as", []any{"a", "bc"}); !bytes.Equal(a, b) {
		t.Error("[]string and []any encode differently")
	}
}

func TestEncodeErrors(t *testing.T) {
	for name, c := range map[string]struct {
		sig  string
		args []any
	}{
		"wrong type":        {"u", []any{int32(1)}},
		"too few":           {"uu", []any{uint32(1)}},
		"too many":          {"u", []any{uint32(1), uint32(2)}},
		"bad signature":     {"a", []any{[]any{}}},
		"empty struct":      {"()", []any{Struct{}}},
		"dict outside a":    {"{su}", []any{DictEntry{"a", uint32(1)}}},
		"bad path":          {"o", []any{ObjectPath("a//b")}},
		"NUL in string":     {"s", []any{"a\x00b"}},
		"bad UTF-8":         {"s", []any{"\xff"}},
		"struct arity":      {"(uu)", []any{Struct{uint32(1)}}},
		"variant two types": {"v", []any{Variant{"uu", uint32(1)}}},
	} {
		var e encoder
		if err := e.values(c.sig, c.args); err == nil {
			t.Errorf("%s: no error", name)
		}
	}
	deep := strings.Repeat("a", 40) + "y"
	var e encoder
	if err := e.values(deep, []any{nil}); err == nil {
		t.Error("40 nested arrays accepted")
	}
}

func decode(sig string, b []byte) error {
	_, err := (&decoder{b: b}).values(sig)
	return err
}

// Hostile input: every length is checked against what is left, padding must
// be zero, nesting is bounded, and nothing panics.
func TestDecodeBounds(t *testing.T) {
	u32 := func(v uint32) []byte { return binary.LittleEndian.AppendUint32(nil, v) }
	for name, c := range map[string]struct {
		sig string
		b   []byte
	}{
		"string longer than the message": {"s", append(u32(1<<30), 'a', 0)},
		"string length wraps":            {"s", append(u32(0xffffffff), 'a', 0)},
		"string without NUL":             {"s", append(u32(1), 'a', 'b')},
		"string with embedded NUL":       {"s", append(u32(2), 'a', 0, 0)},
		"string not UTF-8":               {"s", append(u32(1), 0xff, 0)},
		"array longer than the message":  {"ai", append(u32(1<<30), 1, 0, 0, 0)},
		"array over MaxMessage":          {"ay", u32(MaxMessage + 1)},
		"array element overruns":         {"a(yu)", append(u32(5), make([]byte, 12)...)},
		"array of strings, odd length":   {"as", append(u32(3), 1, 0, 0)},
		"boolean 2":                      {"b", u32(2)},
		"padding not zero":               {"yu", []byte{1, 1, 0, 0, 5, 0, 0, 0}},
		"variant with a bad signature":   {"v", []byte{1, '(', 0, 0}},
		"variant with two types":         {"v", []byte{2, 'u', 'u', 0, 0, 0, 0, 0}},
		"signature not terminated":       {"g", []byte{1, 'u', 'x'}},
		"bad object path":                {"o", append(append(u32(3), "a/b"...), 0)},
		"truncated uint64":               {"t", []byte{1, 2, 3}},
		"empty input for a value":        {"u", nil},
	} {
		if err := decode(c.sig, c.b); err == nil {
			t.Errorf("%s: no error", name)
		}
	}
	// Variants nested deeper than the limit.
	deep := bytes.Repeat([]byte{1, 'v', 0}, 100)
	if err := decode("v", deep); err == nil {
		t.Error("100 nested variants accepted")
	}
	// A claimed length is not allocated before it is checked.
	huge := append(u32(MaxMessage), make([]byte, 16)...)
	if n := testing.AllocsPerRun(10, func() { decode("ai", huge) }); n > 4 {
		t.Errorf("rejecting a huge array allocates %v times", n)
	}
}

// Every prefix and every single-byte corruption of a real message is decoded
// without panicking (most are errors).
func TestParseMessageNeverPanics(t *testing.T) {
	m := &Message{Type: TypeSignal, Serial: 9, Path: "/org/freedesktop/portal/desktop", Interface: "org.freedesktop.portal.Settings",
		Member: "SettingChanged", Sender: ":1.5", Signature: "ssv",
		Body: []any{"org.freedesktop.appearance", "accent-color", Variant{"(ddd)", Struct{0.1, 0.2, 0.3}}}}
	b, err := m.Marshal()
	if err != nil {
		t.Fatal(err)
	}
	if back, err := parseMessage(b); err != nil || !reflect.DeepEqual(back, m) {
		t.Fatalf("round trip: %+v %v", back, err)
	}
	for n := 0; n < len(b); n++ {
		if _, err := parseMessage(b[:n]); err == nil {
			t.Errorf("prefix of %d bytes parsed", n)
		}
	}
	for i := range b {
		for _, x := range []byte{0x00, 0x01, 0x7f, 0xff} {
			c := append([]byte(nil), b...)
			c[i] = x
			parseMessage(c)
		}
	}
}

func TestReadMessageLimits(t *testing.T) {
	hdr := func(body, fields uint32) []byte {
		b := []byte{'l', 4, 0, 1}
		b = binary.LittleEndian.AppendUint32(b, body)
		b = binary.LittleEndian.AppendUint32(b, 1)
		return binary.LittleEndian.AppendUint32(b, fields)
	}
	for name, b := range map[string][]byte{
		"body over the limit":   hdr(MaxMessage+1, 0),
		"fields over the limit": hdr(0, maxFields+1),
		"big endian":            append([]byte{'B'}, hdr(0, 0)[1:]...),
		"protocol version 2":    append([]byte{'l', 4, 0, 2}, hdr(0, 0)[4:]...),
	} {
		if _, err := ReadMessage(bytes.NewReader(b)); err == nil {
			t.Errorf("%s: accepted", name)
		}
	}
}

func TestParseAddress(t *testing.T) {
	for in, want := range map[string][2]string{
		"unix:path=/run/user/1000/bus":                  {"unix", "/run/user/1000/bus"},
		"unix:path=/tmp/dbus-AbC,guid=0123456789abcdef": {"unix", "/tmp/dbus-AbC"},
		"unix:abstract=/tmp/dbus-xyz,guid=1":            {"unix", "@/tmp/dbus-xyz"},
		"unix:path=/tmp/with%20space":                   {"unix", "/tmp/with space"},
		"unix:guid=1,path=/x":                           {"unix", "/x"},
	} {
		n, a, err := parseAddress(in)
		if err != nil || n != want[0] || a != want[1] {
			t.Errorf("%q: %q %q %v, want %v", in, n, a, err, want)
		}
	}
	for _, in := range []string{"", "tcp:host=x,port=1", "unix:", "unix:path=%zz", "unix:path=%4", "systemd:"} {
		if _, _, err := parseAddress(in); err == nil {
			t.Errorf("%q: accepted", in)
		}
	}
}

// parseHeader reads the same fields as parseMessage, and neither panics on
// damaged input.
func TestParseHeaderAgreesWithParseMessage(t *testing.T) {
	msgs := []*Message{
		{Type: TypeSignal, Serial: 9, Path: "/org/freedesktop/portal/desktop", Interface: "org.freedesktop.portal.Settings",
			Member: "SettingChanged", Sender: ":1.5", Signature: "ssv",
			Body: []any{"org.freedesktop.appearance", "color-scheme", V(uint32(1))}},
		{Type: TypeMethodReturn, Serial: 3, ReplySerial: 2, Destination: ":1.7", Signature: "s", Body: []any{"x"}},
		{Type: TypeError, Serial: 4, ReplySerial: 2, ErrorName: "org.example.Error", Destination: ":1.7"},
		{Type: TypeMethodCall, Serial: 5, Path: "/", Interface: "a.b", Member: "M", Destination: "a.b", Flags: FlagNoReplyExpected},
	}
	for _, m := range msgs {
		b, err := m.Marshal()
		if err != nil {
			t.Fatal(err)
		}
		var h header
		if !parseHeader(b, &h) {
			t.Fatalf("%v: parseHeader refused a valid message", m.Type)
		}
		if h.typ != m.Type || h.flags != m.Flags || h.serial != m.Serial || h.replySerial != m.ReplySerial ||
			string(h.path) != string(m.Path) || string(h.iface) != m.Interface || string(h.member) != m.Member ||
			string(h.errName) != m.ErrorName || string(h.dest) != m.Destination || string(h.sender) != m.Sender || string(h.sig) != m.Signature {
			t.Errorf("%v: header %+v does not match %+v", m.Type, h, m)
		}
		for i := range b {
			for _, x := range []byte{0x00, 0x01, 0x7f, 0xff} {
				c := append([]byte(nil), b...)
				c[i] = x
				parseHeader(c, &h)
			}
		}
		for n := 0; n < len(b); n++ {
			if parseHeader(b[:n], &h) {
				t.Errorf("%v: prefix of %d bytes accepted", m.Type, n)
			}
		}
	}
	// A header field the fast reader does not know sends the message down the
	// general path (which ignores it).
	b, _ := (&Message{Type: TypeSignal, Serial: 1, Path: "/a", Interface: "a.b", Member: "M"}).Marshal()
	var h header
	if !parseHeader(b, &h) {
		t.Fatal("plain signal refused")
	}
}

func TestBodyReader(t *testing.T) {
	body := enc(t, "ssv", "org.freedesktop.appearance", "accent-color", Variant{"(ddd)", Struct{0.25, 0.5, 1.0}})
	r := BodyReader{b: body}
	if string(r.Str()) != "org.freedesktop.appearance" || string(r.Str()) != "accent-color" || string(r.Variant()) != "(ddd)" {
		t.Fatal("strings")
	}
	r.Struct()
	if a, b, c := r.Float64(), r.Float64(), r.Float64(); a != 0.25 || b != 0.5 || c != 1 || !r.Ok() || r.off != len(body) {
		t.Fatalf("floats %v %v %v ok=%v off=%d/%d", a, b, c, r.Ok(), r.off, len(body))
	}
	// Every truncation and corruption fails cleanly.
	for n := 0; n < len(body); n++ {
		r := BodyReader{b: body[:n]}
		r.Str()
		r.Str()
		r.Variant()
		r.Struct()
		r.Float64()
		r.Float64()
		r.Float64()
		if r.Ok() {
			t.Errorf("truncation to %d bytes read fine", n)
		}
	}
	for name, b := range map[string][]byte{
		"string longer than the body": append(binary.LittleEndian.AppendUint32(nil, 1<<30), 'a', 0),
		"string without NUL":          append(binary.LittleEndian.AppendUint32(nil, 1), 'a', 'b'),
		"string with NUL":             append(binary.LittleEndian.AppendUint32(nil, 2), 'a', 0, 0),
		"string not UTF-8":            append(binary.LittleEndian.AppendUint32(nil, 1), 0xff, 0),
	} {
		r := BodyReader{b: b}
		if r.Str(); r.Ok() {
			t.Errorf("%s: accepted", name)
		}
	}
	r = BodyReader{b: []byte{1, 1, 0, 0, 5, 0, 0, 0}} // padding that is not zero before the u32
	r.byte()
	if r.Uint32(); r.Ok() {
		t.Error("non-zero padding accepted")
	}
}
