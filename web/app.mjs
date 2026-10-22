// The starc playground: compiles Starlark in the browser with the
// compiler built as WebAssembly and runs the module with host.mjs.
import { loadCompiler } from "./compiler.mjs";
import { load, StarlarkError, ExitError } from "./host.mjs";
import { show } from "./show.mjs";
import { CMD, PAUSE, BREAKS_OFFSET, MAX_LINES, CONTINUE, STEP_INTO, STEP_OVER, STEP_OUT } from "./debug-protocol.mjs";

const EXAMPLES = {
  "Fibonacci": {
    fn: "fib", args: "[30]",
    src: `# Fibonacci numbers, exported for the host and printed at start-up.

def fib(n):
    """Returns the n'th Fibonacci number."""
    a, b = 0, 1
    for _ in range(n):
        a, b = b, a + b
    return a

def fib_table(n):
    return {i: fib(i) for i in range(n)}

print("fib(30) =", fib(30))
`,
  },
  "Word count": {
    fn: "count_words", args: '["The quick brown fox jumps over the lazy dog. The dog sleeps.", 3]',
    src: `# Word frequency counting over text supplied by the host.

def count_words(text, top = 3):
    counts = {}
    for word in text.lower().split():
        word = word.strip(".,;:!?\\"'()")
        if word:
            counts[word] = counts.get(word, 0) + 1
    ranked = sorted(counts.items(), key = lambda kv: (-kv[1], kv[0]))
    return [{"word": w, "count": n} for w, n in ranked[:top]]
`,
  },
  "Fuel limit": {
    fn: "collatz_steps", args: "[27]", budget: 200,
    src: `# Every statement, call and loop iteration costs fuel, deterministically.
# With a budget of 200, collatz_steps(27) runs out; raise the budget to 400.

def collatz_steps(n):
    steps = 0
    while n != 1:
        n = n // 2 if n % 2 == 0 else 3 * n + 1
        steps += 1
    return steps
`,
  },
  "Run-time error": {
    fn: "average", args: "[[]]",
    src: `# Errors report the position and the call stack, as go.starlark.net does.
# Click a position to jump to it; try [[3, 4, 5]] as arguments.

def total(xs):
    t = 0
    for x in xs:
        t += x
    return t

def average(xs):
    return total(xs) / len(xs)
`,
  },
  "Structs and sets": {
    fn: "inventory", args: "[]",
    src: `# Structs, sets and dict union, as in go.starlark.net.

Item = lambda name, qty, tags: struct(name = name, qty = qty, tags = set(tags))

ITEMS = [
    Item("apple", 3, ["fruit", "red"]),
    Item("pear", 0, ["fruit"]),
    Item("brick", 12, ["red", "heavy"]),
]

def inventory():
    in_stock = [i for i in ITEMS if i.qty > 0]
    tags = set()
    for i in in_stock:
        tags |= i.tags
    return {"in_stock": [i.name for i in in_stock], "tags": sorted(tags)} | {"count": len(in_stock)}
`,
  },
};

const OPTIONS = {
  filename: "main.star",
  fuel: true,
  profile: true,
  struct: true,
  dialect: { while: true, topLevelControl: true, globalReassign: true, set: true, recursion: true },
};

const $ = (id) => document.getElementById(id);
const els = {
  example: $("example"), status: $("status"), source: $("source"), gutter: $("gutter"), diagnostics: $("diagnostics"),
  fn: $("function"), args: $("args"), budget: $("budget"), run: $("run"),
  result: $("result"), fuel: $("fuel"), module: $("module"), output: $("output"), profile: $("profile").tBodies[0],
  version: $("version"),
  downloadWasm: $("download-wasm"), downloadStar: $("download-star"),
  debug: $("debug"), stopErrors: $("stop-errors"), debugger: $("debugger"), dbgState: $("dbg-state"),
  dbgStack: $("dbg-stack"), dbgVars: $("dbg-vars").tBodies[0],
  dbgContinue: $("dbg-continue"), dbgPause: $("dbg-pause"), dbgOver: $("dbg-over"), dbgInto: $("dbg-into"),
  dbgOut: $("dbg-out"), dbgStop: $("dbg-stop"),
};

