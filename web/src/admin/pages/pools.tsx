// Server groups (the traffic pools of GitHub issue #6): a set of servers and protocols that
// every tariff, and every subscription, holds in one mode: on the main traffic, with a limit
// of its own, without a limit, or not included. The protocols in no group are the main
// servers. A group is made here, with its protocols and its mode in each tariff; a tariff
// sets its groups in its own form too, a client's card per client.
import { useMutation, useQueryClient } from "@tanstack/react-query";
import { Pencil, Plus, Server, Trash2 } from "lucide-react";
import { useEffect, useMemo, useState, type FormEvent } from "react";
import { api, ApiError, errorText, unwrap, type Inbound, type Schemas, type Tariff } from "../../api/client";
import { invalidatePoolRelated, useInbounds, useNodes, usePools, useTariffs } from "../../api/hooks";
import { Confirm, Drawer, DrawerFooter } from "../../components/overlay";
import { QueryBoundary } from "../../components/query";
import { useToast } from "../../components/toast";
import { Button, EmptyState, Field, Pill, Skeleton } from "../../components/ui";
import { t } from "../../i18n";
import { bytes, GiB } from "../../lib/format";
import { nodeLabel } from "../../lib/node-label";
import { parseDecimal } from "../../lib/validate";
import { byNode, ServersTree } from "./servers-tree";

type Pool = Schemas["PoolView"];
export type GroupMode = Schemas["PoolSetting"]["mode"];
export const GROUP_MODES: GroupMode[] = ["main", "own", "unlimited", "closed"];

/** How a form holds a group's setting: its mode, and its limit in GB with the mode own. */
export type GroupDraft = { mode: GroupMode; gb: string };

/** The draft of a setting as the API has it; a group the tariff never set is on the main traffic. */
export function draftOf(s: { mode: GroupMode; traffic_limit?: number | null } | undefined): GroupDraft {
  if (!s) return { mode: "main", gb: "" };
  return { mode: s.mode, gb: s.traffic_limit != null ? String(+(s.traffic_limit / GiB).toFixed(2)) : "" };
}

/** A group's limit in bytes from its draft; NaN when the GB are not a positive number. */
function limitOf(d: GroupDraft): number {
  const gb = parseDecimal(d.gb.trim());
  return gb > 0 ? Math.round(gb * GiB) : NaN;
}

/** The settings a form sends for the groups, or the error under the field when a limit is bad. */
export function settingsOf(groups: Pool[], drafts: Record<number, GroupDraft>): { settings: Schemas["PoolSetting"][]; error?: string } {
  const settings: Schemas["PoolSetting"][] = [];
  for (const g of groups) {
    const d = drafts[g.id] ?? draftOf(undefined);
    if (d.mode !== "own") {
      settings.push({ pool_id: g.id, mode: d.mode });
      continue;
    }
    const limit = limitOf(d);
    if (!Number.isFinite(limit)) return { settings, error: t("pools.errLimit") };
    settings.push({ pool_id: g.id, mode: "own", traffic_limit: limit });
  }
  return { settings };
}

/** A group's mode in a few words: "main traffic", "100 GB", "unlimited", "not included". */
export function modeShort(mode: GroupMode, limit?: number | null): string {
  if (mode === "own") return limit != null ? bytes(limit) : t("pools.mode.own");
  return t(`pools.short.${mode}`);
}

/** The tone of a mode's chip: not included is the one that takes something away. */
function modeTone(mode: GroupMode): "ok" | "warn" | "bad" | "off" {
  return mode === "closed" ? "off" : mode === "own" ? "warn" : "ok";
}

