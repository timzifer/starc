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
//
// The arguments of a call are an encoded tuple of positional arguments,
// optionally followed by an encoded dict of keyword arguments.
//
// Go values map to Starlark values as follows: nil ↔ None, bool ↔ bool,
// signed and unsigned integers → int (decoded as int64), float32 and
// float64 → float (decoded as float64), string ↔ string, []any ↔ list,
// [Tuple] ↔ tuple, *[Dict] ↔ dict. Maps with string keys are encoded as
// dicts with sorted keys.
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
	TagNone  = 0
	TagFalse = 1
	TagTrue  = 2
	TagInt   = 3
	TagFloat = 4
	TagStr   = 5
	TagList  = 6
	TagTuple = 7
	TagDict  = 8
)

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

// DecodePrefix decodes one value from the start of b and returns the
// remaining bytes.
func DecodePrefix(b []byte) (any, []byte, error) {
	if len(b) == 0 {
		return nil, nil, ErrTruncated
	}
	tag, b := b[0], b[1:]
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
			if xs[i], b, err = DecodePrefix(b); err != nil {
				return nil, nil, err
			}
		}
		if tag == TagTuple {
			return Tuple(xs), b, nil
		}
		return xs, b, nil
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
			if d.Entries[i].Key, b, err = DecodePrefix(b); err != nil {
				return nil, nil, err
			}
			if d.Entries[i].Value, b, err = DecodePrefix(b); err != nil {
				return nil, nil, err
			}
		}
		return d, b, nil
	}
	return nil, nil, fmt.Errorf("abi: invalid tag %d", tag)
}
