package jsapi

import (
	"bytes"
	"testing"
)

func TestCompile(t *testing.T) {
	wasm, errs := Compile("load(\"lib.star\", \"f\")\ndef g():\n    return f()\n",
		`{"filename": "p.star", "fuel": true, "command": false, "host": [{"name": "now", "cost": 2}],
		  "dialect": {"set": true}, "files": {"lib.star": "def f():\n    return now()\n"}}`)
	if errs != nil || !bytes.HasPrefix(wasm, []byte("\x00asm")) {
		t.Fatalf("compile: %v", errs)
	}
	for _, c := range []struct{ src, opts, want string }{
		{"x = (", `{"filename": "e.star"}`, "e.star:1:6: got end of file, want primary expression"},
		{"load(\"m\", \"x\")", ``, "main.star:1:1: cannot load m: no such module"},
		{"x = 1", `{"maxMemoryPages": 70000}`, ":0:0: MaxMemoryPages 70000 exceeds 65535"},
		{"x = 1", `{`, ":0:0: invalid options: unexpected end of JSON input"},
	} {
		wasm, errs := Compile(c.src, c.opts)
		if wasm != nil || len(errs) != 1 {
			t.Errorf("%q: %v, %v", c.src, wasm != nil, errs)
			continue
		}
		e := errs[0]
		if got := e.File + ":" + itoa(e.Line) + ":" + itoa(e.Col) + ": " + e.Msg; got != c.want {
			t.Errorf("%q: got %q, want %q", c.src, got, c.want)
		}
	}
}

func itoa(n int) string {
	if n < 10 {
		return string(rune('0' + n))
	}
	return itoa(n/10) + string(rune('0'+n%10))
}
