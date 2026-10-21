// Package abi implements the value encoding used to pass arguments to
// and results from functions exported by starc-compiled modules.
//
// Every value is a one-byte tag followed by a payload; all integers are
// little-endian:
//
//	0 None
//	1 False
//	2 True
//	3 int     int64
//	4 float   float64 (IEEE 754 bits)
//	5 string  uint32 length, UTF-8 bytes
//	6 list    uint32 count, values
//	7 tuple   uint32 count, values
//	8 dict    uint32 count, key/value pairs
//	9 struct  uint32 count, fields: uint32 name length, name bytes, value
//	10 opaque uint8 flags, then type name, string form and payload,
//	          each as uint32 length and bytes
//
// The arguments of a call are an encoded tuple of positional arguments,
// optionally followed by an encoded dict of keyword arguments.
//
// Go values map to Starlark values as follows: nil ↔ None, bool ↔ bool,
// signed and unsigned integers → int (decoded as int64), float32 and
// float64 → float (decoded as float64), string ↔ string, []any ↔ list,
// [Tuple] ↔ tuple, *[Dict] ↔ dict, *[Struct] ↔ struct, *[Opaque] ↔ an
// opaque host value. Maps with string keys are encoded as dicts with
// sorted keys.
package abi

import (
	"encoding/binary"
	"errors"
	"fmt"
	"math"
	"reflect"
	"sort"
)

// Tags of encoded values.
const (
	TagNone   = 0
	TagFalse  = 1
	TagTrue   = 2
	TagInt    = 3
	TagFloat  = 4
	TagStr    = 5
	TagList   = 6
	TagTuple  = 7
	TagDict   = 8
	TagStruct = 9
	TagOpaque = 10
)

// Flags of an encoded [Opaque] value.
const (
	OpaqueFalsy      = 1
	OpaqueUnhashable = 2
)

// Struct is a Starlark struct: immutable named fields, as created by
// starlarkstruct in go.starlark.net. Decoded structs have their fields
// sorted by name; field names must be unique.
type Struct struct {
	Fields []Field
}

// Field is a named field of a [Struct].
type Field struct {
	Name  string
	Value any
}

// NewStruct returns a struct with the given alternating names and
// values, sorted by name.
func NewStruct(kv ...any) *Struct {
	s := &Struct{}
	for i := 0; i+1 < len(kv); i += 2 {
		s.Fields = append(s.Fields, Field{kv[i].(string), kv[i+1]})
	}
	sort.SliceStable(s.Fields, func(i, j int) bool { return s.Fields[i].Name < s.Fields[j].Name })
	return s
}

// Get returns the value of the named field.
func (s *Struct) Get(name string) (any, bool) {
	for _, f := range s.Fields {
		if f.Name == name {
			return f.Value, true
		}
	}
	return nil, false
}

// Opaque is a host value that a program can hold, compare, hash and pass
// back, but not look into, such as an action or a handle. Two opaque
// values are equal if their types and payloads are equal.
type Opaque struct {
	Type       string // the result of type(v)
	Repr       string // the result of str(v) and repr(v)
	Payload    []byte // data for the host only
	Falsy      bool   // v is false in a boolean context (it is true by default)
	Unhashable bool   // v cannot be a dict key or set element
}

// Tuple is a Starlark tuple.
type Tuple []any

// Entry is a key/value pair of a [Dict].
type Entry struct {
	Key, Value any
}

// Dict is an insertion-ordered Starlark dict.
type Dict struct {
	Entries []Entry
}

// NewDict returns a dict with the given alternating keys and values.
func NewDict(kv ...any) *Dict {
	d := &Dict{}
	for i := 0; i+1 < len(kv); i += 2 {
		d.Set(kv[i], kv[i+1])
	}
	return d
}

// Len returns the number of entries.
func (d *Dict) Len() int { return len(d.Entries) }

