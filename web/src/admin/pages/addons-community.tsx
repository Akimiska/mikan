import type { UseQueryResult } from "@tanstack/react-query";
import { Search, Users } from "lucide-react";
import { useEffect, useRef, useState } from "react";
import { errorText } from "../../api/client";
import type { useAddons } from "../../api/hooks";
import { Button, EmptyState, ErrorState } from "../../components/ui";
import { t } from "../../i18n";
import { num } from "../../lib/format";
import type { AddonKind } from "../search";
import type { AddonActions } from "./addons-installed";
import { CatalogCard, CatalogSkeleton, toItem } from "./addons-catalog";
import { queuedIds, type Market } from "./market-common";
import { ShieldBanner } from "./market-parts";

const KINDS = ["bot", "ui"] as const satisfies readonly AddonKind[];

/**
 * The registry of community addons: third-party authors' addons that mikan lists and does not
 * vouch for. The warning stays on top of the list whatever the admin does; installing and
 * updating one each ask them to take the risk again (ApproveDialog).
 */
export function CommunityTab({
  market,
  pay,
  act,
  q,
  kind,
  onSearch,
}: {
  market: UseQueryResult<Market>;
  pay: ReturnType<typeof useAddons>;
  act: AddonActions;
  q: string;
  kind: AddonKind | undefined;
  onSearch: (next: { q?: string; kind?: AddonKind }) => void;
}) {
  const m = market.data;
  const [text, setText] = useState(q);
  const written = useRef(q);
  const search = useRef(onSearch);
  useEffect(() => {
    search.current = onSearch;
  });
  useEffect(() => {
    if (q !== written.current) {
      written.current = q;
      setText(q);
    }
  }, [q]);
  useEffect(() => {
    if (text === written.current) return;
    const timer = window.setTimeout(() => {
      written.current = text;
      search.current({ q: text.trim() ? text : undefined });
    }, 250);
    return () => window.clearTimeout(timer);
  }, [text]);

  const failed = (market.isError && !m) || !!m?.community_error || !!m?.catalog_error;
  const loading = !m && !market.isError;
  const queued = queuedIds(m, pay.data);
  const items = (m?.community ?? []).map((e) => toItem(e, e.kind, e)).sort((a, b) => a.name.localeCompare(b.name));
  const needle = q.trim().toLowerCase();
  const found = needle ? items.filter((i) => [i.name, i.text, i.id, i.market?.origin?.title ?? ""].some((s) => s.toLowerCase().includes(needle))) : items;
  const shown = kind ? found.filter((i) => i.kind === kind) : found;
  const counts = (k?: AddonKind) => (k ? found.filter((i) => i.kind === k).length : found.length);
  const reset = () => {
    setText("");
    written.current = "";
    onSearch({ q: undefined, kind: undefined });
  };

  return (
    <>
      <ShieldBanner tone="err" role="alert">
        <strong>{t("community.warnTitle")}</strong> {t("community.warnText")}
      </ShieldBanner>
      <div className="flex flex-col gap-3 lg:flex-row lg:items-center lg:justify-between">
        <div className="-mx-3 flex items-center gap-2 overflow-x-auto px-3 pb-1 [scrollbar-width:none] lg:mx-0 lg:flex-wrap lg:overflow-visible lg:px-0 lg:pb-0 [&::-webkit-scrollbar]:hidden" role="group" aria-label={t("addons.filterKind")}>
          {([undefined, ...KINDS] as const).map((k) => (
            <button key={k ?? "all"} type="button" className="chip shrink-0" aria-pressed={kind === k} onClick={() => onSearch({ kind: k })}>
              {k ? t(`addons.kinds.${k}`) : t("addons.kinds.all")}
              {loading ? null : <span className="chip-count num">{num(counts(k))}</span>}
            </button>
          ))}
        </div>
        <label className="search-field lg:max-w-[360px]">
          <Search size={16} aria-hidden />
          <input type="search" placeholder={t("community.searchPlaceholder")} value={text} onChange={(e) => setText(e.target.value)} aria-label={t("addons.searchLabel")} />
        </label>
      </div>
      {loading ? (
        <CatalogSkeleton />
      ) : failed ? (
        <section className="card glass">
          <ErrorState
            text={m?.catalog_error ? t("errors.api.catalog_unavailable") : market.isError && !m ? errorText(market.error) : t("errors.api.community_unavailable")}
            onRetry={market.isFetching ? undefined : () => void market.refetch()}
          />
        </section>
      ) : items.length === 0 ? (
        <section className="card glass">
          <EmptyState icon={Users} title={t("community.empty")} text={t("community.emptyText")} />
        </section>
      ) : shown.length === 0 ? (
        <section className="card glass">
          <EmptyState search title={t("addons.nothingFound")} text={t("addons.nothingFoundText")}>
            <Button onClick={reset}>{t("addons.resetSearch")}</Button>
          </EmptyState>
        </section>
      ) : (
        <ul className="addon-catalog" aria-label={t("addons.tabs.community")}>
          {shown.map((i, n) => (
            <CatalogCard key={i.id} item={i} index={n} m={m} p={pay.data} queued={queued.has(i.id)} act={act} />
          ))}
        </ul>
      )}
    </>
  );
}
