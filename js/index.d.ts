// Types of the starc JavaScript API: host.mjs (running modules) and
// compiler.mjs (the compiler as WebAssembly). See docs/js.md.

// ---- host.mjs ----

/** A Starlark tuple. */
export class Tuple<T = any> extends Array<T> {
  static of<T>(...items: T[]): Tuple<T>;
}

/** A Starlark struct; fields are sorted by name. */
export class Struct {
  constructor(fields?: Record<string, any> | Map<string, any>);
  readonly fields: Map<string, any>;
}

/** An opaque host value. */
export class Opaque {
  constructor(o: { type: string; repr: string; payload?: Uint8Array; falsy?: boolean; unhashable?: boolean });
  type: string;
  repr: string;
  payload: Uint8Array;
  falsy: boolean;
  unhashable: boolean;
}

/** Marks a number to be passed as a Starlark float even if it is integral. */
export class Float {
  constructor(value: number);
  value: number;
}

/**
 * A value that crosses the boundary. Encoding: null/undefined → None,
 * booleans, bigint → int, safe integers → int, other numbers and Float →
 * float, strings, arrays → list, Tuple → tuple, Map and plain objects →
 * dict, Struct, Opaque. Decoding gives numbers for ints (bigints outside
 * the safe integer range) and floats, arrays for lists, Tuples, Maps for
 * dicts, Structs and Opaques.
 */
export type Value = any;

export function encode(v: Value): Uint8Array;
export function decode(bytes: Uint8Array): Value;
export function encodeCall(args: Value[], kwargs?: Map<string, Value> | Record<string, Value>): Uint8Array;
export function decodeCall(bytes: Uint8Array): { args: Value[]; kwargs: Map<string, Value> | null };

export const ErrorCode: Readonly<{ STARLARK: 1; FUEL: 2; MEMORY: 3; DEPTH: 4; SIZE: 5 }>;

export class Position {
  constructor(filename: string, line?: number, col?: number);
  filename: string;
  line: number;
  col: number;
  readonly builtin: boolean;
  toString(): string;
}

/** A Starlark run-time error. */
export class StarlarkError extends Error {
  code: number;
  /** The failing operation in the program, or null. */
  pos: Position | null;
  /** The active calls, outermost first. */
  callStack: { name: string; pos: Position }[];
  /** As go.starlark.net's EvalError.Backtrace. */
  backtrace(): string;
}

/** A command module exited with a non-zero status (its error code). */
export class ExitError extends Error {
  code: number;
}

/** A host function: receives the arguments and returns a Value; a thrown error becomes a Starlark error. */
export type HostFunc = (
  args: Value[],
  kwargs: Map<string, Value> | null,
  call: { chargeFuel(n: number | bigint): void },
) => Value;

export interface Config {
  /** Output of print (and of errors of command modules on stderr). */
  stdout?: (text: string) => void;
  stderr?: (text: string) => void;
  /** Host functions by their dotted names. */
  host?: Record<string, HostFunc>;
  /** Fuel budget of the top-level code; 0 or absent is unlimited. */
  initFuel?: number | bigint;
}

export interface Meta {
  version: number;
  model: "reactor" | "command";
  exports: { name: string; params: string[] }[];
  host?: string[];
  no_wasi?: boolean;
  fuel?: Record<string, unknown>;
  limits: Record<string, number>;
  finite_floats?: boolean;
  file?: string;
  files?: string[];
}

export interface ProfileEntry {
  name: string;
  kind: "toplevel" | "function" | "builtin" | "host";
  pos: Position | null;
  fuel: bigint;
  calls: bigint;
}

export class Module {
  readonly meta: Meta;
  readonly debugInfo: any | null;
  readonly exports: { name: string; params: string[] }[];
  call(name: string, ...args: Value[]): Value;
  callKw(name: string, args: Value[], kwargs?: Map<string, Value> | Record<string, Value>): Value;
  callRaw(name: string, payload: Uint8Array): Uint8Array;
  setFuel(n: number | bigint): void;
  fuel(): bigint;
  profile(): ProfileEntry[];
  resetProfile(): void;
}

/** Instantiates a starc module and runs its top-level code. */
export function load(wasm: BufferSource | WebAssembly.Module, config?: Config): Promise<Module>;
export function readMeta(wasm: BufferSource | WebAssembly.Module): Meta;

// ---- compiler.mjs ----

export interface CompileOptions {
  filename?: string;
  command?: boolean;
  exports?: string[];
  noNames?: boolean;
  host?: { name: string; cost?: number }[];
  noWasi?: boolean;
  fuel?: boolean;
  profile?: boolean;
  maxMemoryPages?: number;
  maxCallDepth?: number;
  maxInputBytes?: number;
  maxResultBytes?: number;
  finiteFloats?: boolean;
  struct?: boolean;
  noBacktrace?: boolean;
  dialect?: { while?: boolean; topLevelControl?: boolean; globalReassign?: boolean; recursion?: boolean; set?: boolean };
  /** Sources of the modules that load statements name. */
  files?: Record<string, string>;
}

export interface CompileError {
  file: string;
  /** 0 if the error has no position. */
  line: number;
  col: number;
  msg: string;
}

export class Compiler {
  readonly version: string;
  compile(src: string, options?: CompileOptions): { wasm: Uint8Array | null; errors: CompileError[] };
}

/** Starts the compiler (starc.wasm); Go's wasm_exec.js must be loaded first. */
export function loadCompiler(
  source: BufferSource | WebAssembly.Module | Response | Promise<Response> | string | URL,
): Promise<Compiler>;
