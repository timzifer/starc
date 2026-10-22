// Tests of the value conversions and the convenience API of host.mjs.
import { test } from "node:test";
import assert from "node:assert/strict";
import { readFileSync } from "node:fs";
import { createRequire } from "node:module";
import { execFileSync } from "node:child_process";
import { fileURLToPath } from "node:url";
import { join, dirname } from "node:path";

import { loadCompiler } from "../compiler.mjs";
import { encode, decode, encodeCall, decodeCall, load, Tuple, Struct, Opaque, Float, StarlarkError, ErrorCode } from "../host.mjs";

test("values round-trip", () => {
  const values = [
    null, true, false, 0, -1, 2 ** 53 - 1, 9007199254740993n, -(2n ** 63n), 0.5, -0, Infinity, "hé", [], [1, [2]],
    Tuple.of(1, "a"), new Map([["k", 1], [2, Tuple.of()]]), new Struct({ b: 2, a: 1 }),
    new Opaque({ type: "t", repr: "<t>", payload: new Uint8Array([1, 2]), falsy: true }),
  ];
  for (const v of values) assert.deepEqual(decode(encode(v)), v);
  assert.equal(decode(encode(new Float(2))), 2);
  assert.equal(encode(new Float(2))[0], 4, "Float is a float");
  assert.equal(encode(2)[0], 3, "an integral number is an int");
  assert.equal(encode(2.5)[0], 4);
  assert.equal(encode(2 ** 60)[0], 4, "beyond the safe integers, numbers are floats");
  assert.ok(Number.isNaN(decode(encode(NaN))));
  assert.deepEqual(decode(encode({ a: 1 })), new Map([["a", 1]]), "objects are dicts");
  assert.deepEqual([...new Struct({ b: 1, a: 2 }).fields.keys()], ["a", "b"]);
  // Field names are sorted by code point, like their UTF-8 bytes in Go (issue #51).
  const names = ["\u{10000}", "\ue000", "\uffff", "\ud7ff", "a", "", "\u{10000}a", "\u{10ffff}"];
  assert.deepEqual([...new Struct(new Map(names.map((k) => [k, 1]))).fields.keys()], ["", "a", "\ud7ff", "\ue000", "\uffff", "\u{10000}", "\u{10000}a", "\u{10ffff}"]);
  const native = Buffer.from("CQIAAAADAAAA7oCAAwEAAAAAAAAABAAAAPCQgIADAgAAAAAAAAA=", "base64");
  assert.deepEqual([...decode(native).fields.keys()], ["\ue000", "\u{10000}"]);
  assert.ok(Buffer.from(encode(decode(native))).equals(native), "a struct from Go re-encodes to the same bytes");
  assert.throws(() => encode(2n ** 63n), RangeError);
  assert.throws(() => encode(() => 1), TypeError);
  assert.throws(() => decode(new Uint8Array([3, 1])), /truncated/);
  assert.throws(() => decode(new Uint8Array([0, 0])), /trailing/);
  assert.deepEqual(decodeCall(encodeCall([1, "x"], { k: true })), { args: [1, "x"], kwargs: new Map([["k", true]]) });
  assert.deepEqual(decodeCall(encodeCall([], {})), { args: [], kwargs: null });
});

test("a leading U+FEFF is part of a string", () => {
  const s = "\ufeffhello";
  assert.equal(decode(encode(s)), s);
  assert.deepEqual(decode(encode(new Map([[s, 1]]))), new Map([[s, 1]]));
  assert.deepEqual([...decode(encode(new Struct({ [s]: 1 }))).fields.keys()], [s]);
  assert.deepEqual(decode(new Uint8Array([5, 3, 0, 0, 0, 0xef, 0xbb, 0xbf])), "\ufeff");
});

