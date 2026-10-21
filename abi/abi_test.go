package abi

import (
	"bytes"
	"errors"
	"math"
	"reflect"
	"strings"
	"testing"
)

func TestRoundTrip(t *testing.T) {
	for _, v := range []any{
		nil, true, false, int64(0), int64(-1), int64(math.MaxInt64), 3.25, math.Inf(-1), "", "héllo",
		[]any{}, []any{int64(1), "x", nil}, Tuple{}, Tuple{1.5, Tuple{false}},
		NewDict(), NewDict("a", int64(1), int64(2), []any{"b"}),
	} {
		b, err := Encode(v)
		if err != nil {
			t.Fatalf("Encode(%#v): %v", v, err)
		}
		got, err := Decode(b)
		if err != nil {
			t.Fatalf("Decode(%#v): %v", v, err)
		}
		if !reflect.DeepEqual(got, v) {
			t.Errorf("round trip: got %#v, want %#v", got, v)
		}
	}
}

func TestGoConversions(t *testing.T) {
	for _, c := range []struct {
		in, want any
	}{
		{1, int64(1)}, {int8(-2), int64(-2)}, {uint16(3), int64(3)}, {uint64(4), int64(4)},
		{float32(0.5), 0.5}, {map[string]any{"b": 2, "a": 1}, NewDict("a", int64(1), "b", int64(2))},
		{Dict{Entries: []Entry{{"k", "v"}}}, NewDict("k", "v")},
	} {
		b, err := Encode(c.in)
		if err != nil {
			t.Fatal(err)
		}
		if got, _ := Decode(b); !reflect.DeepEqual(got, c.want) {
			t.Errorf("%#v: got %#v, want %#v", c.in, got, c.want)
		}
	}
	if _, err := Encode(uint64(math.MaxUint64)); err == nil {
		t.Error("overflow not reported")
	}
	if _, err := Encode(struct{}{}); err == nil {
		t.Error("unsupported type not reported")
	}
}

func TestEncodeCall(t *testing.T) {
	b, err := EncodeCall([]any{1}, NewDict("k", 2))
	if err != nil {
		t.Fatal(err)
	}
	args, rest, err := DecodePrefix(b)
	if err != nil || !reflect.DeepEqual(args, Tuple{int64(1)}) {
		t.Fatalf("args = %#v, %v", args, err)
	}
	if kw, err := Decode(rest); err != nil || !reflect.DeepEqual(kw, NewDict("k", int64(2))) {
		t.Fatalf("kwargs = %#v, %v", kw, err)
	}
	if _, err := EncodeCall(nil, NewDict(1, 2)); err == nil {
		t.Error("non-string keyword accepted")
	}
}

func TestDecodeErrors(t *testing.T) {
	for _, b := range [][]byte{{}, {TagInt, 1}, {TagStr, 5, 0, 0, 0, 'a'}, {TagList, 2, 0, 0, 0, TagNone}, {99}, {TagNone, TagNone}, {TagDict, 0xff, 0xff, 0xff, 0xff}} {
		if _, err := Decode(b); err == nil {
			t.Errorf("Decode(%v) succeeded", b)
		}
	}
	if _, err := Decode([]byte{TagInt}); !errors.Is(err, ErrTruncated) {
		t.Errorf("err = %v", err)
	}
}

func TestDict(t *testing.T) {
	d := NewDict("a", 1)
	d.Set("a", 2)
	d.Set(Tuple{1}, 3)
	if v, ok := d.Get("a"); !ok || v != 2 || d.Len() != 2 {
		t.Errorf("dict = %#v", d)
	}
	if v, ok := d.Get(Tuple{1}); !ok || v != 3 {
		t.Errorf("tuple key lookup failed")
	}
	if _, ok := d.Get("z"); ok {
		t.Error("missing key found")
	}
}

func TestDecodeCall(t *testing.T) {
	b, err := EncodeCall([]any{int64(1), "x"}, NewDict("k", true))
	if err != nil {
		t.Fatal(err)
	}
	args, kw, err := DecodeCall(b)
	if err != nil || !reflect.DeepEqual(args, []any{int64(1), "x"}) || !reflect.DeepEqual(kw, NewDict("k", true)) {
		t.Errorf("DecodeCall = %#v, %#v, %v", args, kw, err)
	}
	b, _ = EncodeCall(nil, nil)
	if args, kw, err := DecodeCall(b); err != nil || len(args) != 0 || kw != nil {
		t.Errorf("DecodeCall(()) = %#v, %#v, %v", args, kw, err)
	}
	for _, bad := range [][]byte{
		nil,
		{TagNone},
		append(must(Encode(Tuple{})), TagNone),
		append(must(Encode(Tuple{})), must(Encode(NewDict(int64(1), int64(2))))...),
		append(must(EncodeCall(nil, NewDict("k", 1))), TagNone),
	} {
		if _, _, err := DecodeCall(bad); err == nil {
			t.Errorf("DecodeCall(%v) succeeded", bad)
		}
	}
}

