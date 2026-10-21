import { describe, expect, it } from "vitest";
import type { Inbound, Schemas, Tariff } from "../../api/client";
import { canBuy, fitsTariff, quotasOf, servedGroups, serversHas } from "./sale-rules";

type Package = Schemas["PackageView"];

// The fields the rules read; the rest of a view does not matter to them.
const inbound = (id: number, node: number, pool: number | undefined, enabled = true) => ({ id, node_id: node, pool_id: pool, enabled }) as Inbound;
const tariff = (p: Partial<Tariff>) => ({ id: 1, traffic_limit: 100, pools: [], servers: { all: true, nodes: [], inbounds: [] }, ...p }) as Tariff;
const pkg = (p: Partial<Package>) => ({ pool_id: undefined, on_sale: true, tariffs: [], ...p }) as Package;

describe("serversHas", () => {
  it("takes every protocol with all, a node whole, a protocol alone", () => {
    const s = { all: false, nodes: [2], inbounds: [5] };
    expect(serversHas({ all: true, nodes: [], inbounds: [] }, inbound(1, 1, undefined))).toBe(true);
    expect(serversHas(s, inbound(1, 2, undefined))).toBe(true);
    expect(serversHas(s, inbound(5, 3, undefined))).toBe(true);
    expect(serversHas(s, inbound(6, 3, undefined))).toBe(false);
  });
  it("reads the form's sets as the saved lists", () => {
    expect(serversHas({ all: false, nodes: new Set([2]), inbounds: new Set<number>() }, inbound(1, 2, undefined))).toBe(true);
  });
});

describe("servedGroups", () => {
  it("are the groups with a protocol that is on and among the servers", () => {
    const ins = [inbound(1, 1, 10), inbound(2, 2, 20), inbound(3, 1, 30, false), inbound(4, 1, undefined)];
    expect([...servedGroups({ all: false, nodes: [1], inbounds: [] }, ins)]).toEqual([10]);
    expect([...servedGroups({ all: true, nodes: [], inbounds: [] }, ins)].sort()).toEqual([10, 20]);
  });
});

describe("fitsTariff", () => {
  const ins = [inbound(1, 1, 10), inbound(2, 2, 20)];
  it("puts a main traffic package on a limited main traffic only", () => {
    expect(fitsTariff(pkg({}), quotasOf(tariff({ traffic_limit: 100 }), ins))).toBe(true);
    expect(fitsTariff(pkg({}), quotasOf(tariff({ traffic_limit: undefined }), ins))).toBe(false);
  });
  it("puts a group's package on a group with its own limit that the servers serve", () => {
    const pools = [{ pool_id: 10, mode: "own" }, { pool_id: 20, mode: "own" }] as Tariff["pools"];
    const servers = { all: false, nodes: [1], inbounds: [] };
    const q = quotasOf(tariff({ pools, servers }), ins);
    expect(fitsTariff(pkg({ pool_id: 10 }), q)).toBe(true);
    // Group 20 has its limit, but none of its protocols is on the tariff's servers.
    expect(fitsTariff(pkg({ pool_id: 20 }), q)).toBe(false);
  });
  it("does not put a package on a group of the main traffic, unlimited or closed", () => {
    for (const mode of ["main", "unlimited", "closed"] as const) {
      const q = quotasOf(tariff({ pools: [{ pool_id: 10, mode }] as Tariff["pools"] }), ins);
      expect(fitsTariff(pkg({ pool_id: 10 }), q)).toBe(false);
    }
  });
  it("leaves the servers out while the protocols load", () => {
    const q = quotasOf(tariff({ pools: [{ pool_id: 20, mode: "own" }] as Tariff["pools"], servers: { all: false, nodes: [1], inbounds: [] } }));
    expect(fitsTariff(pkg({ pool_id: 20 }), q)).toBe(true);
  });
});

describe("canBuy", () => {
  it("needs the package on sale, fitting, and sold to the tariff", () => {
    expect(canBuy(pkg({}), true, 1)).toBe(true);
    expect(canBuy(pkg({ on_sale: false }), true, 1)).toBe(false);
    expect(canBuy(pkg({}), false, 1)).toBe(false);
    expect(canBuy(pkg({ tariffs: [2] }), true, 1)).toBe(false);
    expect(canBuy(pkg({ tariffs: [1, 2] }), true, 1)).toBe(true);
  });
});
