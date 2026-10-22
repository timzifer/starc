// Package host runs modules produced by starc in JavaScript (browsers,
// Node.js, Deno, Bun) and converts between JavaScript and Starlark
// values. It mirrors the Go package github.com/timzifer/starc/host: the
// same module gives the same results and uses the same fuel in both.
// See docs/js.md.

// ---- values ----

/** A Starlark tuple. */
export class Tuple extends Array {
  /** @param {Iterable<any>} items */
  static of(...items) {
    const t = new Tuple();
    t.push(...items);
    return t;
  }
}

/** A Starlark struct: named fields, sorted by name. */
export class Struct {
  /** @param {Record<string, any> | Map<string, any>} fields */
  constructor(fields = {}) {
    const entries = fields instanceof Map ? [...fields] : Object.entries(fields);
    entries.sort((a, b) => (a[0] < b[0] ? -1 : a[0] > b[0] ? 1 : 0));
    /** @type {Map<string, any>} */
    this.fields = new Map(entries);
  }
}

/** An opaque host value (see docs/language.md). */
export class Opaque {
  /**
   * @param {{type: string, repr: string, payload?: Uint8Array, falsy?: boolean, unhashable?: boolean}} o
   */
  constructor({ type, repr, payload = new Uint8Array(0), falsy = false, unhashable = false }) {
    this.type = type;
    this.repr = repr;
    this.payload = payload;
    this.falsy = falsy;
    this.unhashable = unhashable;
  }
}

/** Float marks a number that is passed as a Starlark float even if it is integral. */
export class Float {
  /** @param {number} value */
  constructor(value) {
    this.value = value;
  }
}

const TAG = { none: 0, false: 1, true: 2, int: 3, float: 4, string: 5, list: 6, tuple: 7, dict: 8, struct: 9, opaque: 10 };
const MAX_DEPTH = 1000;
const encoder = new TextEncoder();
const decoder = new TextDecoder("utf-8", { fatal: false });

class Writer {
  constructor() {
    this.buf = new Uint8Array(64);
    this.view = new DataView(this.buf.buffer);
    this.len = 0;
  }
  grow(n) {
    if (this.len + n <= this.buf.length) return;
    let size = this.buf.length * 2;
    while (size < this.len + n) size *= 2;
    const buf = new Uint8Array(size);
    buf.set(this.buf.subarray(0, this.len));
    this.buf = buf;
    this.view = new DataView(buf.buffer);
  }
  u8(v) {
    this.grow(1);
    this.buf[this.len++] = v;
  }
  u32(v) {
    if (v > 0xffffffff) throw new RangeError("starc: value too large to encode");
    this.grow(4);
    this.view.setUint32(this.len, v, true);
    this.len += 4;
  }
  i64(v) {
    this.grow(8);
    this.view.setBigInt64(this.len, v, true);
    this.len += 8;
  }
  f64(v) {
    this.grow(8);
    // NaN is canonical, as in the module.
    if (Number.isNaN(v)) this.view.setBigUint64(this.len, 0x7ff8000000000000n, true);
    else this.view.setFloat64(this.len, v, true);
    this.len += 8;
  }
  raw(bytes) {
    this.u32(bytes.length);
    this.grow(bytes.length);
    this.buf.set(bytes, this.len);
    this.len += bytes.length;
  }
  bytes() {
    return this.buf.slice(0, this.len);
  }
}

const MIN_INT64 = -(1n << 63n);
const MAX_INT64 = (1n << 63n) - 1n;

