// Load balancing between nodes: how full each node of a balance group is, and a way to
// spread the group's clients out again.
import { useMutation, useQuery, useQueryClient } from "@tanstack/react-query";
import { Scale, Shuffle } from "lucide-react";
import { useState } from "react";
import { api, errorText, unwrap, type Schemas } from "../../api/client";
import { Confirm } from "../../components/overlay";
import { useToast } from "../../components/toast";
import { Bar, Button, Field, Pill } from "../../components/ui";
import { t } from "../../i18n";
import { nodeLabel } from "../../lib/node-label";

type Node = Schemas["NodeInfo"];
export const balanceKey = ["balance"] as const;

export function useBalance() {
  return useQuery({ queryKey: balanceKey, queryFn: () => unwrap(api.GET("/api/v1/balance")) });
}

/** The balance groups of the panel with their nodes' load; nothing without groups. */
export function BalanceGroups({ nodes }: { nodes: Node[] }) {
  const qc = useQueryClient();
  const toast = useToast();
  const balance = useBalance();
  const [asking, setAsking] = useState<string | null>(null);
  const redistribute = useMutation({
    mutationFn: (group: string) => unwrap(api.POST("/api/v1/balance/{group}/redistribute", { params: { path: { group } } })),
    onSuccess: (r) => {
      toast.ok(t("balance.redistributed", { n: r.moved }));
      setAsking(null);
      void qc.invalidateQueries({ queryKey: balanceKey });
    },
    onError: (e) => toast.error(errorText(e)),
  });
  const groups = balance.data ?? [];
  if (groups.length === 0) return null;
  const byId = new Map(nodes.map((n) => [n.id, n]));
  return (
    <section className="card glass reveal mb-4" aria-label={t("balance.title")}>
      <div className="card-head">
        <div>
          <h2 className="card-title flex items-center gap-2">
            <Scale size={18} aria-hidden /> {t("balance.title")}
          </h2>
          <div className="card-sub">{t("balance.sub")}</div>
        </div>
      </div>
      <div className="flex flex-col gap-4">
        {groups.map((g) => {
          const full = g.nodes.every((n) => n.full);
          return (
            <div key={g.group}>
              <div className="mb-2 flex flex-wrap items-center gap-2">
                <span className="font-semibold">{g.group}</span>
                {full ? <Pill tone="warn">{t("balance.groupFull")}</Pill> : null}
                <Button size="sm" variant="ghost" className="ml-auto" onClick={() => setAsking(g.group)}>
                  <Shuffle size={16} aria-hidden /> {t("balance.redistribute")}
                </Button>
              </div>
              <div className="flex flex-col gap-2">
                {g.nodes.map((n) => {
                  const node = byId.get(n.node_id);
                  const pct = n.capacity ? Math.min(100, Math.round((n.clients / n.capacity) * 100)) : 0;
                  return (
                    <div key={n.node_id} className="grid grid-cols-[minmax(0,1fr)_auto] items-center gap-x-3 gap-y-1">
                      <span className="truncate text-[13px]">{node ? nodeLabel(node) : `#${n.node_id}`}</span>
                      <span className="text-xs text-[var(--ink-500)]">
                        {n.capacity != null ? t("balance.load", { n: n.clients, cap: n.capacity }) : t("balance.loadUnlimited", { n: n.clients })}
                      </span>
                      {n.capacity != null ? <Bar pct={pct} className="col-span-2" label={t("balance.load", { n: n.clients, cap: n.capacity })} /> : null}
                    </div>
                  );
                })}
              </div>
            </div>
          );
        })}
      </div>
      <p className="mt-3 text-xs text-[var(--ink-500)]">{t("balance.note")}</p>
      <Confirm
        open={!!asking}
        onOpenChange={(v) => !v && setAsking(null)}
        title={t("balance.redistributeTitle", { group: asking ?? "" })}
        text={t("balance.redistributeText")}
        confirm={t("balance.redistribute")}
        loading={redistribute.isPending}
        onConfirm={() => asking && redistribute.mutate(asking)}
      />
    </section>
  );
}

/** The node's part in balancing, inside the node's drawer. */
export function BalanceFields({
  group,
  capacity,
  onGroup,
  onCapacity,
  error,
  groups,
}: {
  group: string;
  capacity: string;
  onGroup: (v: string) => void;
  onCapacity: (v: string) => void;
  error?: string;
  groups: string[];
}) {
  return (
    <div className="dr-sec">
      <div className="switch-row-title mb-1">{t("balance.nodeTitle")}</div>
      <div className="switch-row-sub mb-3">{t("balance.nodeSub")}</div>
      <div className="grid gap-x-3 sm:grid-cols-[1fr_160px]">
        <Field label={t("balance.group")} htmlFor="e-balance-group" hint={t("balance.groupHint")} error={error}>
          <input id="e-balance-group" className="input" list="balance-groups" value={group} onChange={(e) => onGroup(e.target.value)} maxLength={40} placeholder={t("balance.groupPlaceholder")} autoComplete="off" />
          <datalist id="balance-groups">
            {groups.map((g) => (
              <option key={g} value={g} />
            ))}
          </datalist>
        </Field>
        <Field label={t("balance.capacity")} htmlFor="e-capacity" hint={t("balance.capacityHint")}>
          <input id="e-capacity" className="input" inputMode="numeric" value={capacity} onChange={(e) => onCapacity(e.target.value.replace(/[^0-9]/g, ""))} disabled={!group.trim()} placeholder="∞" />
        </Field>
      </div>
    </div>
  );
}