/** The protocols of a group in a line: "NL: VLESS, Hysteria2 · DE: Trojan". */
function serversText(ids: number[], inbounds: Inbound[] | undefined, nodes: { id: number; name: string; local: boolean }[] | undefined): string {
  const byNode = new Map<number, string[]>();
  for (const id of ids) {
    const i = inbounds?.find((x) => x.id === id);
    if (!i) continue;
    byNode.set(i.node_id, [...(byNode.get(i.node_id) ?? []), i.sub_name || i.title]);
  }
  const multi = (nodes?.length ?? 0) > 1;
  return [...byNode.entries()]
    .map(([node, names]) => {
      const n = nodes?.find((x) => x.id === node);
      return multi && n ? `${nodeLabel(n)}: ${names.join(", ")}` : names.join(", ");
    })
    .join(" · ");
}

/**
 * How a tariff or a subscription has each group: a row per group with its mode and, with the
 * mode own, its limit. `base` is the tariff's setting a client's card compares with.
 */
export function GroupModesField({
  groups,
  value,
  onChange,
  base,
  disabled,
}: {
  groups: Pool[];
  value: Record<number, GroupDraft>;
  onChange: (v: Record<number, GroupDraft>) => void;
  base?: Record<number, GroupDraft>;
  disabled?: boolean;
}) {
  const inbounds = useInbounds();
  const nodes = useNodes();
  if (groups.length === 0) return null;
  const set = (id: number, d: GroupDraft) => onChange({ ...value, [id]: d });
  return (
    <ul className="row-list rounded-2xl border border-[var(--hairline)] px-3">
      <li className="py-2.5">
        <div className="text-[13px] font-medium">{t("pools.mainServers")}</div>
        <div className="text-xs text-[var(--ink-500)]">{t("pools.mainServersSub")}</div>
      </li>
      {groups.map((g) => {
        const d = value[g.id] ?? draftOf(undefined);
        const servers = serversText(g.inbound_ids, inbounds.data, nodes.data);
        const differs = base && (base[g.id]?.mode ?? "main") !== d.mode ? true : base && d.mode === "own" && (base[g.id]?.gb ?? "") !== d.gb;
        const id = `grp-${g.id}`;
        return (
          <li key={g.id} className="py-2.5">
            <div className="grid grid-cols-[minmax(0,1fr)_auto] items-center gap-x-3 gap-y-2 max-[520px]:grid-cols-1">
              <div className="min-w-0">
                <div className="flex flex-wrap items-center gap-2">
                  <label htmlFor={id} className="truncate text-[13px] font-medium">
                    {g.name}
                  </label>
                  {base ? <Pill tone={differs ? "warn" : "off"}>{differs ? t("pools.changed") : t("pools.asTariff")}</Pill> : null}
                </div>
                <div className="truncate text-xs text-[var(--ink-500)]">{servers || t("pools.empty")}</div>
              </div>
              <div className="flex items-center gap-2">
                <select
                  id={id}
                  className="input h-9 min-w-[168px] py-0 text-[13px]"
                  value={d.mode}
                  disabled={disabled}
                  aria-label={t("pools.modeOf", { name: g.name })}
                  onChange={(e) => {
                    const mode = e.target.value as GroupMode;
                    // A limit asked for the first time starts from the tariff's, else from 50 GB.
                    set(g.id, { mode, gb: mode === "own" && !d.gb ? (base?.[g.id]?.gb || "50") : d.gb });
                  }}
                >
                  {GROUP_MODES.map((m) => (
                    <option key={m} value={m}>
                      {t(`pools.mode.${m}`)}
                    </option>
                  ))}
                </select>
                {d.mode === "own" ? (
                  <label className="input-unit h-9 w-[112px]">
                    <input
                      className="input num"
                      inputMode="decimal"
                      value={d.gb}
                      disabled={disabled}
                      onChange={(e) => set(g.id, { mode: "own", gb: e.target.value })}
                      aria-label={t("pools.limitOf", { name: g.name })}
                      aria-invalid={!Number.isFinite(limitOf(d))}
                    />
                    <span aria-hidden>{t("units.gb")}</span>
                  </label>
                ) : null}
              </div>
            </div>
            <p className="mt-1 text-xs text-[var(--ink-500)]">{t(`pools.modeHint.${d.mode}`)}</p>
          </li>
        );
      })}
    </ul>
  );
}

