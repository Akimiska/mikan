import { useState, type SetStateAction } from "react";

/** Structural comparison for form values (plain objects, arrays and primitives). */
export function same(a: unknown, b: unknown): boolean {
  return a === b || JSON.stringify(a) === JSON.stringify(b);
}

/**
 * A form's draft next to the value the server holds.
 *
 * `source` is what the form starts from, rebuilt on every render from the query's data
 * (a fresh object each time is fine: it is compared by value, not by identity). While the
 * user has not touched the form it follows the server; once the draft differs from the
 * value it was made from, a refetch, a save of another card or a background poll leaves
 * the typed input alone. After a successful save the server's value equals the draft, so
 * `dirty` drops back to false without any effect.
 *
 * Use it instead of `useState(init)` + `useEffect(() => setForm(init()), [data])`, which
 * wipes the input whenever the query hands back a new object. For a form of another record
 * (a user, a node) key the component by the record's id so one draft never leaks into the
 * next record.
 */
export function useDraft<T>(source: T) {
  const [draft, setDraft] = useState(source);
  const [base, setBase] = useState(source);
  // Adjusting state while rendering is React's way to derive state from props: the render
  // is thrown away and repeated with the new state, no flash of the stale draft.
  if (!same(source, base)) {
    setBase(source);
    if (same(draft, base)) setDraft(source);
  }
  return {
    draft,
    setDraft,
    /** The draft differs from the server's value. */
    dirty: !same(draft, source),
    /** Throws the edits away. */
    reset: () => setDraft(source),
  };
}

/** The setters of a form's fields, each taking a value or an updater of the field's own. */
export type FieldSetters<T> = { [K in keyof T]-?: (v: SetStateAction<T[K]>) => void };

/**
 * A form of many fields in one state, instead of a useState a field: set.name(v) changes one
 * of them, replace(next) all of them at once (the form opened on another record). The fields
 * are the keys init gives.
 */
export function useFields<T extends object>(init: () => T) {
  const [form, replace] = useState(init);
  const [set] = useState(() => {
    const out = {} as FieldSetters<T>;
    for (const k of Object.keys(init()) as (keyof T)[]) {
      out[k] = (v) => replace((f) => ({ ...f, [k]: typeof v === "function" ? (v as (cur: T[typeof k]) => T[typeof k])(f[k]) : v }));
    }
    return out;
  });
  return [form, set, replace] as const;
}
