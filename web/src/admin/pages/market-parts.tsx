import { ShieldAlert } from "lucide-react";
import type { ReactNode } from "react";
import { Pill } from "../../components/ui";
import { t } from "../../i18n";
import { num } from "../../lib/format";
import type { AddonKind } from "../search";
import { unofficialLabel, type MarketOrigin } from "./market-common";

// The small pieces the addons tabs, their dialogs and the sources drawer all show for an
// addon that mikan has not checked: the warning with the shield and the mark of its source.

/** The kinds of addons a tab lists, as filter chips with how many of each the search finds
 * (no counts while it loads); "all" first. */
export function KindChips({ kinds, value, count, loading, onChange }: { kinds: readonly AddonKind[]; value: AddonKind | undefined; count: (k?: AddonKind) => number; loading: boolean; onChange: (k: AddonKind | undefined) => void }) {
  return (
    <div className="-mx-3 flex items-center gap-2 overflow-x-auto px-3 pb-1 [scrollbar-width:none] lg:mx-0 lg:flex-wrap lg:overflow-visible lg:px-0 lg:pb-0 [&::-webkit-scrollbar]:hidden" role="group" aria-label={t("addons.filterKind")}>
      {([undefined, ...kinds] as const).map((k) => (
        <button key={k ?? "all"} type="button" className="chip shrink-0" aria-pressed={value === k} onClick={() => onChange(k)}>
          {k ? t(`addons.kinds.${k}`) : t("addons.kinds.all")}
          {loading ? null : <span className="chip-count num">{num(count(k))}</span>}
        </button>
      ))}
    </div>
  );
}

/** A warning (or an error) about trust: the shield, then the words. The parent supplies the margin. */
export function ShieldBanner({ tone = "warn", role, className, children }: { tone?: "warn" | "err"; role?: "note" | "alert" | "status"; className?: string; children: ReactNode }) {
  return (
    <div className={`banner ${tone} items-start${className ? ` ${className}` : ""}`} role={role ?? (tone === "err" ? "alert" : "note")}>
      <ShieldAlert size={16} className="mt-0.5 shrink-0" aria-hidden />
      <span className="min-w-0 flex-1">{children}</span>
    </div>
  );
}

/** The badge of an addon from a source: whose it is, and what that does not vouch for in its hint. */
export function OriginMark({ origin }: { origin: MarketOrigin }) {
  if (origin.revoked) {
    return (
      <span title={t("market.revokedText", { reason: origin.revoked })}>
        <Pill tone="bad">{t("market.revoked")}</Pill>
      </span>
    );
  }
  return (
    <span title={t("market.unofficialHint", { title: origin.title })}>
      <Pill tone="warn">{unofficialLabel(origin)}</Pill>
    </span>
  );
}