/** The groups tab: every group with its servers, an overview of the tariffs, and the editor. */
export function PoolsCard({ creating, onCreateClose }: { creating: boolean; onCreateClose: () => void }) {
  const pools = usePools();
  const tariffs = useTariffs();
  const inbounds = useInbounds();
  const nodes = useNodes();
  const qc = useQueryClient();
  const toast = useToast();
  const [edit, setEdit] = useState<Pool | "new" | null>(null);
  const [removing, setRemoving] = useState<Pool | null>(null);
  const remove = useMutation({
    mutationFn: (id: number) => unwrap(api.DELETE("/api/v1/pools/{id}", { params: { path: { id } } })),
    onSuccess: () => {
      setRemoving(null);
      invalidatePoolRelated(qc);
      toast.ok(t("pools.deleted"));
    },
    onError: (e) => toast.error(errorText(e)),
  });
  const live = (tariffs.data ?? []).filter(Boolean);

  return (
    <>
      <section className="card glass reveal">
        <div className="card-head">
          <div>
            <h2 className="card-title">{t("pools.title")}</h2>
            <div className="card-sub">{t("pools.sub")}</div>
          </div>
        </div>
        <QueryBoundary
          query={pools}
          pending={
            <div className="flex flex-col gap-2">
              {[0, 1].map((i) => (
                <Skeleton key={i} style={{ height: 64 }} />
              ))}
            </div>
          }
        >
          {(list) =>
            list.length === 0 ? (
              <EmptyState icon={Server} title={t("pools.emptyTitle")} text={t("pools.none")}>
                <Button variant="primary" onClick={() => setEdit("new")}>
                  <Plus size={16} aria-hidden /> {t("pools.add")}
                </Button>
              </EmptyState>
            ) : (
              <ul className="row-list">
                {list.map((p) => {
                  const servers = serversText(p.inbound_ids, inbounds.data, nodes.data);
                  return (
                    <li key={p.id} className="grid grid-cols-[minmax(0,1fr)_auto] items-start gap-3 py-3">
                      <div className="min-w-0">
                        <button type="button" className="text-left text-[13px] font-semibold hover:underline hover:underline-offset-4" onClick={() => setEdit(p)}>
                          {p.name}
                        </button>
                        <dl className="mt-1 grid gap-1 text-xs">
                          <div className="flex flex-wrap gap-x-2">
                            <dt className="text-[var(--ink-500)]">{t("pools.protocols")}</dt>
                            <dd className="min-w-0 text-[var(--ink-700)]">
                              {servers || (
                                <button type="button" className="link-btn" onClick={() => setEdit(p)}>
                                  {t("pools.empty")}
                                </button>
                              )}
                            </dd>
                          </div>
                          {live.length ? (
                            <div className="flex flex-wrap items-center gap-x-2 gap-y-1">
                              <dt className="text-[var(--ink-500)]">{t("pools.limits")}</dt>
                              <dd className="flex min-w-0 flex-wrap gap-1.5">
                                {live.map((tr) => {
                                  const s = tr.pools.find((x) => x.pool_id === p.id);
                                  const mode = s?.mode ?? "main";
                                  return (
                                    <span key={tr.id} className="inline-flex items-center gap-1">
                                      <span className="text-[var(--ink-700)]">{tr.name}</span>
                                      <Pill tone={modeTone(mode)}>{modeShort(mode, s?.traffic_limit)}</Pill>
                                    </span>
                                  );
                                })}
                              </dd>
                            </div>
                          ) : null}
                        </dl>
                      </div>
                      <div className="flex gap-1">
                        <button type="button" className="icon-btn" aria-label={t("pools.rename", { name: p.name })} onClick={() => setEdit(p)}>
                          <Pencil size={16} />
                        </button>
                        <button type="button" className="icon-btn" aria-label={t("pools.delete", { name: p.name })} onClick={() => setRemoving(p)}>
                          <Trash2 size={16} />
                        </button>
                      </div>
                    </li>
                  );
                })}
              </ul>
            )
          }
        </QueryBoundary>
        <p className="mt-3 text-xs text-[var(--ink-500)]">{t("pools.hint")}</p>
      </section>
      {pools.data?.length && live.length ? <GroupsOverview pools={pools.data} tariffs={live} onOpen={setEdit} /> : null}
      <GroupDrawer
        group={creating ? "new" : edit}
        onClose={() => {
          setEdit(null);
          onCreateClose();
        }}
      />
      <Confirm
        open={!!removing}
        onOpenChange={(v) => !v && setRemoving(null)}
        title={t("pools.deleteTitle", { name: removing?.name ?? "" })}
        text={t("pools.deleteText")}
        confirm={t("common.delete")}
        danger
        loading={remove.isPending}
        onConfirm={() => removing && remove.mutate(removing.id)}
      />
    </>
  );
}