// Get returns the value for key.
func (d *Dict) Get(key any) (any, bool) {
	for _, e := range d.Entries {
		if reflect.DeepEqual(e.Key, key) {
			return e.Value, true
		}
	}
	return nil, false
}

// Set adds or replaces an entry.
func (d *Dict) Set(key, value any) {
	for i, e := range d.Entries {
		if reflect.DeepEqual(e.Key, key) {
			d.Entries[i].Value = value
			return
		}
	}
	d.Entries = append(d.Entries, Entry{key, value})
}

// Encode encodes a single value.
func Encode(v any) ([]byte, error) { return Append(nil, v) }

// EncodeCall encodes call arguments: positional args and optional kwargs.
func EncodeCall(args []any, kwargs *Dict) ([]byte, error) {
	b, err := Append(nil, Tuple(args))
	if err != nil {
		return nil, err
	}
	if kwargs != nil && kwargs.Len() > 0 {
		for _, e := range kwargs.Entries {
			if _, ok := e.Key.(string); !ok {
				return nil, fmt.Errorf("abi: keyword argument name %v is not a string", e.Key)
			}
		}
		b, err = Append(b, kwargs)
	}
	return b, err
}

// Append appends the encoding of v to b.
func Append(b []byte, v any) ([]byte, error) {
	switch v := v.(type) {
	case nil:
		return append(b, TagNone), nil
	case bool:
		if v {
			return append(b, TagTrue), nil
		}
		return append(b, TagFalse), nil
	case int:
		return appendInt(b, int64(v)), nil
	case int8:
		return appendInt(b, int64(v)), nil
	case int16:
		return appendInt(b, int64(v)), nil
	case int32:
		return appendInt(b, int64(v)), nil
	case int64:
		return appendInt(b, v), nil
	case uint:
		return appendUint(b, uint64(v))
	case uint8:
		return appendInt(b, int64(v)), nil
	case uint16:
		return appendInt(b, int64(v)), nil
	case uint32:
		return appendInt(b, int64(v)), nil
	case uint64:
		return appendUint(b, v)
	case float32:
		return appendFloat(b, float64(v)), nil
	case float64:
		return appendFloat(b, v), nil
	case string:
		b = append(b, TagStr)
		b = binary.LittleEndian.AppendUint32(b, uint32(len(v)))
		return append(b, v...), nil
	case []any:
		return appendSeq(b, TagList, v)
	case Tuple:
		return appendSeq(b, TagTuple, v)
	case *Dict:
		return appendDict(b, v)
	case Dict:
		return appendDict(b, &v)
	case *Struct:
		return appendStruct(b, v)
	case Struct:
		return appendStruct(b, &v)
	case *Opaque:
		return appendOpaque(b, v), nil
	case Opaque:
		return appendOpaque(b, &v), nil
	case map[string]any:
		keys := make([]string, 0, len(v))
		for k := range v {
			keys = append(keys, k)
		}
		sort.Strings(keys)
		d := &Dict{}
		for _, k := range keys {
			d.Entries = append(d.Entries, Entry{k, v[k]})
		}
		return appendDict(b, d)
	}
	return nil, fmt.Errorf("abi: cannot encode Go value of type %T", v)
}

func appendInt(b []byte, v int64) []byte {
	b = append(b, TagInt)
	return binary.LittleEndian.AppendUint64(b, uint64(v))
}

func appendUint(b []byte, v uint64) ([]byte, error) {
	if v > math.MaxInt64 {
		return nil, fmt.Errorf("abi: integer %d overflows int64", v)
	}
	return appendInt(b, int64(v)), nil
}

func appendFloat(b []byte, v float64) []byte {
	b = append(b, TagFloat)
	return binary.LittleEndian.AppendUint64(b, math.Float64bits(v))
}