function encodeValue(w, v, depth) {
  if (depth > MAX_DEPTH) throw new RangeError("starc: value nested too deeply");
  if (v === null || v === undefined) return w.u8(TAG.none);
  switch (typeof v) {
    case "boolean":
      return w.u8(v ? TAG.true : TAG.false);
    case "bigint":
      if (v < MIN_INT64 || v > MAX_INT64) throw new RangeError(`starc: int ${v} does not fit in 64 bits`);
      w.u8(TAG.int);
      return w.i64(v);
    case "number":
      // -0 is a float: as an int it would lose its sign.
      if (Number.isSafeInteger(v) && !Object.is(v, -0)) {
        w.u8(TAG.int);
        return w.i64(BigInt(v));
      }
      w.u8(TAG.float);
      return w.f64(v);
    case "string":
      w.u8(TAG.string);
      return w.raw(encoder.encode(v));
  }
  if (v instanceof Float) {
    w.u8(TAG.float);
    return w.f64(v.value);
  }
  if (v instanceof Tuple || Array.isArray(v)) {
    w.u8(v instanceof Tuple ? TAG.tuple : TAG.list);
    w.u32(v.length);
    for (const x of v) encodeValue(w, x, depth + 1);
    return;
  }
  if (v instanceof Map) {
    w.u8(TAG.dict);
    w.u32(v.size);
    for (const [k, x] of v) {
      encodeValue(w, k, depth + 1);
      encodeValue(w, x, depth + 1);
    }
    return;
  }
  if (v instanceof Struct) {
    w.u8(TAG.struct);
    w.u32(v.fields.size);
    for (const [k, x] of v.fields) {
      w.raw(encoder.encode(k));
      encodeValue(w, x, depth + 1);
    }
    return;
  }
  if (v instanceof Opaque) {
    w.u8(TAG.opaque);
    w.u8((v.falsy ? 1 : 0) | (v.unhashable ? 2 : 0));
    w.raw(encoder.encode(v.type));
    w.raw(encoder.encode(v.repr));
    return w.raw(v.payload);
  }
  const proto = Object.getPrototypeOf(v);
  if (proto === Object.prototype || proto === null) {
    const entries = Object.entries(v);
    w.u8(TAG.dict);
    w.u32(entries.length);
    for (const [k, x] of entries) {
      encodeValue(w, k, depth + 1);
      encodeValue(w, x, depth + 1);
    }
    return;
  }
  throw new TypeError(`starc: cannot encode ${Object.prototype.toString.call(v)}`);
}

/**
 * Encodes a value per the starc ABI (docs/abi.md).
 * @param {any} v
 * @returns {Uint8Array}
 */
export function encode(v) {
  const w = new Writer();
  encodeValue(w, v, 0);
  return w.bytes();
}

/**
 * Encodes call arguments: a tuple of positional arguments, followed by
 * a dict of keyword arguments if there are any.
 * @param {any[]} args
 * @param {Map<string, any> | Record<string, any>} [kwargs]
 */
export function encodeCall(args, kwargs) {
  const w = new Writer();
  encodeValue(w, Tuple.of(...args), 0);
  const kw = kwargs instanceof Map ? kwargs : kwargs ? new Map(Object.entries(kwargs)) : null;
  if (kw && kw.size > 0) {
    for (const k of kw.keys()) {
      if (typeof k !== "string") throw new TypeError(`starc: keyword argument name ${String(k)} is not a string`);
    }
    encodeValue(w, kw, 0);
  }
  return w.bytes();
}

class Reader {
  constructor(bytes) {
    this.b = bytes;
    this.v = new DataView(bytes.buffer, bytes.byteOffset, bytes.byteLength);
    this.p = 0;
  }
  need(n) {
    if (this.p + n > this.b.length) throw new RangeError("starc: truncated value");
  }
  u8() {
    this.need(1);
    return this.b[this.p++];
  }
  u32() {
    this.need(4);
    const x = this.v.getUint32(this.p, true);
    this.p += 4;
    return x;
  }
  raw() {
    const n = this.u32();
    this.need(n);
    const s = this.b.subarray(this.p, this.p + n);
    this.p += n;
    return s;
  }
}

