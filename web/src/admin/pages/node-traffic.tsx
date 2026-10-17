// What one node carried: the same chart as the dashboard's, over 24 hours, 7 or 30 days.
// The panel counts it by node from the day it learned to; what was carried before is not
// there to show.
import { useMemo, useState } from "react";
import { useNodeTraffic } from "../../api/hooks";
import { buckets, TrafficChart, type Range } from "../../components/chart";
import { Drawer } from "../../components/overlay";
import { QueryBoundary } from "../../components/query";
import { Segmented, Skeleton } from "../../components/ui";
import { t } from "../../i18n";
import { bytes } from "../../lib/format";

export function NodeTrafficDrawer({ node, onClose }: { node: { id: number; name: string } | null; onClose: () => void }) {
  const [range, setRange] = useState<Range>("24h");
  const traffic = useNodeTraffic(node?.id, range);
  const points = useMemo(() => buckets(traffic.data?.points ?? [], range), [traffic.data, range]);
  const down = points.reduce((a, p) => a + p.down, 0);
  const up = points.reduce((a, p) => a + p.up, 0);
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
        <div className="mt-4" aria-busy={traffic.isPlaceholderData}>
          <QueryBoundary query={traffic} pending={<Skeleton style={{ height: 220, borderRadius: 16 }} />}>
            {() => (
              <>
                <TrafficChart points={points} range={range} />
                <div className="mt-3 flex flex-wrap gap-x-6 gap-y-2 text-[13px] text-[var(--ink-500)]">
                  <span>
                    {t("chart.down")} <b className="num font-semibold text-[var(--ink-900)]">{bytes(down)}</b>
                  </span>
                  <span>
                    {t("chart.up")} <b className="num font-semibold text-[var(--ink-900)]">{bytes(up)}</b>
                  </span>
                </div>
                {down + up === 0 ? <p className="mt-4 text-[13px] text-[var(--ink-500)]">{t("nodeTraffic.empty")}</p> : null}
              </>
            )}
          </QueryBoundary>
        </div>
        <p className="mt-6 text-xs text-[var(--ink-500)]">{t("nodeTraffic.note")}</p>
      </div>
    </Drawer>
  );
}
