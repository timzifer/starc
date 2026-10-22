//go:build js && wasm

// Command starc-js is the starc compiler for JavaScript: built with
// GOOS=js GOARCH=wasm and run with Go's wasm_exec.js, it defines
// globalThis.starcCompile(src, optionsJSON), which js/compiler.mjs wraps
// (see docs/js.md).
package main

import (
	"encoding/json"
	"fmt"
	"syscall/js"

	"github.com/timzifer/starc"
	"github.com/timzifer/starc/internal/jsapi"
)

func main() {
	js.Global().Set("starcCompile", js.FuncOf(func(_ js.Value, args []js.Value) (res any) {
		defer func() {
			if r := recover(); r != nil {
				res = result(nil, []jsapi.Error{{Msg: fmt.Sprintf("internal compiler error: %v", r)}})
			}
		}()
		if len(args) < 1 || args[0].Type() != js.TypeString {
			return result(nil, []jsapi.Error{{Msg: "starcCompile(src, options): src must be a string"}})
		}
		optsJSON := ""
		if len(args) > 1 && args[1].Type() == js.TypeString {
			optsJSON = args[1].String()
		}
		return result(jsapi.Compile(args[0].String(), optsJSON))
	}))
	js.Global().Set("starcVersion", starc.Version)
	select {} // keep the functions alive
}

// result returns {wasm: Uint8Array | null, errors: string (JSON)}.
func result(wasm []byte, errs []jsapi.Error) any {
	var w any
	if wasm != nil {
		arr := js.Global().Get("Uint8Array").New(len(wasm))
		js.CopyBytesToJS(arr, wasm)
		w = arr
	}
	if errs == nil {
		errs = []jsapi.Error{}
	}
	e, _ := json.Marshal(errs)
	return map[string]any{"wasm": w, "errors": string(e)}
}
