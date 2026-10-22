// Formats values decoded by host.mjs in Starlark syntax.
import { Tuple, Struct, Opaque } from "./host.mjs";

export function show(v) {
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
