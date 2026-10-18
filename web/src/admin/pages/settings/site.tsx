import { Check, ExternalLink, FileCode2, Gamepad2, Wrench, XCircle } from "lucide-react";
import type { Schemas } from "../../../api/client";
import { useSaveSettings } from "./shared";
import { t } from "../../../i18n";

type Site = Schemas["SettingsView"]["site_template"];

const OPTIONS: { id: Site; icon: typeof Wrench }[] = [
  { id: "", icon: XCircle },
  { id: "maintenance", icon: Wrench },
  { id: "cats", icon: Gamepad2 },
];

/**
 * What a stranger sees at the panel's address: a bare 404, or a page that looks like an
 * ordinary site. The admin's own files in data/panel/www win over any of them.
 */
export function SiteCard({ s }: { s: Schemas["SettingsView"] }) {
  const save = useSaveSettings();
  // The page sits at the root of the panel's address, where the admin path is not.
  const root = s.admin_url ? new URL("/", s.admin_url).toString() : "";
  return (
    <section className="card glass reveal" style={{ "--i": 2 } as React.CSSProperties}>
      <div className="card-head">
        <div>
          <h2 className="card-title">{t("settings.site.title")}</h2>
          <div className="card-sub">{t("settings.site.sub")}</div>
        </div>
      </div>
      {s.site_own ? (
        <div className="banner info mb-3" role="status">
          <FileCode2 size={18} className="shrink-0" aria-hidden />
          <span>{t("settings.site.own")}</span>
        </div>
      ) : null}
      <div role="radiogroup" aria-label={t("settings.site.title")} className="theme-tiles">
        {OPTIONS.map((o) => {
          const checked = s.site_template === o.id;
          const Icon = o.icon;
          return (
            <button
              key={o.id || "none"}
              type="button"
              role="radio"
              aria-checked={checked}
              className="theme-tile"
              disabled={save.isPending}
              onClick={() => !checked && save.mutate({ site_template: o.id })}
            >
              <span className="grid h-16 place-items-center rounded-xl bg-[var(--surface)] text-[var(--ink-500)]" aria-hidden>
                <Icon size={24} />
              </span>
              <span className="theme-tile-name">
                <span>{t(`settings.site.option.${o.id || "none"}`)}</span>
                {checked ? <Check size={16} aria-hidden /> : null}
              </span>
            </button>
          );
        })}
      </div>
      <p className="mt-3 text-xs text-[var(--ink-500)]">{t(`settings.site.hint.${s.site_template || "none"}`)}</p>
      {root ? (
        <a className="mt-3 inline-flex items-center gap-2 text-[13px] font-medium text-[var(--mikan-700)] hover:underline" href={root} target="_blank" rel="noreferrer">
          <ExternalLink size={16} aria-hidden /> {t("settings.site.open")}
        </a>
      ) : null}
    </section>
  );
}