func appendSeq(b []byte, tag byte, xs []any) ([]byte, error) {
	b = append(b, tag)
	b = binary.LittleEndian.AppendUint32(b, uint32(len(xs)))
	var err error
	for _, x := range xs {
		if b, err = Append(b, x); err != nil {
			return nil, err
		}
	}
	return b, nil
}

func appendDict(b []byte, d *Dict) ([]byte, error) {
	b = append(b, TagDict)
	b = binary.LittleEndian.AppendUint32(b, uint32(len(d.Entries)))
	var err error
	for _, e := range d.Entries {
		if b, err = Append(b, e.Key); err != nil {
			return nil, err
		}
		if b, err = Append(b, e.Value); err != nil {
			return nil, err
		}
	}
	return b, nil
}

func appendStruct(b []byte, s *Struct) ([]byte, error) {
	b = append(b, TagStruct)
	b = binary.LittleEndian.AppendUint32(b, uint32(len(s.Fields)))
	var err error
	for _, f := range s.Fields {
		b = appendRaw(b, f.Name)
		if b, err = Append(b, f.Value); err != nil {
			return nil, err
		}
	}
	return b, nil
}

func appendOpaque(b []byte, o *Opaque) []byte {
	var flags byte
	if o.Falsy {
		flags |= OpaqueFalsy
	}
	if o.Unhashable {
		flags |= OpaqueUnhashable
	}
	b = append(b, TagOpaque, flags)
	b = appendRaw(b, o.Type)
	b = appendRaw(b, o.Repr)
	return appendRaw(b, string(o.Payload))
}

// appendRaw appends a uint32 length and the bytes of s.
func appendRaw(b []byte, s string) []byte {
	b = binary.LittleEndian.AppendUint32(b, uint32(len(s)))
	return append(b, s...)
}

// DecodeCall decodes call arguments encoded by [EncodeCall]: a tuple of
// positional arguments, optionally followed by a dict of keyword
// arguments with string keys. kwargs is nil if there are none.
func DecodeCall(b []byte) (args []any, kwargs *Dict, err error) {
	v, rest, err := DecodePrefix(b)
	if err != nil {
		return nil, nil, err
	}
	t, ok := v.(Tuple)
	if !ok {
		return nil, nil, fmt.Errorf("abi: arguments are a %T, want a tuple", v)
	}
	if len(rest) > 0 {
		if v, rest, err = DecodePrefix(rest); err != nil {
			return nil, nil, err
		}
		if kwargs, ok = v.(*Dict); !ok {
			return nil, nil, fmt.Errorf("abi: keyword arguments are a %T, want a dict", v)
		}
		for _, e := range kwargs.Entries {
			if _, ok := e.Key.(string); !ok {
				return nil, nil, fmt.Errorf("abi: keyword argument name %v is not a string", e.Key)
			}
		}
		if len(rest) != 0 {
			return nil, nil, fmt.Errorf("abi: %d trailing bytes", len(rest))
		}
	}
	return []any(t), kwargs, nil
}

// ErrTruncated reports an incomplete encoding.
var ErrTruncated = errors.New("abi: truncated value")

// Decode decodes a single value, which must span all of b.
func Decode(b []byte) (any, error) {
	v, rest, err := DecodePrefix(b)
	if err != nil {
		return nil, err
	}
	if len(rest) != 0 {
		return nil, fmt.Errorf("abi: %d trailing bytes", len(rest))
	}
	return v, nil
}

// raw reads a uint32 length and that many bytes.
func raw(b []byte) (string, []byte, bool) {
	if len(b) < 4 {
		return "", nil, false
	}
	n := binary.LittleEndian.Uint32(b)
	b = b[4:]
	if uint64(len(b)) < uint64(n) {
		return "", nil, false
	}
	return string(b[:n]), b[n:], true
}

// MaxDepth is the maximum nesting depth of lists, tuples and dicts that
// the decoder accepts, so that untrusted input cannot exhaust the stack.
const MaxDepth = 10000

