import { useMutation, useQuery, useQueryClient } from "@tanstack/react-query";
import { Ban, Check } from "lucide-react";
import { useState } from "react";
import { api, errorText, unwrap, type User } from "../../../api/client";
import { qk, useFilters } from "../../../api/hooks";
import { useToast } from "../../../components/toast";
import { Bar, Button, Segmented, Skeleton } from "../../../components/ui";
import { t } from "../../../i18n";
import { bytes } from "../../../lib/format";
import { Section } from "./section";

const PERIODS = [1, 7, 30] as const;

/**
 * Where the subscription goes: the bytes by site over a few days, summed on the nodes with
 * nothing else kept. Shown while the detail is on (Settings → Routing); a site may be
 * blocked for everyone from here, into the filters.
 */
export function SitesSection({ u }: { u: User }) {
  const [days, setDays] = useState<(typeof PERIODS)[number]>(7);
  const sites = useQuery({
    queryKey: ["user-sites", u.id, days],
    queryFn: ({ signal }) => unwrap(api.GET("/api/v1/users/{id}/sites", { params: { path: { id: u.id }, query: { days } }, signal })),
  });
  const filters = useFilters();
  const qc = useQueryClient();
  const toast = useToast();
  const blocked = new Set(filters.data?.egress.enabled ? filters.data.egress.domains : []);
  const block = useMutation({
    mutationFn: (site: string) => {
      const f = filters.data;
      if (!f) throw new Error("filters");
      const domains = [...new Set([...f.egress.domains, site])];
      return unwrap(api.PATCH("/api/v1/filters", { body: { egress: { ...f.egress, enabled: true, domains } } }));
    },
    onSuccess: (_, site) => {
      void qc.invalidateQueries({ queryKey: qk.filters });
      toast.ok(t("userDrawer.sites.blocked", { site }));
    },
    onError: (e) => toast.error(errorText(e)),
  });
  const v = sites.data;
  if (v && !v.enabled && v.items.length === 0) return null;
  const top = v?.items[0] ? v.items[0].up + v.items[0].down : 0;
  return (
    <Section title={t("userDrawer.sites.title")} aside={v?.enabled === false ? t("userDrawer.sites.off") : undefined}>
      <div className="mb-3">
        <Segmented label={t("userDrawer.sites.period")} value={String(days)} onChange={(x) => setDays(Number(x) as (typeof PERIODS)[number])} options={PERIODS.map((d) => ({ value: String(d), label: t("userDrawer.sites.days", { n: d }) }))} />
      </div>
      {sites.isPending ? (
        <div className="flex flex-col gap-2">
          {[0, 1, 2].map((i) => (
            <Skeleton key={i} style={{ height: 36, borderRadius: 10 }} />
          ))}
        </div>
      ) : sites.isError ? (
        <div className="banner err" role="alert">
          {errorText(sites.error)}
        </div>
      ) : !v || v.items.length === 0 ? (
        <p className="text-xs text-[var(--ink-500)]">{t("userDrawer.sites.none")}</p>
      ) : (
        <ul className="flex flex-col gap-2">
          {v.items.map((s) => {
            const total = s.up + s.down;
            const site = s.site === "*" ? t("userDrawer.sites.other") : s.site;
            const isDomain = s.site !== "*" && /[a-z]/i.test(s.site) && !s.site.includes(":");
            const done = blocked.has(s.site);
            return (
              <li key={s.site} className="flex items-center gap-3">
                <div className="min-w-0 flex-1">
                  <div className="flex items-baseline justify-between gap-2 text-[13px]">
                    <span className="mono truncate">{site}</span>
                    <span className="num shrink-0 text-[var(--ink-600)]">{bytes(total)}</span>
                  </div>
                  <Bar pct={top ? (total / top) * 100 : 0} className="mt-1" />
                </div>
                {isDomain ? (
                  done ? (
                    <span className="flex w-8 shrink-0 justify-center text-[var(--ink-500)]" title={t("userDrawer.sites.isBlocked")}>
                      <Check size={16} aria-label={t("userDrawer.sites.isBlocked")} />
                    </span>
                  ) : (
                    <Button size="sm" variant="ghost" aria-label={t("userDrawer.sites.block", { site })} title={t("userDrawer.sites.block", { site })} disabled={block.isPending || !filters.data} onClick={() => block.mutate(s.site)}>
                      <Ban size={16} aria-hidden />
                    </Button>
                  )
                ) : (
                  <span className="w-8 shrink-0" aria-hidden />
                )}
              </li>
            );
          })}
        </ul>
      )}
      <p className="mt-3 text-xs text-[var(--ink-500)]">{t("userDrawer.sites.hint", { n: v?.keep_days ?? 7 })}</p>
    </Section>
  );
}