/** Groups by tariffs: one row per group, one column per tariff, the mode in each cell. */
function GroupsOverview({ pools, tariffs, onOpen }: { pools: Pool[]; tariffs: Tariff[]; onOpen: (p: Pool) => void }) {
  return (
    <section className="card glass reveal mt-4">
      <div className="card-head">
        <div>
          <h2 className="card-title">{t("pools.overview")}</h2>
          <div className="card-sub">{t("pools.overviewSub")}</div>
        </div>
      </div>
      <div className="-mx-1 overflow-x-auto px-1">
        <table className="w-full min-w-max text-[13px]">
          <thead>
            <tr className="text-left text-xs text-[var(--ink-500)]">
              <th className="sticky left-0 bg-[var(--surface-soft)] py-2 pr-4 font-medium" scope="col">
                {t("pools.title")}
              </th>
              {tariffs.map((tr) => (
                <th key={tr.id} className="px-3 py-2 font-medium whitespace-nowrap" scope="col">
                  {tr.name}
                </th>
              ))}
            </tr>
          </thead>
          <tbody>
            <tr className="border-t border-[var(--hairline)]">
              <th className="sticky left-0 bg-[var(--surface-soft)] py-2 pr-4 text-left font-medium whitespace-nowrap" scope="row">
                {t("pools.mainServers")}
              </th>
              {tariffs.map((tr) => (
                <td key={tr.id} className="px-3 py-2 whitespace-nowrap">
                  <Pill tone="ok">{tr.traffic_limit != null ? bytes(tr.traffic_limit) : t("users.unlimited")}</Pill>
                </td>
              ))}
            </tr>
            {pools.map((p) => (
              <tr key={p.id} className="border-t border-[var(--hairline)]">
                <th className="sticky left-0 bg-[var(--surface-soft)] py-2 pr-4 text-left font-medium whitespace-nowrap" scope="row">
                  <button type="button" className="link-btn" onClick={() => onOpen(p)}>
                    {p.name}
                  </button>
                </th>
                {tariffs.map((tr) => {
                  const s = tr.pools.find((x) => x.pool_id === p.id);
                  const mode = s?.mode ?? "main";
                  return (
                    <td key={tr.id} className="px-3 py-2 whitespace-nowrap">
                      <Pill tone={modeTone(mode)}>{modeShort(mode, s?.traffic_limit)}</Pill>
                    </td>
                  );
                })}
              </tr>
            ))}
          </tbody>
        </table>
      </div>
    </section>
  );
}