let compiler = null;
let errorLines = new Set();
const breakpoints = new Set(); // lines of the main file
let dbg = null; // the running debugger, see debug()
let stopLine = 0; // line of the current stop, 0 if none
let stopIsError = false;

// ---- editor ----

function renderGutter() {
  const n = els.source.value.split("\n").length;
  let html = "";
  for (let i = 1; i <= n; i++) {
    const cls = [breakpoints.has(i) && "bp", errorLines.has(i) && "err", i === stopLine && "cur", i === stopLine && stopIsError && "errstop"];
    html += `<span data-line="${i}" class="${cls.filter(Boolean).join(" ")}">${i}</span>`;
  }
  els.gutter.innerHTML = html;
  els.gutter.scrollTop = els.source.scrollTop;
}

// Clicking a line number toggles a breakpoint, also while debugging.
els.gutter.addEventListener("click", (e) => {
  const line = Number(e.target.closest("span")?.dataset.line);
  if (!line) return;
  if (breakpoints.has(line)) breakpoints.delete(line);
  else breakpoints.add(line);
  if (dbg && line < MAX_LINES) dbg.breaks[line] = breakpoints.has(line) ? 1 : 0;
  renderGutter();
});

function goTo(line, col) {
  const lines = els.source.value.split("\n");
  let offset = 0;
  for (let i = 0; i < line - 1 && i < lines.length; i++) offset += lines[i].length + 1;
  const start = offset + Math.max(0, (col || 1) - 1);
  const end = offset + (lines[line - 1]?.length ?? 0);
  els.source.focus();
  els.source.setSelectionRange(start, Math.max(start, end));
  const lineHeight = parseFloat(getComputedStyle(els.source).lineHeight) || 20;
  els.source.scrollTop = Math.max(0, (line - 4) * lineHeight);
}

els.source.addEventListener("input", () => {
  errorLines = new Set();
  renderGutter();
  saveToHash();
});
els.source.addEventListener("scroll", () => (els.gutter.scrollTop = els.source.scrollTop));
els.source.addEventListener("keydown", (e) => {
  if (e.key === "Tab" && !e.shiftKey) {
    e.preventDefault();
    const { selectionStart: s, selectionEnd: t, value } = els.source;
    els.source.value = value.slice(0, s) + "    " + value.slice(t);
    els.source.setSelectionRange(s + 4, s + 4);
    renderGutter();
  }
});
document.addEventListener("keydown", (e) => {
  if (e.key === "Enter" && (e.ctrlKey || e.metaKey) && !els.run.disabled) {
    e.preventDefault();
    run();
  }
});

// ---- share via the URL ----