function decodeValue(r, depth) {
  if (depth > MAX_DEPTH) throw new RangeError("starc: value nested too deeply");
  const tag = r.u8();
  switch (tag) {
    case TAG.none:
      return null;
    case TAG.false:
      return false;
    case TAG.true:
      return true;
    case TAG.int: {
      r.need(8);
      const x = r.v.getBigInt64(r.p, true);
      r.p += 8;
      const n = Number(x);
      return Number.isSafeInteger(n) ? n : x;
    }
    case TAG.float: {
      r.need(8);
      const x = r.v.getFloat64(r.p, true);
      r.p += 8;
      return x;
    }
    case TAG.string:
      return decoder.decode(r.raw());
    case TAG.list:
    case TAG.tuple: {
      const n = r.u32();
      const out = tag === TAG.tuple ? new Tuple() : [];
      for (let i = 0; i < n; i++) out.push(decodeValue(r, depth + 1));
      return out;
    }
    case TAG.dict: {
      const n = r.u32();
      const m = new Map();
      for (let i = 0; i < n; i++) {
        const k = decodeValue(r, depth + 1);
        m.set(k, decodeValue(r, depth + 1));
      }
      return m;
    }
    case TAG.struct: {
      const n = r.u32();
      const m = new Map();
      for (let i = 0; i < n; i++) {
        const k = decoder.decode(r.raw());
        if (m.has(k)) throw new RangeError(`starc: duplicate struct field ${k}`);
        m.set(k, decodeValue(r, depth + 1));
      }
      return new Struct(m);
    }
    case TAG.opaque: {
      const flags = r.u8();
      const type = decoder.decode(r.raw());
      const repr = decoder.decode(r.raw());
      const payload = r.raw().slice();
      return new Opaque({ type, repr, payload, falsy: (flags & 1) !== 0, unhashable: (flags & 2) !== 0 });
    }
  }
  throw new RangeError(`starc: invalid tag ${tag}`);
}

/**
 * Decodes a value. Ints become numbers, or bigints outside the safe
 * integer range; floats become numbers; lists arrays; tuples Tuples;
 * dicts Maps.
 * @param {Uint8Array} bytes
 */
export function decode(bytes) {
  const r = new Reader(bytes);
  const v = decodeValue(r, 0);
  if (r.p !== bytes.length) throw new RangeError("starc: trailing bytes");
  return v;
}

/** @param {Uint8Array} bytes */
export function decodeCall(bytes) {
  const r = new Reader(bytes);
  const args = decodeValue(r, 0);
  if (!(args instanceof Tuple)) throw new RangeError("starc: arguments are not a tuple");
  let kwargs = null;
  if (r.p < bytes.length) kwargs = decodeValue(r, 0);
  if (r.p !== bytes.length) throw new RangeError("starc: trailing bytes");
  return { args: [...args], kwargs };
}

// ---- errors ----

/** Error codes of starc_error_code (docs/abi.md#errors). */
export const ErrorCode = Object.freeze({ STARLARK: 1, FUEL: 2, MEMORY: 3, DEPTH: 4, SIZE: 5 });

/** A source position; built-ins have the file name "<builtin>" and no line. */
export class Position {
  constructor(filename, line = 0, col = 0) {
    this.filename = filename;
    this.line = line;
    this.col = col;
  }
  get builtin() {
    return this.filename === "<builtin>" && this.line === 0;
  }
  toString() {
    if (this.line === 0) return this.filename;
    if (this.col === 0) return `${this.filename}:${this.line}`;
    return `${this.filename}:${this.line}:${this.col}`;
  }
}

/** A Starlark run-time error reported by a module. */
export class StarlarkError extends Error {
  constructor(message, code, pos, callStack) {
    super(message);
    this.name = "StarlarkError";
    /** @type {number} */
    this.code = code;
    /** Position of the failing operation, or null. @type {Position | null} */
    this.pos = pos;
    /** Active calls, outermost first. @type {{name: string, pos: Position}[]} */
    this.callStack = callStack;
  }
  /** The call stack and message in the format of go.starlark.net's EvalError.Backtrace. */
  backtrace() {
    let stack = this.callStack;
    let inName = "";
    const n = stack.length;
    if (n > 0 && stack[n - 1].pos.builtin) {
      inName = " in " + stack[n - 1].name;
      stack = stack.slice(0, n - 1);
    }
    let s = stack.length > 0 ? "Traceback (most recent call last):\n" : "";
    for (const fr of stack) s += `  ${fr.pos}: in ${fr.name}\n`;
    return s + `Error${inName}: ${this.message}`;
  }
}