/** A new group, or one to change: its name, its protocols by server, and its mode in each tariff. */
function GroupDrawer({ group, onClose }: { group: Pool | "new" | null; onClose: () => void }) {
  const qc = useQueryClient();
  const toast = useToast();
  const inbounds = useInbounds();
  const tariffs = useTariffs();
  const pools = usePools();
  const isNew = group === "new";
  const [name, setName] = useState("");
  const [picked, setPicked] = useState<Set<number>>(new Set());
  const [modes, setModes] = useState<Record<number, GroupDraft>>({});
  const [errors, setErrors] = useState<Record<string, string>>({});

  useEffect(() => {
    if (!group) return;
    const g = group === "new" ? null : group;
    setName(g?.name ?? "");
    setPicked(new Set(g?.inbound_ids ?? []));
    setModes(Object.fromEntries((tariffs.data ?? []).map((tr) => [tr.id, draftOf(g ? tr.pools.find((x) => x.pool_id === g.id) : undefined)])));
    setErrors({});
    // The tariffs are read when the drawer opens; a refetch meanwhile must not reset the form.
  }, [group]);

  const tree = useMemo(() => byNode(inbounds.data), [inbounds.data]);
  const groupName = (id: number | null | undefined) => pools.data?.find((p) => p.id === id)?.name;
  /** The name of the other group protocol i is in now. */
  const otherGroup = (i: Inbound) => (i.pool_id != null && (group === "new" || i.pool_id !== group?.id) ? groupName(i.pool_id) : undefined);
  /** Whether "Whole server" takes protocol i: no other group has it. */
  const takes = (i: Inbound) => !otherGroup(i);

  const save = useMutation({
    mutationFn: async () => {
      const trimmed = name.trim();
      const g =
        group === "new" || !group
          ? await unwrap(api.POST("/api/v1/pools", { body: { name: trimmed } }))
          : trimmed !== group.name
            ? await unwrap(api.PATCH("/api/v1/pools/{id}", { params: { path: { id: group.id } }, body: { name: trimmed } }))
            : group;
      await unwrap(api.PUT("/api/v1/pools/{id}/inbounds", { params: { path: { id: g.id } }, body: { inbound_ids: [...picked] } }));
      const list: Schemas["GroupInTariff"][] = (tariffs.data ?? []).map((tr) => {
        const d = modes[tr.id] ?? draftOf(undefined);
        return d.mode === "own" ? { tariff_id: tr.id, mode: "own", traffic_limit: limitOf(d) } : { tariff_id: tr.id, mode: d.mode };
      });
      if (list.length) await unwrap(api.PUT("/api/v1/pools/{id}/tariffs", { params: { path: { id: g.id } }, body: { tariffs: list } }));
      return g;
    },
    onSuccess: () => {
      invalidatePoolRelated(qc);
      toast.ok(isNew ? t("pools.created") : t("pools.saved"));
      onClose();
    },
    onError: (e) => {
      if (e instanceof ApiError && e.fields.name) setErrors({ name: e.fields.name });
      else toast.error(errorText(e));
      // A new group made before the failure is there now: show it, the form stays open on it.
      invalidatePoolRelated(qc);
    },
  });

  const submit = (e: FormEvent) => {
    e.preventDefault();
    const errs: Record<string, string> = {};
    if (!name.trim()) errs.name = t("errors.api.name_blank");
    if (Object.values(modes).some((d) => d.mode === "own" && !Number.isFinite(limitOf(d)))) errs.tariffs = t("pools.errLimit");
    setErrors(errs);
    if (!Object.keys(errs).length) save.mutate();
  };

  const toggle = (ids: number[], on: boolean) =>
    setPicked((cur) => {
      const next = new Set(cur);
      for (const id of ids) {
        if (on) next.add(id);
        else next.delete(id);
      }
      return next;
    });

  return (
    <Drawer
      open={!!group}
      onOpenChange={(v) => {
        if (!v) {
          save.reset();
          onClose();
        }
      }}
      title={isNew ? t("pools.new") : t("pools.edit")}
      meta={t("pools.drawerMeta")}
      footer={
        <DrawerFooter form="group-form" onCancel={onClose} loading={save.isPending} disabled={!name.trim()}>
          {isNew ? t("pools.add") : t("common.save")}
        </DrawerFooter>
      }
    >
      <form id="group-form" onSubmit={submit} className="pt-5" noValidate>
        <Field label={t("pools.name")} htmlFor="group-name" error={errors.name} hint={t("pools.nameHint")}>
          <input id="group-name" className="input" value={name} onChange={(e) => setName(e.target.value)} maxLength={40} placeholder={t("pools.placeholder")} aria-invalid={!!errors.name} autoFocus />
        </Field>

        <Field label={t("pools.servers")} hint={t("pools.serversHint")}>
          {inbounds.isPending ? (
            <Skeleton style={{ height: 96 }} />
          ) : tree.length === 0 ? (
            <p className="text-xs text-[var(--ink-500)]">{t("pools.noInbounds")}</p>
          ) : (
            <ServersTree
              nodes={tree}
              // "Whole server" takes the protocols no other group has: one of another group
              // comes over only when ticked on its own.
              nodeState={(_, list) => {
                const free = list.filter(takes);
                const on = free.filter((i) => picked.has(i.id)).length;
                return free.length > 0 && on === free.length ? "on" : list.some((i) => picked.has(i.id)) ? "some" : "off";
              }}
              nodeDisabled={(_, list) => !list.some(takes)}
              onNode={(_, list, on) => toggle((on ? list.filter(takes) : list).map((i) => i.id), on)}
              checked={(i) => picked.has(i.id)}
              onInbound={(i, _, on) => toggle([i.id], on)}
              aside={(i) => {
                const other = otherGroup(i);
                if (!other) return null;
                return picked.has(i.id) ? (
                  <span className="text-xs text-[var(--honey-600)]">{t("pools.moveFrom", { name: other })}</span>
                ) : (
                  <span className="text-xs text-[var(--ink-500)]">{t("pools.inOther", { name: other })}</span>
                );
              }}
            />
          )}
        </Field>

        <Field label={t("pools.inTariffs")} hint={t("pools.inTariffsHint")} error={errors.tariffs}>
          {(tariffs.data ?? []).length === 0 ? (
            <p className="text-xs text-[var(--ink-500)]">{t("pools.noTariffs")}</p>
          ) : (
            <ul className="row-list rounded-2xl border border-[var(--hairline)] px-3">
              {(tariffs.data ?? []).map((tr) => {
                const d = modes[tr.id] ?? draftOf(undefined);
                const id = `gt-${tr.id}`;
                return (
                  <li key={tr.id} className="py-2.5">
                    <div className="grid grid-cols-[minmax(0,1fr)_auto] items-center gap-x-3 gap-y-2 max-[520px]:grid-cols-1">
                      <label htmlFor={id} className="min-w-0 truncate text-[13px] font-medium">
                        {tr.name}
                      </label>
                      <div className="flex items-center gap-2">
                        <select
                          id={id}
                          className="input h-9 min-w-[168px] py-0 text-[13px]"
                          value={d.mode}
                          onChange={(e) => {
                            const mode = e.target.value as GroupMode;
                            setModes({ ...modes, [tr.id]: { mode, gb: mode === "own" && !d.gb ? "50" : d.gb } });
                          }}
                        >
                          {GROUP_MODES.map((m) => (
                            <option key={m} value={m}>
                              {t(`pools.mode.${m}`)}
                            </option>
                          ))}
                        </select>
                        {d.mode === "own" ? (
                          <label className="input-unit h-9 w-[112px]">
                            <input
                              className="input num"
                              inputMode="decimal"
                              value={d.gb}
                              onChange={(e) => setModes({ ...modes, [tr.id]: { mode: "own", gb: e.target.value } })}
                              aria-label={t("pools.limitOf", { name: tr.name })}
                              aria-invalid={!Number.isFinite(limitOf(d))}
                            />
                            <span aria-hidden>{t("units.gb")}</span>
                          </label>
                        ) : null}
                      </div>
                    </div>
                    <p className="mt-1 text-xs text-[var(--ink-500)]">{t(`pools.modeHint.${d.mode}`)}</p>
                  </li>
                );
              })}
            </ul>
          )}
        </Field>
      </form>
    </Drawer>
  );
}
