// The protocol between the playground and its debugger worker
// (debug-worker.mjs): shared memory of Int32 slots, then one byte per
// line of the main file that marks a breakpoint.
export const CMD = 0; // command after a stop, set by the page
export const PAUSE = 1; // 1: stop at the next statement
export const BREAKS_OFFSET = 64; // bytes
export const MAX_LINES = 1 << 16;

// Commands.
export const NONE = 0;
export const CONTINUE = 1;
export const STEP_INTO = 2;
export const STEP_OVER = 3;
export const STEP_OUT = 4;
export const ABORT = 5;
