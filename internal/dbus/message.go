package dbus

import (
	"encoding/binary"
	"errors"
	"fmt"
	"io"
)

// MessageType is the message kind in the header.
type MessageType byte

// Message kinds.
const (
	TypeMethodCall   MessageType = 1
	TypeMethodReturn MessageType = 2
	TypeError        MessageType = 3
	TypeSignal       MessageType = 4
)

// Header flags.
const (
	FlagNoReplyExpected byte = 1
	FlagNoAutoStart     byte = 2
)

// Header field codes.
const (
	fieldPath        = 1
	fieldInterface   = 2
	fieldMember      = 3
	fieldErrorName   = 4
	fieldReplySerial = 5
	fieldDestination = 6
	fieldSender      = 7
	fieldSignature   = 8
	fieldUnixFDs     = 9
)

// Message is one D-Bus message.
type Message struct {
	Type   MessageType
	Flags  byte
	Serial uint32

	Path        ObjectPath
	Interface   string
	Member      string
	ErrorName   string
	ReplySerial uint32 // 0 when absent: serials start at 1
	Destination string
	Sender      string
	Signature   string

	// Body holds the arguments, decoded by Signature.
	Body []any
}

// Error is a D-Bus error reply (or the error a Handler returns).
type Error struct {
	Name string
	Body []any
}

func (e *Error) Error() string {
	if len(e.Body) > 0 {
		if s, ok := e.Body[0].(string); ok && s != "" {
			return e.Name + ": " + s
		}
	}
	return e.Name
}

// Marshal encodes m. m.Serial must be set.
func (m *Message) Marshal() ([]byte, error) {
	var body encoder
	if err := body.values(m.Signature, m.Body); err != nil {
		return nil, err
	}
	if len(body.b) > MaxMessage {
		return nil, errors.New("dbus: message too large")
	}
	var fields []any
	add := func(code byte, sig string, v any) {
		fields = append(fields, Struct{code, Variant{sig, v}})
	}
	if m.Path != "" {
		add(fieldPath, "o", m.Path)
	}
	if m.Destination != "" {
		add(fieldDestination, "s", m.Destination)
	}
	if m.Interface != "" {
		add(fieldInterface, "s", m.Interface)
	}
	if m.Member != "" {
		add(fieldMember, "s", m.Member)
	}
	if m.ErrorName != "" {
		add(fieldErrorName, "s", m.ErrorName)
	}
	if m.ReplySerial != 0 {
		add(fieldReplySerial, "u", m.ReplySerial)
	}
	if m.Sender != "" {
		add(fieldSender, "s", m.Sender)
	}
	if m.Signature != "" {
		add(fieldSignature, "g", Signature(m.Signature))
	}
	e := encoder{b: make([]byte, 0, 128+len(body.b))}
	e.b = append(e.b, 'l', byte(m.Type), m.Flags, 1)
	e.u32(uint32(len(body.b)))
	e.u32(m.Serial)
	if fields == nil {
		fields = []any{}
	}
	if err := e.one("a(yv)", fields, 0); err != nil {
		return nil, err
	}
	e.align(8)
	e.b = append(e.b, body.b...)
	return e.b, nil
}

// ReadMessage reads and decodes one message, refusing anything over
// [MaxMessage]. It allocates the message, its strings and its containers; a
// [Conn] reads its own traffic with [readFrame] instead and takes the
// allocation-free path for the signals subscribed with [Conn.SubscribeSignal].
func ReadMessage(r io.Reader) (*Message, error) {
	buf, err := readFrame(r, nil)
	if err != nil {
		return nil, err
	}
	return parseMessage(buf)
}