// DecodePrefix decodes one value from the start of b and returns the
// remaining bytes.
func DecodePrefix(b []byte) (any, []byte, error) { return decodePrefix(b, 0) }

func decodePrefix(b []byte, depth int) (any, []byte, error) {
	if len(b) == 0 {
		return nil, nil, ErrTruncated
	}
	tag, b := b[0], b[1:]
	if tag == TagOpaque {
		if len(b) < 1 {
			return nil, nil, ErrTruncated
		}
		o := &Opaque{Falsy: b[0]&OpaqueFalsy != 0, Unhashable: b[0]&OpaqueUnhashable != 0}
		b = b[1:]
		var payload string
		var ok bool
		if o.Type, b, ok = raw(b); !ok {
			return nil, nil, ErrTruncated
		}
		if o.Repr, b, ok = raw(b); !ok {
			return nil, nil, ErrTruncated
		}
		if payload, b, ok = raw(b); !ok {
			return nil, nil, ErrTruncated
		}
		o.Payload = []byte(payload)
		return o, b, nil
	}
	switch tag {
	case TagNone:
		return nil, b, nil
	case TagFalse:
		return false, b, nil
	case TagTrue:
		return true, b, nil
	case TagInt, TagFloat:
		if len(b) < 8 {
			return nil, nil, ErrTruncated
		}
		u := binary.LittleEndian.Uint64(b)
		if tag == TagInt {
			return int64(u), b[8:], nil
		}
		return math.Float64frombits(u), b[8:], nil
	}
	if len(b) < 4 {
		return nil, nil, ErrTruncated
	}
	n := binary.LittleEndian.Uint32(b)
	b = b[4:]
	if tag != TagStr && depth >= MaxDepth {
		return nil, nil, fmt.Errorf("abi: values nested more than %d deep", MaxDepth)
	}
	switch tag {
	case TagStr:
		if uint64(len(b)) < uint64(n) {
			return nil, nil, ErrTruncated
		}
		return string(b[:n]), b[n:], nil
	case TagList, TagTuple:
		if uint64(len(b)) < uint64(n) {
			return nil, nil, ErrTruncated
		}
		xs := make([]any, n)
		for i := range xs {
			var err error
			if xs[i], b, err = decodePrefix(b, depth+1); err != nil {
				return nil, nil, err
			}
		}
		if tag == TagTuple {
			return Tuple(xs), b, nil
		}
		return xs, b, nil
	case TagStruct:
		if uint64(len(b)) < 5*uint64(n) {
			return nil, nil, ErrTruncated
		}
		s := &Struct{}
		for i := uint32(0); i < n; i++ {
			var f Field
			var ok bool
			if f.Name, b, ok = raw(b); !ok {
				return nil, nil, ErrTruncated
			}
			var err error
			if f.Value, b, err = decodePrefix(b, depth+1); err != nil {
				return nil, nil, err
			}
			s.Fields = append(s.Fields, f)
		}
		sort.SliceStable(s.Fields, func(i, j int) bool { return s.Fields[i].Name < s.Fields[j].Name })
		for i := 1; i < len(s.Fields); i++ {
			if s.Fields[i].Name == s.Fields[i-1].Name {
				return nil, nil, fmt.Errorf("abi: duplicate struct field %q", s.Fields[i].Name)
			}
		}
		return s, b, nil
	case TagDict:
		if uint64(len(b)) < 2*uint64(n) {
			return nil, nil, ErrTruncated
		}
		d := &Dict{}
		if n > 0 {
			d.Entries = make([]Entry, n)
		}
		for i := range d.Entries {
			var err error
			if d.Entries[i].Key, b, err = decodePrefix(b, depth+1); err != nil {
				return nil, nil, err
			}
			if d.Entries[i].Value, b, err = decodePrefix(b, depth+1); err != nil {
				return nil, nil, err
			}
		}
		return d, b, nil
	}
	return nil, nil, fmt.Errorf("abi: invalid tag %d", tag)
}