/** A command module exited with a non-zero status. */
export class ExitError extends Error {
  constructor(code) {
    super(`module exited with status ${code}`);
    this.name = "ExitError";
    this.code = code;
  }
}

class ExitSignal {
  constructor(code) {
    this.code = code;
  }
}

// ---- modules ----

const COL_BITS = 20;

function section(mod, name) {
  const s = WebAssembly.Module.customSections(mod, name);
  return s.length ? JSON.parse(decoder.decode(new Uint8Array(s[0]))) : null;
}

/**
 * Reads the starc.meta section of a module.
 * @param {BufferSource | WebAssembly.Module} wasm
 */
export function readMeta(wasm) {
  const mod = wasm instanceof WebAssembly.Module ? wasm : new WebAssembly.Module(wasm);
  const meta = section(mod, "starc.meta");
  if (!meta) throw new Error("starc: module has no starc.meta section (not built by starc?)");
  return meta;
}

/**
 * Instantiates a starc module and runs its top-level code; a command
 * module runs to completion.
 *
 * @param {BufferSource | WebAssembly.Module} wasm
 * @param {{
 *   stdout?: (text: string) => void,
 *   stderr?: (text: string) => void,
 *   host?: Record<string, (args: any[], kwargs: Map<string, any> | null, call: {chargeFuel(n: number | bigint): void}) => any>,
 *   initFuel?: number | bigint,
 * }} [config]
 */
export async function load(wasm, config = {}) {
  const mod = wasm instanceof WebAssembly.Module ? wasm : await WebAssembly.compile(wasm);
  const m = new Module(mod, config);
  await m._start();
  return m;
}

export class Module {
  constructor(mod, config) {
    this.meta = section(mod, "starc.meta");
    if (!this.meta) throw new Error("starc: module has no starc.meta section (not built by starc?)");
    /** The starc.debug section, or null. */
    this.debugInfo = section(mod, "starc.debug");
    this._mod = mod;
    this._config = config;
    this._busy = false;
    this._pending = null;
  }