// readFrame reads the bytes of one message. They go into scratch when the
// message fits (the caller reuses scratch, so the result is valid until the
// next call), and into a new slice when it does not.
func readFrame(r io.Reader, scratch []byte) ([]byte, error) {
	if cap(scratch) < 16 {
		scratch = make([]byte, 16)
	}
	fixed := scratch[:16]
	if _, err := io.ReadFull(r, fixed); err != nil {
		return nil, err
	}
	if fixed[0] != 'l' {
		return nil, errors.New("dbus: big-endian messages are not supported")
	}
	if fixed[3] != 1 {
		return nil, fmt.Errorf("dbus: protocol version %d", fixed[3])
	}
	bodyLen := binary.LittleEndian.Uint32(fixed[4:])
	fieldsLen := binary.LittleEndian.Uint32(fixed[12:])
	if fieldsLen > maxFields || bodyLen > MaxMessage {
		return nil, fmt.Errorf("dbus: message too large (fields %d, body %d)", fieldsLen, bodyLen)
	}
	hdr := (16 + int(fieldsLen) + 7) &^ 7
	total := hdr + int(bodyLen)
	buf := scratch[:cap(scratch)]
	if total > len(buf) {
		buf = make([]byte, total)
		copy(buf, fixed)
	}
	buf = buf[:total]
	if _, err := io.ReadFull(r, buf[16:]); err != nil {
		return nil, err
	}
	return buf, nil
}

// parseMessage decodes a whole message held in buf (whose lengths ReadMessage
// ReadMessage has already bounded).
func parseMessage(buf []byte) (*Message, error) {
	if len(buf) < 16 {
		return nil, errShort
	}
	m := &Message{Type: MessageType(buf[1]), Flags: buf[2], Serial: binary.LittleEndian.Uint32(buf[8:])}
	if m.Type < TypeMethodCall || m.Type > TypeSignal || m.Serial == 0 {
		return nil, errors.New("dbus: invalid message header")
	}
	bodyLen := int(binary.LittleEndian.Uint32(buf[4:]))
	d := &decoder{b: buf, off: 12}
	fv, err := d.one("a(yv)", 0)
	if err != nil {
		return nil, err
	}
	if err := d.align(8); err != nil {
		return nil, err
	}
	if len(buf)-d.off != bodyLen {
		return nil, errors.New("dbus: body length does not match the message")
	}
	for _, f := range fv.([]any) {
		st := f.(Struct)
		code, v := st[0].(byte), st[1].(Variant)
		var ok bool
		switch code {
		case fieldPath:
			var p ObjectPath
			p, ok = v.Val.(ObjectPath)
			m.Path = p
		case fieldInterface:
			m.Interface, ok = v.Val.(string)
		case fieldMember:
			m.Member, ok = v.Val.(string)
		case fieldErrorName:
			m.ErrorName, ok = v.Val.(string)
		case fieldReplySerial:
			m.ReplySerial, ok = v.Val.(uint32)
		case fieldDestination:
			m.Destination, ok = v.Val.(string)
		case fieldSender:
			m.Sender, ok = v.Val.(string)
		case fieldSignature:
			var s Signature
			s, ok = v.Val.(Signature)
			m.Signature = string(s)
		case fieldUnixFDs:
			return nil, errors.New("dbus: file descriptors are not supported")
		default:
			ok = true // unknown fields are ignored
		}
		if !ok {
			return nil, fmt.Errorf("dbus: header field %d has the wrong type", code)
		}
	}
	if bodyLen > 0 || m.Signature != "" {
		bd := &decoder{b: buf, off: d.off}
		if m.Body, err = bd.values(m.Signature); err != nil {
			return nil, err
		}
		if bd.off != len(buf) {
			return nil, errors.New("dbus: trailing bytes in the body")
		}
	}
	switch m.Type {
	case TypeMethodCall:
		if m.Path == "" || m.Member == "" {
			return nil, errors.New("dbus: method call without path or member")
		}
	case TypeSignal:
		if m.Path == "" || m.Interface == "" || m.Member == "" {
			return nil, errors.New("dbus: signal without path, interface or member")
		}
	case TypeError:
		if m.ErrorName == "" || m.ReplySerial == 0 {
			return nil, errors.New("dbus: error without name or reply serial")
		}
	case TypeMethodReturn:
		if m.ReplySerial == 0 {
			return nil, errors.New("dbus: reply without reply serial")
		}
	}
	return m, nil
}
