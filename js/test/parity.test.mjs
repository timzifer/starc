// Parity of the JavaScript compiler and host with the Go ones: compiles
// the programs of js/testdata/parity.json (written by
// internal/cmd/jsfixtures with the native compiler and wazero) with the
// compiler built for JavaScript, runs them with host.mjs and requires the
// same module bytes, output, results, errors and fuel.
//
// Run with "make js-test", or set STARC_COMPILER_WASM to the compiler
// (GOOS=js GOARCH=wasm go build ./cmd/starc-js) and STARC_WASM_EXEC to
// Go's lib/wasm/wasm_exec.js.
import { test } from "node:test";
import assert from "node:assert/strict";
import { readFileSync } from "node:fs";
import { createHash } from "node:crypto";
import { createRequire } from "node:module";
import { execFileSync } from "node:child_process";
import { fileURLToPath } from "node:url";
import { join, dirname } from "node:path";

import { loadCompiler } from "../compiler.mjs";
import { load, ExitError, StarlarkError } from "../host.mjs";

const here = dirname(fileURLToPath(import.meta.url));
const goroot = () => execFileSync("go", ["env", "GOROOT"], { encoding: "utf8" }).trim();
const wasmExec = process.env.STARC_WASM_EXEC ?? join(goroot(), "lib", "wasm", "wasm_exec.js");
createRequire(import.meta.url)(wasmExec);
const compiler = await loadCompiler(readFileSync(process.env.STARC_COMPILER_WASM ?? join(here, "..", "testdata", "starc.wasm")));
const fixtures = JSON.parse(readFileSync(join(here, "..", "testdata", "parity.json"), "utf8"));

const hex = (b) => Buffer.from(b).toString("hex");
const unhex = (s) => new Uint8Array(Buffer.from(s, "hex"));

// The host functions of internal/cmd/jsfixtures.
const host = {
  add: (args) => args.reduce((s, a) => s + a, 0),
  charge: (args, _kw, call) => {
    call.chargeFuel(args[0]);
    return null;
  },
  refuse: (args) => {
    throw new Error(`refused: ${args[0]}`);
  },
  "ctx.name": (_args, kwargs) => (kwargs?.get("upper") === true ? "STARC" : "starc"),
};

const vars = (vs) => vs.map(({ name, bound, type, value }) => ({ name, bound, type, value }));

function recordStop(s) {
  return {
    pos: s.pos.toString(),
    err: s.err,
    frames: s.stack().map((f) => ({ name: f.name, kind: f.kind, pos: f.pos ? f.pos.toString() : "", locals: vars(f.locals) })),
    globals: vars(s.globals()),
  };
}

function errorResult(e) {
  if (e instanceof ExitError) return { msg: "", code: 0, backtrace: "", exitCode: e.code };
  if (e instanceof StarlarkError) return { msg: e.message, code: e.code, backtrace: e.backtrace() };
  throw e;
}

for (const c of fixtures.cases) {
  test(c.name, async () => {
    const { wasm, errors } = compiler.compile(c.src, c.options);
    assert.deepEqual(errors, c.compileErrors, "compile errors");
    if (!c.wasmSha256) {
      assert.equal(wasm, null);
      return;
    }
    assert.equal(createHash("sha256").update(wasm).digest("hex"), c.wasmSha256, "module bytes");

    let stdout = "";
    let stderr = "";
    let m;
    // Debug cases record every stop, as internal/cmd/jsfixtures does.
    const stops = [];
    const debug = c.stops ? { step: true, errors: true, stop: (s) => stops.push(recordStop(s)) } : undefined;
    try {
      m = await load(wasm, { stdout: (s) => (stdout += s), stderr: (s) => (stderr += s), host, initFuel: c.initFuel, debug });
    } catch (e) {
      assert.deepEqual(errorResult(e), c.initError, "init error");
      assert.equal(stdout, c.initStdout, "init stdout");
      assert.equal(stderr, c.initStderr, "init stderr");
      return;
    }
    assert.equal(c.initError, undefined, "init succeeded");
    assert.equal(stdout, c.initStdout, "init stdout");
    assert.equal(stderr, c.initStderr, "init stderr");
    if (c.options.command) return;
    assert.equal(BigInt(c.initFuel) - m.fuel(), BigInt(c.initFuelUsed), "init fuel");
    if (c.options.profile) m.resetProfile();
    for (const cl of c.calls) {
      stdout = "";
      m.setFuel(cl.budget);
      const got = { fn: cl.fn, payload: cl.payload, budget: cl.budget };
      try {
        got.result = hex(m.callRaw(cl.fn, unhex(cl.payload)));
      } catch (e) {
        got.error = errorResult(e);
      }
      got.fuelUsed = Number(BigInt(cl.budget) - m.fuel());
      got.stdout = stdout;
      assert.deepEqual(got, cl, `call ${cl.fn}`);
    }
    if (c.stops) assert.deepEqual(stops, c.stops, "debugger stops");
    if (c.profile) {
      const prof = m.profile().map((p) => ({ name: p.name, fuel: Number(p.fuel), calls: Number(p.calls) }));
      assert.deepEqual(prof, c.profile, "profile");
    }
  });
}
