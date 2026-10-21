import type { Schemas } from "../../../api/client";
import { t } from "../../../i18n";
import { period, periodLabel } from "../../../lib/format";
import { LimitPicker } from "../user/limit-picker";
import { useSaveSettings } from "./shared";

type TimesRule = { off?: boolean; limit: number; days: number };

/** "So many times a period" of the subscriber's own unbinds and reissues: off (-1), as often
 * as they like (no limit), or a limit and the days it counts in. */
export function TimesPerPeriod({ label, value, busy, onChange }: { label: string; value: TimesRule; busy: boolean; onChange: (patch: Partial<TimesRule>, done: () => void) => void }) {
  const off = !!value.off;
  return (
    <>
      <LimitPicker
        label={label}
        value={off ? -1 : value.limit === 0 ? null : value.limit}
        presets={[-1, 1, 2, 3, 5]}
        format={(n) => (n < 0 ? t("settings.unbindOff") : String(n))}
        min={1}
        max={100}
        busy={busy}
        onChange={(n, done) => onChange(n === -1 ? { off: true } : { off: false, limit: n ?? 0 }, done)}
      />
      {!off && value.limit > 0 ? (
        <LimitPicker
          label={t("settings.unbindPeriod")}
          value={value.days}
          presets={[1, 7, 30]}
          min={1}
          max={365}
          unlimited={false}
          format={periodLabel}
          fieldUnit={t("settings.unbindDaysUnit")}
          busy={busy}
          onChange={(n, done) => onChange({ days: n ?? 1 }, done)}
        />
      ) : null}
    </>
  );
}

/**
 * How subscribers replace their own link in the bot and the Mini App: off, or so many times
 * in a period. The limit is there because a new link cuts every app off until it is added
 * again, and costs the panel a fresh slot; the admin reissues from the client's card without it.
 */
export function ReissueRules({ s }: { s: Schemas["SettingsView"] }) {
  const save = useSaveSettings();
  const r = s.link_reissue;
  const set = (patch: Partial<Schemas["ReissueRules"]>, done: () => void) => save.mutate({ link_reissue: { ...r, ...patch } }, { onSuccess: done });
  const off = !!r.off;
  const summary = off
    ? t("settings.reissueSummaryOff")
    : r.limit === 0
      ? t("settings.reissueSummaryFree")
      : t("settings.reissueSummary", { n: r.limit, period: period(r.days) });
  return (
    <div className="mt-4 border-t border-[var(--line)] pt-4">
      <h3 className="text-[14px] font-medium">{t("settings.reissueTitle")}</h3>
      <p className="mt-1 mb-3 text-xs text-[var(--ink-500)]">{t("settings.reissueSub")}</p>
      <div className="flex flex-col gap-3">
        <TimesPerPeriod label={t("settings.reissueLimit")} value={r} busy={save.isPending} onChange={set} />
      </div>
      <p className="mt-3 text-xs text-[var(--ink-600)]" role="status">
        {summary}
      </p>
    </div>
  );
}