  async _start() {
    const cfg = this._config;
    const out = cfg.stdout ?? (() => {});
    const err = cfg.stderr ?? (() => {});
    const host = cfg.host ?? {};
    const imports = { wasi_snapshot_preview1: {}, starc_host: {} };
    const mem = () => new Uint8Array(this._exports.memory.buffer);
    const view = () => new DataView(this._exports.memory.buffer);
    const read = (ptr, n) => {
      if (ptr + n > mem().length) throw new RangeError("starc: buffer out of range");
      return mem().slice(ptr, ptr + n);
    };
    for (const imp of WebAssembly.Module.imports(this._mod)) {
      if (imp.kind !== "function") throw new Error(`starc: unexpected import ${imp.module}.${imp.name}`);
      if (imp.module === "wasi_snapshot_preview1") {
        if (imp.name === "fd_write") {
          imports.wasi_snapshot_preview1.fd_write = (fd, iovs, n, nwritten) => {
            const v = view();
            let total = 0;
            const parts = [];
            for (let i = 0; i < n; i++) {
              const p = v.getUint32(iovs + 8 * i, true);
              const l = v.getUint32(iovs + 8 * i + 4, true);
              parts.push(read(p, l));
              total += l;
            }
            const bytes = new Uint8Array(total);
            let o = 0;
            for (const p of parts) {
              bytes.set(p, o);
              o += p.length;
            }
            (fd === 2 ? err : out)(decoder.decode(bytes));
            v.setUint32(nwritten, total, true);
            return 0;
          };
        } else if (imp.name === "proc_exit") {
          imports.wasi_snapshot_preview1.proc_exit = (code) => {
            throw new ExitSignal(code);
          };
        } else {
          throw new Error(`starc: module imports ${imp.module}.${imp.name}, which is not part of the starc ABI`);
        }
      } else if (imp.module === "starc_host") {
        if (imp.name === "starc_result") {
          imports.starc_host.starc_result = (dst) => {
            if (this._pending === null) throw new Error("starc: starc_result called without a pending host result");
            const p = this._pending;
            this._pending = null;
            if (dst + p.length > mem().length) throw new RangeError("starc: buffer out of range");
            mem().set(p, dst);
          };
        } else if (imp.name === "starc_print") {
          imports.starc_host.starc_print = (ptr, n) => out(decoder.decode(read(ptr, n)));
        } else if (imp.name === "starc_dbg") {
          imports.starc_host.starc_dbg = () => {}; // debug builds: no debugger attached
        } else {
          const fn = host[imp.name];
          if (typeof fn !== "function") {
            throw new Error(`starc: module imports host function "${imp.name}", which config.host does not provide`);
          }
          imports.starc_host[imp.name] = (ptr, n) => this._hostCall(imp.name, fn, read(ptr, n));
        }
      } else {
        throw new Error(`starc: module imports ${imp.module}.${imp.name}, which is not part of the starc ABI`);
      }
    }
    const inst = await WebAssembly.instantiate(this._mod, imports);
    this._exports = inst.exports;
    if (cfg.initFuel !== undefined && cfg.initFuel !== 0) this.setFuel(cfg.initFuel);
    if (this.meta.model !== "reactor") {
      try {
        this._exports._start();
      } catch (e) {
        if (e instanceof ExitSignal) {
          if (e.code !== 0) throw new ExitError(e.code);
          return;
        }
        throw e;
      }
      return;
    }
    try {
      this._exports._initialize();
    } catch (e) {
      throw this._error(e);
    }
  }

  _hostCall(name, fn, input) {
    this._pending = null;
    const { args, kwargs } = decodeCall(input);
    const call = { chargeFuel: (n) => this._charge(n) };
    let res;
    let error = null;
    // As in the Go host, the message of an error is that of the host
    // function; a result that cannot be encoded names the function.
    try {
      res = fn(args, kwargs, call);
    } catch (e) {
      error = e instanceof Error ? e.message : String(e);
    }
    if (error === null) {
      try {
        if (res instanceof Promise) throw new Error("host functions must be synchronous, got a Promise");
        res = encode(res);
      } catch (e) {
        error = `${name}: invalid result: ${e instanceof Error ? e.message : String(e)}`;
      }
    }
    if (error !== null) {
      let msg = encoder.encode(error);
      if (msg.length > 1 << 16) msg = msg.slice(0, 1 << 16);
      this._pending = msg;
      return ~msg.length;
    }
    if (res.length > 0x7fffffff) {
      this._pending = encoder.encode(`${name}: result too large`);
      return ~this._pending.length;
    }
    this._pending = res;
    return res.length;
  }

  _charge(n) {
    n = BigInt(n);
    const ex = this._exports;
    if (n <= 0n || !ex.starc_fuel || !ex.starc_fuel_set) return;
    let rest = ex.starc_fuel();
    rest = rest < MIN_INT64 + n ? MIN_INT64 : rest - n;
    ex.starc_fuel_set(rest);
  }

  _read(packed) {
    packed = BigInt.asUintN(64, packed);
    const ptr = Number(packed & 0xffffffffn);
    const n = Number(packed >> 32n);
    const buf = new Uint8Array(this._exports.memory.buffer);
    if (ptr + n > buf.length) throw new RangeError("starc: result buffer out of range");
    return buf.slice(ptr, ptr + n);
  }

