// The starc compiler in JavaScript: the Go compiler built with GOOS=js
// GOARCH=wasm (cmd/starc-js). Go's wasm_exec.js must be loaded first, so
// that globalThis.Go exists. See docs/js.md.

/**
 * Starts the compiler.
 * @param {BufferSource | WebAssembly.Module | Response | Promise<Response> | string | URL} source
 *   the compiler (starc.wasm): its bytes, a compiled module, a fetch
 *   response, or a URL to fetch
 * @returns {Promise<Compiler>}
 */
export async function loadCompiler(source) {
  if (typeof globalThis.Go !== "function") {
    throw new Error("starc: load Go's wasm_exec.js before the compiler");
  }
  if (typeof source === "string" || source instanceof URL) source = fetch(source);
  const go = new globalThis.Go();
  let instance;
  if (source instanceof WebAssembly.Module) {
    instance = await WebAssembly.instantiate(source, go.importObject);
  } else {
    const resp = await source;
    if (typeof Response !== "undefined" && resp instanceof Response) {
      if (!resp.ok) throw new Error(`starc: cannot load the compiler: ${resp.status} ${resp.statusText}`);
      ({ instance } = WebAssembly.instantiateStreaming
        ? await WebAssembly.instantiateStreaming(resp, go.importObject)
        : await WebAssembly.instantiate(await resp.arrayBuffer(), go.importObject));
    } else {
      ({ instance } = await WebAssembly.instantiate(resp, go.importObject));
    }
  }
  // The Go program defines starcCompile and then waits forever.
  go.run(instance);
  if (typeof globalThis.starcCompile !== "function") throw new Error("starc: the compiler did not start");
  return new Compiler(globalThis.starcCompile, globalThis.starcVersion);
}

export class Compiler {
  constructor(compile, version) {
    this._compile = compile;
    /** The version of starc. */
    this.version = version;
  }

  /**
   * Compiles a Starlark program.
   * @param {string} src
   * @param {object} [options] see docs/js.md; files maps module names to
   *   the sources of the modules that load statements name
   * @returns {{wasm: Uint8Array | null, errors: {file: string, line: number, col: number, msg: string}[]}}
   */
  compile(src, options = {}) {
    const res = this._compile(src, JSON.stringify(options));
    return { wasm: res.wasm ?? null, errors: JSON.parse(res.errors) };
  }
}
