// Keys live in code, texts in ru.json / en.json. Both files have the same shape (the
// type check below fails the build when en.json misses a key); plural forms are written
// inside the string, ICU-style: "{n, plural, one {# день} few {# дня} other {# дней}}".
import { useSyncExternalStore } from "react";
import en from "./en.json";
import ru from "./ru.json";

export type Locale = "ru" | "en";
export const LOCALES: { id: Locale; label: string }[] = [
  { id: "ru", label: "Русский" },
  { id: "en", label: "English" },
];

type Dict = typeof ru;
const dicts: Record<Locale, Dict> = { ru, en: en satisfies Dict };

type Leaves<T> = { [K in keyof T & string]: T[K] extends string ? K : `${K}.${Leaves<T[K]>}` }[keyof T & string];
export type Key = Leaves<Dict>;
export type Params = Record<string, string | number>;

const STORAGE = "mikan.lang";
let locale: Locale = detect();
const listeners = new Set<() => void>();

function detect(): Locale {
  try {
    const saved = localStorage.getItem(STORAGE);
    if (saved === "ru" || saved === "en") return saved;
  } catch {
    // storage may be blocked; fall back to the browser language
  }
  return navigator.languages.some((l) => l.toLowerCase().startsWith("ru")) ? "ru" : "en";
}

export function getLocale(): Locale {
  return locale;
}

export function setLocale(l: Locale) {
  if (l === locale) return;
  locale = l;
  try {
    localStorage.setItem(STORAGE, l);
  } catch {
    // not persisted; the choice still applies to this tab
  }
  document.documentElement.lang = l;
  listeners.forEach((f) => f());
}

/** Re-renders on a language switch; the apps key their root on it. */
export function useLocale(): Locale {
  return useSyncExternalStore(
    (f) => {
      listeners.add(f);
      return () => listeners.delete(f);
    },
    () => locale,
  );
}

export function t(key: Key, params?: Params): string {
  const s = lookup(dicts[locale], key) ?? lookup(dicts.ru, key) ?? key;
  return params ? format(s, params) : s;
}

/** For keys built at runtime (API error codes, preset ids): undefined when missing. */
export function tMaybe(key: string, params?: Params): string | undefined {
  const s = lookup(dicts[locale], key);
  return s === undefined ? undefined : params ? format(s, params) : s;
}

function lookup(d: unknown, key: string): string | undefined {
  let cur: unknown = d;
  for (const part of key.split(".")) {
    if (typeof cur !== "object" || cur === null) return undefined;
    cur = (cur as Record<string, unknown>)[part];
  }
  return typeof cur === "string" ? cur : undefined;
}

const pluralRules: Partial<Record<Locale, Intl.PluralRules>> = {};
const numberFormats: Partial<Record<Locale, Intl.NumberFormat>> = {};

function format(s: string, p: Params): string {
  return s.replace(/\{(\w+)(?:,\s*plural,\s*((?:[^{}]|\{[^{}]*\})*))?\}/g, (_, name: string, forms?: string) => {
    const v = p[name];
    if (forms === undefined) return v === undefined ? "" : String(v);
    const n = Number(v);
    const options = new Map<string, string>();
    for (const m of forms.matchAll(/(=?\w+)\s*\{([^{}]*)\}/g)) options.set(m[1]!, m[2]!);
    const rules = (pluralRules[locale] ??= new Intl.PluralRules(locale));
    const body = options.get(`=${n}`) ?? options.get(rules.select(n)) ?? options.get("other") ?? "";
    const nf = (numberFormats[locale] ??= new Intl.NumberFormat(locale, { maximumFractionDigits: 1 }));
    return body.replace(/#/g, nf.format(n));
  });
}

document.documentElement.lang = locale;