function saveToHash() {
  const data = JSON.stringify({ src: els.source.value, fn: els.fn.value, args: els.args.value, budget: els.budget.value });
  const b64 = btoa(String.fromCharCode(...new TextEncoder().encode(data)));
  history.replaceState(null, "", "#" + b64.replace(/\+/g, "-").replace(/\//g, "_").replace(/=+$/, ""));
}

function loadFromHash() {
  if (location.hash.length < 2) return null;
  try {
    const b64 = location.hash.slice(1).replace(/-/g, "+").replace(/_/g, "/");
    return JSON.parse(new TextDecoder().decode(Uint8Array.from(atob(b64), (c) => c.charCodeAt(0))));
  } catch {
    return null;
  }
}

// ---- compile and run ----

function setStatus(text, kind = "") {
  els.status.textContent = text;
  els.status.className = "status " + kind;
}

function showDiagnostics(errors) {
  els.diagnostics.replaceChildren();
  errorLines = new Set(errors.filter((e) => e.line && e.file === OPTIONS.filename).map((e) => e.line));
  for (const e of errors) {
    const li = document.createElement("li");
    li.textContent = e.line ? `${e.file}:${e.line}:${e.col}: ${e.msg}` : e.msg;
    if (e.line && e.file === OPTIONS.filename) li.addEventListener("click", () => goTo(e.line, e.col));
    els.diagnostics.append(li);
  }
  renderGutter();
}

/** Appends text to the output; positions of the main file become links. */
function appendOutput(text, cls = "") {
  const re = /main\.star:(\d+):(\d+)/g;
  let last = 0;
  const frag = document.createDocumentFragment();
  for (const m of text.matchAll(re)) {
    if (m.index > last) frag.append(span(text.slice(last, m.index), cls));
    const a = span(m[0], "pos");
    a.addEventListener("click", () => goTo(Number(m[1]), Number(m[2])));
    frag.append(a);
    last = m.index + m[0].length;
  }
  if (last < text.length) frag.append(span(text.slice(last), cls));
  els.output.append(frag);
}

function span(text, cls) {
  const s = document.createElement("span");
  if (cls) s.className = cls;
  s.textContent = text;
  return s;
}

function updateFunctions(exports) {
  const current = els.fn.value;
  els.fn.replaceChildren(new Option("(top level only)", ""));
  for (const e of exports) els.fn.append(new Option(`${e.name}(${e.params.join(", ")})`, e.name));
  if ([...els.fn.options].some((o) => o.value === current)) els.fn.value = current;
}

function showProfile(prof) {
  els.profile.replaceChildren();
  const total = prof.reduce((s, p) => s + Number(p.fuel), 0);
  for (const p of prof.filter((p) => p.fuel > 0n || p.calls > 0n).sort((a, b) => Number(b.fuel - a.fuel))) {
    const tr = document.createElement("tr");
    const name = document.createElement("td");
    name.textContent = p.name;
    name.append(span(p.pos ? `line ${p.pos.line}` : p.kind, "kind"));
    if (p.pos) {
      name.style.cursor = "pointer";
      name.addEventListener("click", () => goTo(p.pos.line, p.pos.col));
    }
    tr.append(name);
    for (const v of [p.fuel.toLocaleString(), total ? ((100 * Number(p.fuel)) / total).toFixed(1) : "0", p.calls.toLocaleString()]) {
      const td = document.createElement("td");
      td.textContent = v;
      tr.append(td);
    }
    els.profile.append(tr);
  }
}

function showError(e) {
  if (e instanceof StarlarkError) {
    appendOutput(e.backtrace() + "\n", "err");
    if (e.pos && e.pos.filename === OPTIONS.filename) {
      errorLines = new Set([e.pos.line]);
      renderGutter();
    }
    els.result.textContent = e.code === 2 ? "fuel exhausted" : "error";
  } else {
    appendOutput(String(e?.message ?? e) + "\n", "err");
    els.result.textContent = "error";
  }
  els.result.className = "error";
}

/** Reads the fuel budget and the arguments; it reports errors and returns null. */
function inputs() {
  let budget;
  try {
    budget = BigInt(els.budget.value || "0");
    if (budget <= 0n) throw new Error();
  } catch {
    appendOutput("The fuel budget must be a positive integer.\n", "err");
    return null;
  }
  let args;
  try {
    args = JSON.parse(els.args.value || "[]");
    if (!Array.isArray(args)) throw new Error("not an array");
  } catch (e) {
    appendOutput(`Arguments: ${e.message}; give a JSON array, such as [1, "two"].\n`, "err");
    return null;
  }
  return { budget, args };
}

function resetResults() {
  els.output.replaceChildren();
  els.result.textContent = "–";
  els.result.className = "";
  els.fuel.textContent = "–";
  els.profile.replaceChildren();
}

async function run() {
  if (!compiler) return;
  stopDebugger();
  resetResults();
  saveToHash();

  const t0 = performance.now();
  const { wasm, errors } = compiler.compile(els.source.value, OPTIONS);
  const ms = performance.now() - t0;
  showDiagnostics(errors);
  if (!wasm) {
    setStatus(`${errors.length} compile error${errors.length === 1 ? "" : "s"}`, "error");
    els.module.textContent = "–";
    return;
  }
  setStatus(`compiled in ${ms.toFixed(0)} ms`, "ok");
  els.module.textContent = `${(wasm.length / 1024).toFixed(1)} KiB`;

  const input = inputs();
  if (!input) return;
  const { budget, args } = input;
  const print = (s) => appendOutput(s);
  let m;
  try {
    m = await load(wasm, { stdout: print, stderr: print, initFuel: budget });
  } catch (e) {
    showError(e instanceof ExitError ? new Error(e.message) : e);
    return;
  }
  updateFunctions(m.exports);
  const initFuel = budget - m.fuel();
  const fn = els.fn.value;
  if (!fn) {
    els.result.textContent = "None";
    els.fuel.textContent = `${initFuel.toLocaleString()} (top level)`;
    showProfile(m.profile());
    return;
  }
  m.resetProfile();
  m.setFuel(budget);
  try {
    const res = m.call(fn, ...args);
    els.result.textContent = show(res);
  } catch (e) {
    showError(e);
  }
  els.fuel.textContent = `${(budget - m.fuel()).toLocaleString()} of ${budget.toLocaleString()}`;
  showProfile(m.profile());
}

// ---- debugger ----
//
// The program runs natively in a Web Worker (debug-worker.mjs), which
// blocks in the debug hook at a stop; this page shows the stop and sends
// commands through shared memory. SharedArrayBuffer needs cross-origin
// isolation, which coi.js provides on hosts that cannot send the headers.

const MAX_VALUE = 2000; // characters of a value shown

function debug() {
  if (!compiler) return;
  if (!globalThis.crossOriginIsolated) {
    setStatus("the debugger needs a cross-origin isolated page; reload to enable it", "error");
    return;
  }
  stopDebugger();
  resetResults();
  saveToHash();
  const { wasm, errors } = compiler.compile(els.source.value, { ...OPTIONS, profile: false, debug: true });
  showDiagnostics(errors);
  if (!wasm) {
    setStatus(`${errors.length} compile error${errors.length === 1 ? "" : "s"}`, "error");
    return;
  }
  els.module.textContent = `${(wasm.length / 1024).toFixed(1)} KiB (debug build)`;
  const input = inputs();
  if (!input) return;
  const shared = new SharedArrayBuffer(BREAKS_OFFSET + MAX_LINES);
  const breaks = new Uint8Array(shared, BREAKS_OFFSET);
  for (const line of breakpoints) if (line < MAX_LINES) breaks[line] = 1;
  const worker = new Worker("debug-worker.mjs", { type: "module" });
  dbg = { worker, ctrl: new Int32Array(shared, 0, BREAKS_OFFSET / 4), breaks, stopped: false, frames: [], globals: [] };
  worker.onmessage = ({ data }) => onDebugMessage(data);
  worker.onerror = (e) => {
    appendOutput(`debugger: ${e.message}\n`, "err");
    stopDebugger();
  };
  worker.postMessage({
    type: "start", wasm, fn: els.fn.value, args: input.args, budget: input.budget,
    errors: els.stopErrors.checked, shared, filename: OPTIONS.filename,
  });
  els.debugger.hidden = false;
  els.source.readOnly = true;
  setDebugState("running");
}

function onDebugMessage(msg) {
  if (!dbg) return;
  switch (msg.type) {
    case "output":
      appendOutput(msg.text);
      break;
    case "stop":
      dbg.stopped = true;
      dbg.frames = msg.frames;
      dbg.globals = msg.globals;
      stopIsError = !!msg.err;
      setDebugState(msg.err ? `stopped at an error: ${msg.err}` : `paused at line ${msg.pos.line}`, msg.err ? "error" : "ok");
      // The innermost frame of the program is selected.
      selectFrame(dbg.frames.length - 1);
      break;
    case "running":
      dbg.stopped = false;
      stopLine = 0;
      els.dbgStack.replaceChildren();
      els.dbgVars.replaceChildren();
      setDebugState("running");
      renderGutter();
      break;
    case "done": {
      if (msg.aborted) appendOutput("stopped by the debugger\n", "err");
      else if (msg.error) {
        appendOutput(msg.error + "\n", "err");
        if (msg.pos && msg.pos.file === OPTIONS.filename) errorLines = new Set([msg.pos.line]);
        els.result.textContent = msg.code === 2 ? "fuel exhausted" : "error";
        els.result.className = "error";
      } else els.result.textContent = msg.result;
      if (msg.fuel !== undefined) {
        els.fuel.textContent = `${msg.fuel.toLocaleString()}${msg.topLevel ? " (top level)" : ` of ${BigInt(els.budget.value).toLocaleString()}`}`;
      }
      finishDebugger(msg.aborted ? "stopped" : "finished");
      break;
    }
  }
}

function selectFrame(i) {
  if (!dbg) return;
  // Built-ins have no variables; select the nearest frame of the program.
  while (i > 0 && dbg.frames[i].kind !== "function") i--;
  dbg.selected = i;
  const frame = dbg.frames[i];
  els.dbgStack.replaceChildren();
  dbg.frames.forEach((f, j) => {
    const li = document.createElement("li");
    li.append(f.kind === "function" ? f.name : `${f.name} (built-in)`);
    if (f.pos) li.append(span(f.pos.file === OPTIONS.filename ? `line ${f.pos.line}` : `${f.pos.file}:${f.pos.line}`, "where"));
    if (f.kind !== "function") li.className = "builtin";
    else li.addEventListener("click", () => selectFrame(j));
    if (j === i) li.classList.add("sel");
    els.dbgStack.prepend(li); // innermost first
  });
  els.dbgVars.replaceChildren();
  const section = (title) => {
    const tr = els.dbgVars.insertRow();
    tr.className = "section";
    const td = tr.insertCell();
    td.colSpan = 2;
    td.textContent = title;
  };
  const rows = (vars) => {
    for (const v of vars) {
      const tr = els.dbgVars.insertRow();
      tr.insertCell().textContent = v.name;
      const td = tr.insertCell();
      if (!v.bound) td.append(span("unbound", "unbound"));
      else {
        td.textContent = v.value.length > MAX_VALUE ? v.value.slice(0, MAX_VALUE) + "…" : v.value;
        td.title = v.type;
      }
    }
  };
  if (frame?.locals.length) {
    section(`locals of ${frame.name}`);
    rows(frame.locals);
  }
  if (dbg.globals.length) {
    section("globals");
    rows(dbg.globals);
  }
  stopLine = frame?.pos?.file === OPTIONS.filename ? frame.pos.line : 0;
  renderGutter();
  if (stopLine) goTo(stopLine, frame.pos.col);
}

function setDebugState(text, kind = "") {
  els.dbgState.textContent = text;
  els.dbgState.className = "status " + kind;
  const stopped = !!dbg?.stopped;
  for (const b of [els.dbgContinue, els.dbgOver, els.dbgInto, els.dbgOut]) b.disabled = !stopped;
  els.dbgPause.disabled = !dbg || stopped;
  els.dbgStop.disabled = !dbg;
}

/** Resumes a stopped program with a command (CONTINUE, STEP_*). */
function command(cmd) {
  if (!dbg?.stopped) return;
  dbg.stopped = false;
  Atomics.store(dbg.ctrl, CMD, cmd);
  Atomics.notify(dbg.ctrl, CMD);
}

function finishDebugger(state) {
  if (dbg) dbg.worker.terminate();
  dbg = null;
  stopLine = 0;
  stopIsError = false;
  els.source.readOnly = false;
  els.dbgStack.replaceChildren();
  els.dbgVars.replaceChildren();
  setDebugState(state);
  renderGutter();
}

function stopDebugger() {
  if (dbg) {
    finishDebugger("stopped");
    appendOutput("stopped by the debugger\n", "err");
  }
  els.debugger.hidden = true;
}

els.debug.addEventListener("click", debug);
els.dbgContinue.addEventListener("click", () => command(CONTINUE));
els.dbgOver.addEventListener("click", () => command(STEP_OVER));
els.dbgInto.addEventListener("click", () => command(STEP_INTO));
els.dbgOut.addEventListener("click", () => command(STEP_OUT));
els.dbgPause.addEventListener("click", () => dbg && Atomics.store(dbg.ctrl, PAUSE, 1));
els.dbgStop.addEventListener("click", () => {
  if (!dbg) return;
  finishDebugger("stopped");
  appendOutput("stopped by the debugger\n", "err");
});
document.addEventListener("keydown", (e) => {
  const keys = {
    F5: () => (e.shiftKey ? els.dbgStop.click() : dbg?.stopped ? command(CONTINUE) : !dbg && debug()),
    F8: () => command(CONTINUE),
    F10: () => command(STEP_OVER),
    F11: () => command(e.shiftKey ? STEP_OUT : STEP_INTO),
  };
  if (keys[e.key] && compiler) {
    e.preventDefault();
    keys[e.key]();
  }
});

// ---- downloads ----

function download(name, data, type) {
  const url = URL.createObjectURL(new Blob([data], { type }));
  const a = Object.assign(document.createElement("a"), { href: url, download: name });
  document.body.append(a);
  a.click();
  a.remove();
  setTimeout(() => URL.revokeObjectURL(url), 1000);
}

// The downloaded module is compiled from the current source, without the
// profile that the playground records.
els.downloadWasm.addEventListener("click", () => {
  const { wasm, errors } = compiler.compile(els.source.value, { ...OPTIONS, profile: false });
  showDiagnostics(errors);
  if (!wasm) {
    setStatus("fix the compile errors to download the module", "error");
    return;
  }
  download("program.wasm", wasm, "application/wasm");
});
els.downloadStar.addEventListener("click", () => download("program.star", els.source.value, "text/plain"));

// Compiles on edits (debounced) to update the function list and markers.
let timer = 0;
els.source.addEventListener("input", () => {
  clearTimeout(timer);
  timer = setTimeout(() => {
    if (!compiler) return;
    const { wasm, errors } = compiler.compile(els.source.value, OPTIONS);
    showDiagnostics(errors);
    if (wasm) {
      const meta = JSON.parse(new TextDecoder().decode(new Uint8Array(WebAssembly.Module.customSections(new WebAssembly.Module(wasm), "starc.meta")[0])));
      updateFunctions(meta.exports);
    }
  }, 400);
});
els.fn.addEventListener("change", saveToHash);
els.args.addEventListener("change", saveToHash);
els.run.addEventListener("click", run);

// ---- start ----

function setExample(name) {
  const ex = EXAMPLES[name];
  els.source.value = ex.src;
  els.args.value = ex.args;
  els.budget.value = String(ex.budget ?? 1000000);
  updateFunctions([{ name: ex.fn, params: [] }]);
  els.fn.value = ex.fn;
  errorLines = new Set();
  els.diagnostics.replaceChildren();
  renderGutter();
}

for (const name of Object.keys(EXAMPLES)) els.example.append(new Option(name, name));
els.example.addEventListener("change", () => {
  setExample(els.example.value);
  saveToHash();
  if (compiler) run();
});

const shared = loadFromHash();
if (shared) {
  els.source.value = shared.src ?? "";
  els.args.value = shared.args ?? "[]";
  els.budget.value = shared.budget ?? "1000000";
  if (shared.fn) {
    updateFunctions([{ name: shared.fn, params: [] }]);
    els.fn.value = shared.fn;
  }
  renderGutter();
} else {
  setExample(Object.keys(EXAMPLES)[0]);
}

try {
  const t0 = performance.now();
  compiler = await loadCompiler("starc.wasm");
  setStatus(`compiler ready in ${((performance.now() - t0) / 1000).toFixed(1)} s`, "ok");
  els.version.textContent = `starc ${compiler.version}`;
  els.run.disabled = false;
  els.downloadWasm.disabled = false;
  els.debug.disabled = false;
  if (!globalThis.crossOriginIsolated) els.debug.title = "The debugger needs a cross-origin isolated page (see coi.js)";
  run();
} catch (e) {
  setStatus(`cannot load the compiler: ${e.message}`, "error");
}
