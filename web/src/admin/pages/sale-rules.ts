// Who can buy a traffic package and what a tariff can give as a gift, as the server decides
// it (domain.PackageForTariff, Servers.Groups): the one copy the packages' list, the
// package's form and the tariff's form all ask.
import type { Inbound, Schemas, Tariff } from "../../api/client";

type Package = Schemas["PackageView"];
type Mode = Schemas["PoolSetting"]["mode"];

/** The servers of a tariff as saved (lists) or as its form holds them (sets). */
type Servers = { all: boolean; nodes: Set<number> | number[]; inbounds: Set<number> | number[] };

/** Whether the tariff includes protocol i. */
export function serversHas(s: Servers, i: Inbound): boolean {
  const has = (x: Set<number> | number[], id: number) => (x instanceof Set ? x.has(id) : x.includes(id));
  return s.all || has(s.nodes, i.node_id) || has(s.inbounds, i.id);
}

/** What a tariff gives, as a package fits it: a limited main traffic, each group's mode, and
 * the groups its servers serve (undefined while the protocols load: not looked at then). */
export type TariffQuotas = { trafficLimited: boolean; groups: Record<number, { mode: Mode }>; served?: Set<number> };

/** The server groups the servers serve (domain.Servers.Groups): those with a protocol that is
 * on and among them. A package or a gift of another group is traffic nobody can spend. */
export function servedGroups(s: Servers, inbounds: Inbound[]): Set<number> {
  const out = new Set<number>();
  for (const i of inbounds) if (i.pool_id != null && i.enabled && serversHas(s, i)) out.add(i.pool_id);
  return out;
}

/** The quotas a saved tariff gives; with the protocols, the groups its servers serve too. */
export function quotasOf(tr: Tariff, inbounds?: Inbound[]): TariffQuotas {
  return {
    trafficLimited: tr.traffic_limit != null,
    groups: Object.fromEntries(tr.pools.map((p) => [p.pool_id, { mode: p.mode }])),
    served: inbounds ? servedGroups(tr.servers, inbounds) : undefined,
  };
}

/** The package adds to a quota the tariff gives (domain.PackageForTariff): the main traffic
 * with a limit, or a group with a limit of its own that the tariff's servers serve; whom it
 * is sold to is not looked at. */
export function fitsTariff(p: Pick<Package, "pool_id">, q: TariffQuotas): boolean {
  if (p.pool_id == null) return q.trafficLimited;
  return q.groups[p.pool_id]?.mode === "own" && (q.served?.has(p.pool_id) ?? true);
}

/** Tariff tariffId can buy the package now: it is on sale, fits the tariff (fits,
 * fitsTariff) and is sold to it. */
export function canBuy(p: Pick<Package, "on_sale" | "tariffs">, fits: boolean, tariffId: number): boolean {
  return p.on_sale && fits && (p.tariffs.length === 0 || p.tariffs.includes(tariffId));
}
