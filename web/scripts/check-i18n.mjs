// Checks the dictionaries before a build. ru and en have the same keys, with the same
// placeholders in each text. The subscription page and the Mini App (sub.html) get only the
// sections of SUB_SECTIONS (vite.config.ts): a key of another section, used by code that
// page reaches, shows as the key itself, and tsc does not see it (any key is a Key).
import { existsSync, readFileSync, statSync } from "node:fs";
import { dirname, relative, resolve } from "node:path";
import { fileURLToPath } from "node:url";

const root = resolve(dirname(fileURLToPath(import.meta.url)), "..");
const read = (p) => readFileSync(resolve(root, p), "utf8");
const problems = [];

// ru and en.
function flat(o, prefix = "", out = {}) {
  for (const [k, v] of Object.entries(o)) {
    const key = prefix ? `${prefix}.${k}` : k;
    if (v && typeof v === "object") flat(v, key, out);
    else out[key] = String(v);
  }
  return out;
}
const ru = flat(JSON.parse(read("src/i18n/ru.json")));
const en = flat(JSON.parse(read("src/i18n/en.json")));
// The names a text substitutes: {name}, and the number of a plural ({n, plural, …}); the
// words of a plural's cases ("=0 {Nobody}") are no names. The cases go until none is left,
// so one that a removed case leaves whole goes too.
const pluralCase = /(?:=\d+|\b(?:zero|one|two|few|many|other))\s*\{[^{}]*\}/g;
const vars = (s) => {
  let bare = s;
  for (let next = bare.replace(pluralCase, ""); next !== bare; next = bare.replace(pluralCase, "")) bare = next;
  return [...new Set([...bare.matchAll(/\{(\w+)(?=[,}])/g)].map((m) => m[1]))].sort().join(", ");
};
for (const k of Object.keys(ru)) {
  if (!(k in en)) problems.push(`en has no ${k}`);
  else if (vars(ru[k]) !== vars(en[k])) problems.push(`${k}: ru has {${vars(ru[k])}}, en {${vars(en[k])}}`);
}
for (const k of Object.keys(en)) if (!(k in ru)) problems.push(`ru has no ${k}`);

// The sections of the subscription page's bundle.
const config = read("vite.config.ts").match(/const SUB_SECTIONS = \[([^\]]*)\]/);
if (!config) throw new Error("SUB_SECTIONS not found in vite.config.ts");
const sections = new Set([...config[1].matchAll(/"([^"]+)"/g)].map((m) => m[1]));

// The files sub.html reaches: its entry and what it imports, type-only imports aside.
const entry = read("sub.html").match(/<script type="module" src="\/([^"]+)"/)[1];
function resolveImport(from, spec) {
  const base = resolve(dirname(from), spec);
  for (const p of [base, `${base}.ts`, `${base}.tsx`, `${base}/index.ts`, `${base}/index.tsx`]) {
    if (existsSync(p) && statSync(p).isFile()) return p;
  }
  return undefined;
}
const reached = new Set();
function walk(file) {
  if (reached.has(file)) return;
  reached.add(file);
  const src = readFileSync(file, "utf8");
  const specs = [
    ...[...src.matchAll(/^(?:import|export)(\s+type)?\s[^;]*?from\s+"(\.[^"]+)";/gms)].filter((m) => !m[1]).map((m) => m[2]),
    ...[...src.matchAll(/import\(\s*"(\.[^"]+)"\s*\)/g)].map((m) => m[1]),
  ];
  for (const spec of specs) {
    const p = resolveImport(file, spec);
    if (p && /\.tsx?$/.test(p)) walk(p);
  }
}
walk(resolve(root, entry));

// Every key those files ask for: a literal one whole, a template one by what comes before
// its first substitution.
for (const file of reached) {
  const src = readFileSync(file, "utf8");
  for (const m of src.matchAll(/\bt(?:Maybe)?\(\s*(["'`])([^"'`]*)\1?/g)) {
    const literal = m[1] !== "`" || !m[2].includes("${");
    const key = m[2].split("${")[0];
    const section = key.split(".")[0];
    if (!key.includes(".")) continue;
    const at = `${relative(root, file).replace(/\\/g, "/")}: ${m[2]}`;
    if (!sections.has(section)) problems.push(`${at} is not in the subscription page's sections (${[...sections].join(", ")})`);
    else if (literal && !(key in ru)) problems.push(`${at} is not in ru.json`);
  }
}

if (problems.length) {
  console.error(problems.map((p) => `i18n: ${p}`).join("\n"));
  process.exit(1);
}
console.log(`i18n: ${Object.keys(ru).length} texts in ru and en, ${reached.size} files of the subscription page`);