  _error(e) {
    const ex = this._exports;
    if (!(e instanceof WebAssembly.RuntimeError) || !ex.starc_error) return e;
    const packed = ex.starc_error();
    if (packed === 0n) return e;
    const msg = decoder.decode(this._read(packed));
    const code = ex.starc_error_code ? ex.starc_error_code() || ErrorCode.STARLARK : ErrorCode.STARLARK;
    const stack = [];
    let pos = null;
    if (ex.starc_frames) {
      const n = ex.starc_frames();
      for (let i = 0; i < n; i++) {
        const name = decoder.decode(this._read(ex.starc_frame(i)));
        const p = BigInt.asUintN(64, ex.starc_frame_pos(i));
        let fpos = new Position("<builtin>");
        if (p !== 0xffffffffffffffffn) {
          const hi = Number(p >> 32n);
          const file = hi >>> COL_BITS;
          const filename = this.meta.files?.[file] ?? this.meta.file;
          fpos = new Position(filename, Number(p & 0xffffffffn), hi & ((1 << COL_BITS) - 1));
          pos = fpos;
        }
        stack.push({ name, pos: fpos });
      }
    }
    return new StarlarkError(msg, code, pos, stack);
  }

  /** The exported Starlark functions: [{name, params}]. */
  get exports() {
    return this.meta.exports;
  }

  /** Calls an exported function with positional arguments. */
  call(name, ...args) {
    return this.callKw(name, args);
  }

  /**
   * Calls an exported function with positional and keyword arguments.
   * @param {string} name
   * @param {any[]} args
   * @param {Map<string, any> | Record<string, any>} [kwargs]
   */
  callKw(name, args, kwargs) {
    return decode(this.callRaw(name, encodeCall(args, kwargs)));
  }

  /**
   * Calls an exported function with encoded arguments and returns the
   * encoded result.
   * @param {string} name
   * @param {Uint8Array} payload
   */
  callRaw(name, payload) {
    const fn = this.meta.exports.some((e) => e.name === name) ? this._exports[name] : undefined;
    if (typeof fn !== "function") throw new Error(`starc: module exports no function "${name}"`);
    if (this._busy) throw new Error("starc: module called from one of its own host functions");
    this._busy = true;
    try {
      let ptr;
      try {
        ptr = this._exports.starc_alloc(payload.length);
      } catch (e) {
        throw this._error(e);
      }
      new Uint8Array(this._exports.memory.buffer).set(payload, ptr);
      try {
        return this._read(fn(ptr, payload.length));
      } catch (e) {
        throw this._error(e);
      }
    } finally {
      this._busy = false;
    }
  }

  /** Sets the remaining fuel budget (modules built with fuel metering). */
  setFuel(n) {
    if (!this._exports.starc_fuel_set) throw new Error("starc: module was built without fuel metering");
    n = BigInt(n);
    if (n < 0n) throw new RangeError(`starc: negative fuel budget ${n}`);
    this._exports.starc_fuel_set(n);
  }

  /** The remaining fuel budget, as a bigint. */
  fuel() {
    if (!this._exports.starc_fuel) throw new Error("starc: module was built without fuel metering");
    return this._exports.starc_fuel();
  }

  /**
   * The fuel profile of a module built with Options.Profile: per function,
   * the fuel used by its own code and the number of calls.
   */
  profile() {
    const ex = this._exports;
    if (!ex.starc_profile_fuel || !this.debugInfo) throw new Error("starc: module was built without a profile");
    const d = this.debugInfo;
    return d.functions.map((f, i) => ({
      name: f.name,
      kind: f.kind,
      pos: f.line ? new Position(d.files?.[f.file ?? 0] ?? d.file, f.line, f.col) : null,
      fuel: ex.starc_profile_fuel(i),
      calls: ex.starc_profile_calls(i),
    }));
  }

  /** Clears the fuel profile. */
  resetProfile() {
    if (!this._exports.starc_profile_reset) throw new Error("starc: module was built without a profile");
    this._exports.starc_profile_reset();
  }
}
