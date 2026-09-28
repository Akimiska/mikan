import clsx from "clsx";
import { LOCALES, setLocale, t, useLocale } from "../i18n";

/** Compact RU/EN switch; the choice is remembered in this browser only. */
export function LangSwitch({ className }: { className?: string }) {
  const current = useLocale();
  return (
    <div className={clsx("seg lang-switch", className)} role="group" aria-label={t("common.language")}>
      {LOCALES.map((l) => (
        <button key={l.id} type="button" aria-pressed={l.id === current} lang={l.id} title={l.label} onClick={() => setLocale(l.id)}>
          {l.id.toUpperCase()}
        </button>
      ))}
    </div>
  );
}