test("calls with JavaScript values", async () => {
  const here = dirname(fileURLToPath(import.meta.url));
  const goroot = execFileSync("go", ["env", "GOROOT"], { encoding: "utf8" }).trim();
  createRequire(import.meta.url)(process.env.STARC_WASM_EXEC ?? join(goroot, "lib", "wasm", "wasm_exec.js"));
  const compiler = await loadCompiler(readFileSync(process.env.STARC_COMPILER_WASM ?? join(here, "..", "testdata", "starc.wasm")));
  assert.equal(typeof compiler.version, "string");

  const src = 'def greet(name, punct = "!"):\n    return "hello " + name + punct\n\ndef bad(x):\n    return x + 1\n';
  const { wasm, errors } = compiler.compile(src, { filename: "greet.star", fuel: true });
  assert.deepEqual(errors, []);
  const m = await load(wasm);
  assert.deepEqual(m.exports.map((e) => e.name), ["greet", "bad"]);
  assert.equal(m.call("greet", "world"), "hello world!");
  assert.equal(m.callKw("greet", ["you"], { punct: "?" }), "hello you?");
  assert.throws(() => m.call("bad", "x"), (e) => {
    assert.ok(e instanceof StarlarkError);
    assert.equal(e.code, ErrorCode.STARLARK);
    assert.equal(String(e.pos), "greet.star:5:14");
    assert.equal(e.backtrace(), 'Traceback (most recent call last):\n  greet.star:5:14: in bad\nError: unknown binary op: string + int');
    return true;
  });
  assert.equal(m.call("greet", "again"), "hello again!", "usable after an error");
  assert.throws(() => m.call("nope"), /exports no function/);

  // A leading U+FEFF is part of results and output (issue #48).
  let printed = "";
  const echo = await load(compiler.compile('print("\ufeffhi")\n\ndef echo(x):\n    return x\n', { filename: "echo.star" }).wasm, {
    stdout: (s) => (printed += s),
  });
  assert.equal(printed, "\ufeffhi\n");
  assert.equal(echo.call("echo", "\ufeffhello"), "\ufeffhello");
  assert.deepEqual(echo.call("echo", new Map([["\ufeffk", 1]])), new Map([["\ufeffk", 1]]));

  // A struct result decodes to the field order of the module (issue #51).
  const st = await load(compiler.compile('def f():\n    return struct(**{"\\ue000": 1, "\\U00010000": 2})\n', { filename: "s.star", struct: true }).wasm);
  const raw = st.callRaw("f", encodeCall([], {}));
  assert.ok(Buffer.from(raw).equals(Buffer.from("CQIAAAADAAAA7oCAAwEAAAAAAAAABAAAAPCQgIADAgAAAAAAAAA=", "base64")));
  assert.ok(Buffer.from(encode(st.call("f"))).equals(Buffer.from(raw)));

  const res = compiler.compile("x = (", { filename: "e.star" });
  assert.equal(res.wasm, null);
  assert.deepEqual(res.errors, [{ file: "e.star", line: 1, col: 6, msg: "got end of file, want primary expression" }]);

  // Host functions are own properties of config.host, never inherited ones
  // like Object.prototype.toString (issue #49).
  for (const name of ["toString", "constructor", "hasOwnProperty", "__proto__"]) {
    const { wasm } = compiler.compile(`def f():\n    return ${name}()\n`, { filename: "h.star", noWasi: true, host: [{ name }] });
    await assert.rejects(load(wasm), new RegExp(`imports host function "${name}", which config.host does not provide`), name);
    await assert.rejects(load(wasm, { host: {} }), /does not provide/, name);
    const host = Object.defineProperty({}, name, { value: () => `own ${name}`, enumerable: true });
    assert.equal((await load(wasm, { host })).call("f"), `own ${name}`, name);
  }

  // initFuel 0 is unlimited, as a number and as a bigint (issue #50).
  const fueled = compiler.compile("x = 1\n\ndef f():\n    return x\n", { filename: "fuel.star", noWasi: true, fuel: true }).wasm;
  const unlimited = (await load(fueled)).fuel();
  for (const initFuel of [0, 0n]) {
    const fm = await load(fueled, { initFuel });
    assert.equal(fm.fuel(), unlimited, `initFuel ${typeof initFuel}`);
    assert.equal(fm.call("f"), 1);
  }
  for (const initFuel of [1, 1n]) {
    await assert.rejects(load(fueled, { initFuel }), (e) => e instanceof StarlarkError && e.code === ErrorCode.FUEL, `initFuel ${typeof initFuel}`);
  }
  const limited = await load(fueled, { initFuel: 100000n });
  assert.ok(limited.fuel() < 100000n, "a finite budget is used");
});
