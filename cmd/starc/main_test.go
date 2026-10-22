package main

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func cli(t *testing.T, stdin string, args ...string) (int, string, string) {
	t.Helper()
	var out, errb bytes.Buffer
	code := run(args, strings.NewReader(stdin), &out, &errb)
	return code, out.String(), errb.String()
}

func TestBuildAndRun(t *testing.T) {
	dir := t.TempDir()
	out := filepath.Join(dir, "fib.wasm")
	if code, _, stderr := cli(t, "", "build", "-o", out, "../../examples/fib.star"); code != 0 {
		t.Fatalf("build failed: %s", stderr)
	}
	data, err := os.ReadFile(out)
	if err != nil || !bytes.HasPrefix(data, []byte("\x00asm")) {
		t.Fatalf("bad output: %v", err)
	}
	code, stdout, stderr := cli(t, "", "run", out, "fib", "20")
	if code != 0 || stdout != "fib(30) = 832040\n6765\n" {
		t.Errorf("run wasm: code %d stdout %q stderr %q", code, stdout, stderr)
	}
	code, stdout, _ = cli(t, "", "run", "../../examples/fib.star")
	if code != 0 || stdout != "fib(30) = 832040\n" {
		t.Errorf("run star: code %d stdout %q", code, stdout)
	}
	code, stdout, _ = cli(t, "", "run", "../../examples/wordcount.star", "count_words", "b a b. c, A b", "2")
	if want := `[{"count":3,"word":"b"},{"count":2,"word":"a"}]` + "\n"; code != 0 || stdout != want {
		t.Errorf("wordcount: code %d stdout %q", code, stdout)
	}
}

func TestStdinToStdout(t *testing.T) {
	code, stdout, stderr := cli(t, "print(1)", "-command", "-")
	if code != 0 || !strings.HasPrefix(stdout, "\x00asm") {
		t.Fatalf("code %d stderr %s", code, stderr)
	}
}

func TestErrors(t *testing.T) {
	if code, _, stderr := cli(t, "x = (", "-o", "-", "-"); code != 1 || !strings.Contains(stderr, "<stdin>:1:6") {
		t.Errorf("syntax error: code %d stderr %q", code, stderr)
	}
	if code, _, stderr := cli(t, "fail('x')", "run", "-"); code != 1 ||
		stderr != "Traceback (most recent call last):\n  <stdin>:1:5: in <toplevel>\nError in fail: fail: x\n" {
		t.Errorf("runtime error: code %d stderr %q", code, stderr)
	}
	if code, _, _ := cli(t, ""); code != 2 {
		t.Errorf("no args: code %d", code)
	}
	if code, stdout, _ := cli(t, "", "version"); code != 0 || !strings.HasPrefix(stdout, "starc ") {
		t.Errorf("version: %q", stdout)
	}
	if code, stdout, _ := cli(t, "", "help"); code != 0 || !strings.Contains(stdout, "-command") {
		t.Errorf("help: %q", stdout)
	}
}

func TestHostFlags(t *testing.T) {
	out := filepath.Join(t.TempDir(), "h.wasm")
	src := "print(ctx.now())\n"
	if code, _, stderr := cli(t, src, "-o", out, "-host", "ctx.now, ctx.self", "-no-wasi", "-"); code != 0 {
		t.Fatalf("build: %s", stderr)
	}
	data, err := os.ReadFile(out)
	if err != nil || bytes.Contains(data, []byte("wasi_snapshot_preview1")) || !bytes.Contains(data, []byte("ctx.now")) {
		t.Fatalf("bad module: %v", err)
	}
	// The CLI provides no host functions.
	if code, _, stderr := cli(t, "", "run", out); code != 1 || !strings.Contains(stderr, `"ctx.now"`) {
		t.Errorf("run: code %d stderr %q", code, stderr)
	}
	if code, _, stderr := cli(t, "print(1)", "-host", "a..b", "-o", out, "-"); code != 1 || !strings.Contains(stderr, "invalid host function name") {
		t.Errorf("bad name: code %d stderr %q", code, stderr)
	}
}

func TestModuleVersion(t *testing.T) {
	for in, want := range map[string]string{
		"":        "",
		"(devel)": "",
		"v0.1.0":  "0.1.0",
		"v0.1.1-0.20261020173601-54ed88086fa5+dirty": "0.1.1-0.20261020173601-54ed88086fa5+dirty",
	} {
		if got := moduleVersion(in); got != want {
			t.Errorf("moduleVersion(%q) = %q, want %q", in, got, want)
		}
	}
}

func TestFuelFlags(t *testing.T) {
	src := "def f(n):\n    return sorted(range(n, 0, -1))[:2]\n"
	code, stdout, stderr := cli(t, src, "run", "-fuel", "-", "f", "100")
	if code != 0 || stdout != "[1,2]\n" || !strings.HasPrefix(stderr, "fuel used: ") {
		t.Errorf("-fuel: code %d stdout %q stderr %q", code, stdout, stderr)
	}
	code, _, stderr = cli(t, src, "run", "-budget", "3000", "-", "f", "100")
	if want := "fuel used: 3000\nTraceback (most recent call last):\n  <stdin>:2:18: in f\nError in sorted: fuel exhausted\n"; code != 1 || stderr != want {
		t.Errorf("-budget: code %d stderr %q", code, stderr)
	}
	if code, _, _ := cli(t, src, "run", "-budget", "-1", "-", "f", "1"); code != 2 {
		t.Errorf("negative budget: code %d", code)
	}
}
