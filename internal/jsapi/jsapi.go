// Package jsapi implements the compile API of starc for JavaScript
// (cmd/starc-js): options as JSON, errors with positions for editor
// markers. It has no build constraints, so that tests can compile
// exactly as the JavaScript compiler does.
package jsapi

import (
	"encoding/json"
	"errors"

	"github.com/timzifer/starc"
)

// Options are the compile options of the JavaScript API (docs/js.md).
type Options struct {
	Filename       string            `json:"filename"`
	Command        bool              `json:"command"`
	Exports        []string          `json:"exports"`
	NoNames        bool              `json:"noNames"`
	Host           []HostFunc        `json:"host"`
	NoWASI         bool              `json:"noWasi"`
	Fuel           bool              `json:"fuel"`
	Profile        bool              `json:"profile"`
	MaxMemoryPages uint32            `json:"maxMemoryPages"`
	MaxCallDepth   int               `json:"maxCallDepth"`
	MaxInputBytes  uint32            `json:"maxInputBytes"`
	MaxResultBytes uint32            `json:"maxResultBytes"`
	FiniteFloats   bool              `json:"finiteFloats"`
	Struct         bool              `json:"struct"`
	NoBacktrace    bool              `json:"noBacktrace"`
	Dialect        Dialect           `json:"dialect"`
	Files          map[string]string `json:"files"` // modules for load
}

// HostFunc declares a host function (starc.HostFunc).
type HostFunc struct {
	Name string `json:"name"`
	Cost int64  `json:"cost"`
}

// Dialect selects language extensions (starc.Dialect).
type Dialect struct {
	While           bool `json:"while"`
	TopLevelControl bool `json:"topLevelControl"`
	GlobalReassign  bool `json:"globalReassign"`
	Recursion       bool `json:"recursion"`
	Set             bool `json:"set"`
}

// Error is a compile error with a position for editor markers; a line of
// 0 means that the error has no position.
type Error struct {
	File string `json:"file"`
	Line int    `json:"line"`
	Col  int    `json:"col"`
	Msg  string `json:"msg"`
}

// Compile compiles src with the options optsJSON (an Options object).
func Compile(src string, optsJSON string) ([]byte, []Error) {
	var o Options
	if optsJSON != "" {
		if err := json.Unmarshal([]byte(optsJSON), &o); err != nil {
			return nil, []Error{{Msg: "invalid options: " + err.Error()}}
		}
	}
	if o.Filename == "" {
		o.Filename = "main.star"
	}
	opts := &starc.Options{Exports: o.Exports, NoNames: o.NoNames, NoWASI: o.NoWASI, Fuel: o.Fuel, Profile: o.Profile,
		MaxMemoryPages: o.MaxMemoryPages, MaxCallDepth: o.MaxCallDepth, MaxInputBytes: o.MaxInputBytes,
		MaxResultBytes: o.MaxResultBytes, FiniteFloats: o.FiniteFloats, Struct: o.Struct, NoBacktrace: o.NoBacktrace,
		Dialect: starc.Dialect{While: o.Dialect.While, TopLevelControl: o.Dialect.TopLevelControl,
			GlobalReassign: o.Dialect.GlobalReassign, Recursion: o.Dialect.Recursion, Set: o.Dialect.Set}}
	if o.Command {
		opts.ExecModel = starc.Command
	}
	for _, h := range o.Host {
		opts.Host = append(opts.Host, starc.HostFunc{Name: h.Name, Cost: h.Cost})
	}
	opts.Loader = func(module string) ([]byte, error) {
		src, ok := o.Files[module]
		if !ok {
			return nil, errors.New("no such module")
		}
		return []byte(src), nil
	}
	wasm, err := starc.Compile(o.Filename, []byte(src), opts)
	if err == nil {
		return wasm, nil
	}
	var el starc.ErrorList
	if !errors.As(err, &el) {
		return nil, []Error{{Msg: err.Error()}}
	}
	out := make([]Error, len(el))
	for i, e := range el {
		out[i] = Error{e.Pos.Filename, e.Pos.Line, e.Pos.Col, e.Msg}
	}
	return nil, out
}
