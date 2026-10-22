// The debugger's worker: it runs a debug build natively with host.mjs and
// stops it at statements by blocking in the module's debug hook
// (Atomics.wait), while the page shows the stack and the variables and
// sends the next command through shared memory. A stop changes neither the
// results nor the fuel of the program.
import { load, StarlarkError, ExitError } from "./host.mjs";
import { show } from "./show.mjs";
import { CMD, PAUSE, BREAKS_OFFSET, NONE, CONTINUE, STEP_INTO, STEP_OVER, STEP_OUT, ABORT } from "./debug-protocol.mjs";

class Aborted extends Error {}

self.onmessage = async ({ data }) => {
  if (data.type !== "start") return;
  const { wasm, fn, args, budget, errors, shared, filename } = data;
  const ctrl = new Int32Array(shared, 0, BREAKS_OFFSET / 4);
  const breaks = new Uint8Array(shared, BREAKS_OFFSET);

  // Stepping state: after a step command, stop when the depth allows it.
  let mode = CONTINUE;
  let depth = 0;
  const shouldStop = (s) => {
    if (s.err) return true;
    if (Atomics.load(ctrl, PAUSE)) return true;
    if (s.pos.filename === filename && s.pos.line < breaks.length && breaks[s.pos.line]) return true;
    switch (mode) {
      case STEP_INTO:
        return true;
      case STEP_OVER:
        return s.depth <= depth;
      case STEP_OUT:
        return s.depth < depth;
    }
    return false;
  };

  const debug = {
    step: true, // every statement calls back; shouldStop decides
    errors,
    stop(s) {
      if (!shouldStop(s)) return;
      Atomics.store(ctrl, PAUSE, 0);
      Atomics.store(ctrl, CMD, NONE);
      const pos = (p) => (p ? { file: p.filename, line: p.line, col: p.col } : null);
      postMessage({
        type: "stop",
        pos: pos(s.pos),
        err: s.err,
        frames: s.stack().map((f) => ({ name: f.name, kind: f.kind, pos: pos(f.pos), locals: f.locals })),
        globals: s.globals(),
      });
      Atomics.wait(ctrl, CMD, NONE);
      const cmd = Atomics.load(ctrl, CMD);
      if (cmd === ABORT) throw new Aborted("stopped by the debugger");
      mode = cmd;
      depth = s.depth;
      postMessage({ type: "running" });
    },
  };

  const print = (text) => postMessage({ type: "output", text });
  const fail = (e) => {
    if (e instanceof Aborted || e?.message === "stopped by the debugger") return { aborted: true };
    if (e instanceof StarlarkError) return { error: e.backtrace(), pos: e.pos && { file: e.pos.filename, line: e.pos.line, col: e.pos.col }, code: e.code };
    if (e instanceof ExitError) return { error: e.message };
    return { error: String(e?.message ?? e) };
  };

  let m;
  try {
    m = await load(wasm, { stdout: print, stderr: print, initFuel: budget, debug });
  } catch (e) {
    postMessage({ type: "done", ...fail(e) });
    return;
  }
  const initFuel = budget - m.fuel();
  if (!fn) {
    postMessage({ type: "done", result: "None", fuel: initFuel, topLevel: true });
    return;
  }
  m.setFuel(budget);
  let out;
  try {
    out = { result: show(m.call(fn, ...args)) };
  } catch (e) {
    out = fail(e);
  }
  postMessage({ type: "done", ...out, fuel: budget - m.fuel() });
};
