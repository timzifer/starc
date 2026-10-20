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
	if code, _, stderr := cli(t, "fail('x')", "run", "-"); code != 1 || stderr != "Error: fail: x\n" {
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
