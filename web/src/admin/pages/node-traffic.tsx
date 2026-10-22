// What one node carried: the same chart as the dashboard's, over 24 hours, 7 or 30 days.
// The panel counts it by node from the day it learned to; what was carried before is not
// there to show. Apart from its users' traffic, a node that is the exit of other nodes'
// cascades shows what it carried for them: their source node counts those users.
import { useMemo, useState } from "react";
import { useNodeTraffic } from "../../api/hooks";
import { buckets, TrafficChart, type Range } from "../../components/chart";
import { Drawer } from "../../components/overlay";
import { QueryBoundary } from "../../components/query";
import { Segmented, Skeleton } from "../../components/ui";
import { t } from "../../i18n";
import { bytes } from "../../lib/format";

type Kind = "own" | "cascade";

export function NodeTrafficDrawer({ node, onClose }: { node: { id: number; name: string } | null; onClose: () => void }) {
  const [range, setRange] = useState<Range>("24h");
  const [kind, setKind] = useState<Kind>("own");
  const traffic = useNodeTraffic(node?.id, range);
  // Another period keeps the last one's points until it loads: cut into this period's steps
  // they add up to nothing true (the dashboard's chart does the same).
  const stale = traffic.isPlaceholderData;
  const raw = useMemo(() => traffic.data?.points ?? [], [traffic.data]);
  const own = useMemo(() => buckets(raw, range), [raw, range]);
  const cascade = useMemo(() => buckets(raw.map((p) => ({ t: p.t, up: p.cascade_up, down: p.cascade_down })), range), [raw, range]);
  const sum = (ps: { up: number; down: number }[]) => ps.reduce((a, p) => ({ up: a.up + p.up, down: a.down + p.down }), { up: 0, down: 0 });
  const ownSum = sum(own);
  const cascadeSum = sum(cascade);
  const hasCascade = cascadeSum.up + cascadeSum.down > 0;
  const shown = hasCascade && kind === "cascade" ? "cascade" : "own";
  const points = shown === "cascade" ? cascade : own;
  const shownSum = shown === "cascade" ? cascadeSum : ownSum;
  return (
    <Drawer open={!!node} onOpenChange={(v) => !v && onClose()} title={t("nodeTraffic.title")} meta={node?.name}>
      <div className="pt-5">
        <div className="flex flex-wrap items-center justify-between gap-3">
          <div className="flex gap-4 text-[13px] text-[var(--ink-600)]">
            <span className="inline-flex items-center gap-2">
              <i className="h-2 w-2 rounded-full bg-[var(--mikan-500)]" /> {t("chart.down")}
            </span>
            <span className="inline-flex items-center gap-2">
              <i className="h-2 w-2 rounded-full bg-[var(--lagoon-500)]" /> {t("chart.up")}
            </span>
          </div>
          <Segmented
            label={t("dashboard.period")}
            value={range}
            onChange={setRange}
            options={[
              { value: "24h", label: t("dashboard.range24h") },
              { value: "7d", label: t("dashboard.range7d") },
              { value: "30d", label: t("dashboard.range30d") },
            ]}
          />
        </div>
        {hasCascade ? (
          <div className="mt-3">
            <Segmented
              label={t("nodeTraffic.kind")}
              value={shown}
              onChange={setKind}
              options={[
                { value: "own", label: t("nodeTraffic.own") },
                { value: "cascade", label: t("nodeTraffic.cascade") },
              ]}
            />
          </div>
        ) : null}
        <div className="mt-4" aria-busy={stale}>
          <QueryBoundary query={traffic} pending={<Skeleton style={{ height: 220, borderRadius: 16 }} />}>
            {() => (
              <>
                <TrafficChart points={points} range={range} stale={stale} />
                <div className="mt-3 flex min-h-5 flex-wrap items-center gap-x-6 gap-y-2 text-[13px] text-[var(--ink-500)]">
                  {stale ? (
                    <>
                      <Skeleton style={{ width: 112, height: 14 }} />
                      <Skeleton style={{ width: 112, height: 14 }} />
                    </>
                  ) : (
                    <>
                      <span>
                        {t("chart.down")} <b className="num font-semibold text-[var(--ink-900)]">{bytes(shownSum.down)}</b>
                      </span>
                      <span>
                        {t("chart.up")} <b className="num font-semibold text-[var(--ink-900)]">{bytes(shownSum.up)}</b>
                      </span>
                    </>
                  )}
                </div>
                {!stale && hasCascade ? (
                  <p className="mt-2 text-[13px] text-[var(--ink-500)]">
                    {shown === "cascade" ? t("nodeTraffic.ownTotal", { bytes: bytes(ownSum.up + ownSum.down) }) : t("nodeTraffic.cascadeTotal", { bytes: bytes(cascadeSum.up + cascadeSum.down) })}
                  </p>
                ) : null}
                {!stale && shownSum.down + shownSum.up === 0 ? <p className="mt-4 text-[13px] text-[var(--ink-500)]">{t("nodeTraffic.empty")}</p> : null}
              </>
            )}
          </QueryBoundary>
        </div>
        {hasCascade ? <p className="mt-6 text-xs text-[var(--ink-500)]">{t("nodeTraffic.cascadeNote")}</p> : null}
        <p className={hasCascade ? "mt-2 text-xs text-[var(--ink-500)]" : "mt-6 text-xs text-[var(--ink-500)]"}>{t("nodeTraffic.note")}</p>
      </div>
    </Drawer>
  );
}
