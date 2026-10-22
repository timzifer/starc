// The starc playground: compiles Starlark in the browser with the
// compiler built as WebAssembly and runs the module with host.mjs.
import { loadCompiler } from "./compiler.mjs";
import { load, StarlarkError, ExitError, Tuple, Struct, Opaque } from "./host.mjs";

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
};

let compiler = null;
let errorLines = new Set();

// ---- editor ----

function renderGutter() {
  const n = els.source.value.split("\n").length;
  let html = "";
  for (let i = 1; i <= n; i++) html += errorLines.has(i) ? `<span class="err">${i}</span>\n` : `${i}\n`;
  els.gutter.innerHTML = html;
  els.gutter.scrollTop = els.source.scrollTop;
}

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

// ---- values ----

function show(v) {
  if (v === null || v === undefined) return "None";
  if (v === true) return "True";
  if (v === false) return "False";
  if (typeof v === "bigint") return v.toString();
  if (typeof v === "number") return Number.isNaN(v) ? "nan" : v === Infinity ? "+inf" : v === -Infinity ? "-inf" : String(v);
  if (typeof v === "string") return JSON.stringify(v);
  if (v instanceof Tuple) return v.length === 1 ? `(${show(v[0])},)` : `(${v.map(show).join(", ")})`;
  if (Array.isArray(v)) return `[${v.map(show).join(", ")}]`;
  if (v instanceof Map) return `{${[...v].map(([k, x]) => `${show(k)}: ${show(x)}`).join(", ")}}`;
  if (v instanceof Struct) return `struct(${[...v.fields].map(([k, x]) => `${k} = ${show(x)}`).join(", ")})`;
  if (v instanceof Opaque) return v.repr;
  return String(v);
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

async function run() {
  if (!compiler) return;
  els.output.replaceChildren();
  els.result.textContent = "–";
  els.result.className = "";
  els.fuel.textContent = "–";
  els.profile.replaceChildren();
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

  let budget;
  try {
    budget = BigInt(els.budget.value || "0");
    if (budget <= 0n) throw new Error();
  } catch {
    appendOutput("The fuel budget must be a positive integer.\n", "err");
    return;
  }
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
  let args;
  try {
    args = JSON.parse(els.args.value || "[]");
    if (!Array.isArray(args)) throw new Error("not an array");
  } catch (e) {
    appendOutput(`Arguments: ${e.message}; give a JSON array, such as [1, "two"].\n`, "err");
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
  run();
} catch (e) {
  setStatus(`cannot load the compiler: ${e.message}`, "error");
}