func must(b []byte, err error) []byte {
	if err != nil {
		panic(err)
	}
	return b
}

func TestDecodeDepth(t *testing.T) {
	nested := func(depth int) []byte {
		var b []byte
		for i := 0; i < depth; i++ {
			b = append(b, TagList, 1, 0, 0, 0)
		}
		return append(b, TagNone)
	}
	if _, err := Decode(nested(MaxDepth)); err != nil {
		t.Errorf("depth %d: %v", MaxDepth, err)
	}
	if _, err := Decode(nested(MaxDepth + 1)); err == nil || !strings.Contains(err.Error(), "nested") {
		t.Errorf("depth %d: err = %v", MaxDepth+1, err)
	}
}

// FuzzDecode checks that decoding arbitrary bytes never panics and that
// every value it accepts encodes back to exactly the same bytes.
func FuzzDecode(f *testing.F) {
	for _, v := range []any{
		nil, true, int64(math.MinInt64), math.NaN(), "héllo",
		[]any{int64(1), "a", Tuple{nil, false}},
		NewDict("k", []any{2.5}, int64(2), NewDict(), Tuple{}, "v"),
	} {
		b, err := Encode(v)
		if err != nil {
			f.Fatal(err)
		}
		f.Add(b)
	}
	f.Add([]byte{TagList, 0xff, 0xff, 0xff, 0xff})
	f.Add([]byte{TagDict, 1, 0, 0, 0, TagList, 1, 0, 0, 0, TagStr, 0, 0, 0, 0})
	f.Fuzz(func(t *testing.T, b []byte) {
		v, err := Decode(b)
		if err != nil {
			return
		}
		got, err := Encode(v)
		if err != nil {
			t.Fatalf("Encode(Decode(%x)): %v", b, err)
		}
		if !bytes.Equal(got, b) {
			t.Fatalf("Encode(Decode(%x)) = %x", b, got)
		}
	})
}

// FuzzDecodeCall is like FuzzDecode for encoded call arguments.
func FuzzDecodeCall(f *testing.F) {
	f.Add(must(EncodeCall([]any{int64(1), "x"}, NewDict("k", true))))
	f.Add(must(EncodeCall(nil, nil)))
	f.Add(append(must(Encode(Tuple{})), must(Encode(NewDict()))...))
	f.Fuzz(func(t *testing.T, b []byte) {
		args, kwargs, err := DecodeCall(b)
		if err != nil || kwargs != nil && kwargs.Len() == 0 {
			return // EncodeCall omits empty keyword arguments
		}
		got, err := EncodeCall(args, kwargs)
		if err != nil {
			t.Fatalf("EncodeCall(DecodeCall(%x)): %v", b, err)
		}
		if !bytes.Equal(got, b) {
			t.Fatalf("EncodeCall(DecodeCall(%x)) = %x", b, got)
		}
	})
}

func TestStructOpaque(t *testing.T) {
	s := NewStruct("b", int64(2), "a", []any{"x"})
	if s.Fields[0].Name != "a" {
		t.Errorf("fields not sorted: %#v", s.Fields)
	}
	if v, ok := s.Get("b"); !ok || v != int64(2) {
		t.Errorf("Get(b) = %v, %v", v, ok)
	}
	if _, ok := s.Get("c"); ok {
		t.Error("Get(c) found")
	}
	o := &Opaque{Type: "action", Repr: "action(go)", Payload: []byte{1, 2}, Falsy: true, Unhashable: true}
	for _, v := range []any{s, o, NewStruct(), &Opaque{}, Tuple{s, o}} {
		b, err := Encode(v)
		if err != nil {
			t.Fatal(err)
		}
		got, err := Decode(b)
		if err != nil {
			t.Fatalf("Decode(%#v): %v", v, err)
		}
		want := v
		if o, ok := v.(*Opaque); ok && o.Payload == nil {
			want = &Opaque{Payload: []byte{}}
		}
		if !reflect.DeepEqual(got, want) {
			t.Errorf("round trip: got %#v, want %#v", got, want)
		}
	}
	// Encoding sorts nothing, decoding sorts and rejects duplicates.
	b := must(Encode(&Struct{Fields: []Field{{"z", nil}, {"a", true}}}))
	if got, _ := Decode(b); !reflect.DeepEqual(got, NewStruct("a", true, "z", nil)) {
		t.Errorf("unsorted struct decoded as %#v", got)
	}
	if _, err := Decode(must(Encode(&Struct{Fields: []Field{{"a", nil}, {"a", nil}}}))); err == nil {
		t.Error("duplicate field accepted")
	}
	for _, bad := range [][]byte{
		{TagOpaque}, {TagOpaque, 0, 5, 0, 0, 0, 'x'}, {TagStruct, 1, 0, 0, 0}, {TagStruct, 1, 0, 0, 0, 1, 0, 0, 0, 'a'},
	} {
		if _, err := Decode(bad); err == nil {
			t.Errorf("Decode(%v) succeeded", bad)
		}
	}
}
