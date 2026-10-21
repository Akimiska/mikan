// The servers as a tree, a node and its protocols under it: what a tariff includes, what a
// server group holds and what a client may use are picked the same way.
import type { ReactNode } from "react";
import type { Inbound, Schemas, Tariff } from "../../api/client";
import { useNodes } from "../../api/hooks";
import { t } from "../../i18n";
import { nodeLabel } from "../../lib/node-label";

/** A node's id and its protocols, nodes in the order the protocols come. */
export type NodeInbounds = [number, Inbound[]];

export function byNode(inbounds: Inbound[] | undefined): NodeInbounds[] {
  const out = new Map<number, Inbound[]>();
  for (const i of inbounds ?? []) out.set(i.node_id, [...(out.get(i.node_id) ?? []), i]);
  return [...out.entries()];
}

/** How a node's own checkbox stands. */
export type NodeState = "on" | "off" | "some";

export function ServersTree({
  nodes,
  nodeState,
  onNode,
  checked,
  onInbound,
  aside,
  nodeHint = t("pools.wholeNode"),
  nodeDisabled,
  disabled,
}: {
  nodes: NodeInbounds[];
  nodeState: (id: number, list: Inbound[]) => NodeState;
  onNode: (id: number, list: Inbound[], on: boolean) => void;
  checked: (i: Inbound) => boolean;
  onInbound: (i: Inbound, list: Inbound[], on: boolean) => void;
  /** A note at the end of a protocol's row. */
  aside?: (i: Inbound) => ReactNode;
  nodeHint?: string;
  /** A node whose own checkbox has nothing to take. */
  nodeDisabled?: (id: number, list: Inbound[]) => boolean;
  disabled?: boolean;
}) {
  const all = useNodes();
  return (
    <div className="flex flex-col gap-2">
      {nodes.map(([id, list]) => {
        const node = all.data?.find((n) => n.id === id);
        const label = node ? nodeLabel(node) : String(id);
        const state = nodeState(id, list);
        const off = nodeDisabled?.(id, list) ?? false;
        return (
          <fieldset key={id} className="panel-soft px-3 py-2" disabled={disabled}>
            <legend className="sr-only">{label}</legend>
            <label className={`flex items-center gap-2 py-1.5 text-[13px] font-semibold ${off ? "text-[var(--ink-400)]" : ""}`}>
              <input
                type="checkbox"
                className="check"
                disabled={off}
                checked={state === "on"}
                ref={(el) => {
                  if (el) el.indeterminate = state === "some";
                }}
                onChange={(e) => onNode(id, list, e.target.checked)}
              />
              <span className="min-w-0 flex-1 truncate">{label}</span>
              <span className="text-xs font-normal text-[var(--ink-500)]">{nodeHint}</span>
            </label>
            <ul className="border-t border-[var(--hairline)] pt-1">
              {list.map((i) => (
                <li key={i.id}>
                  <label className="flex items-center gap-2 py-1.5 pl-6 text-[13px]">
                    <input type="checkbox" className="check" checked={checked(i)} onChange={(e) => onInbound(i, list, e.target.checked)} />
                    <span className="min-w-0 flex-1 truncate">
                      {i.sub_name || i.title}
                      <span className="text-xs text-[var(--ink-500)]">
                        {" "}
                        · {i.port}/{i.network}
                        {i.enabled ? "" : ` · ${t("servers.off")}`}
                      </span>
                    </span>
                    {aside?.(i)}
                  </label>
                </li>
              ))}
            </ul>
          </fieldset>
        );
      })}
    </div>
  );
}

/** What a tariff includes as the form holds it: every server, or the chosen nodes (whole,
 * with the protocols added to them later) and protocols. */
export type ServersDraft = { all: boolean; nodes: Set<number>; inbounds: Set<number> };

export function serversDraftOf(s: Tariff["servers"] | undefined): ServersDraft {
  return { all: s?.all ?? true, nodes: new Set(s?.nodes ?? []), inbounds: new Set(s?.inbounds ?? []) };
}

export function serversBody(d: ServersDraft): Schemas["ServersBody"] {
  return d.all ? { all: true } : { all: false, nodes: [...d.nodes], inbounds: [...d.inbounds] };
}

/** Whether the tariff includes protocol i. */
export function serversHas(s: { all: boolean; nodes: Set<number> | number[]; inbounds: Set<number> | number[] }, i: Inbound): boolean {
  const has = (x: Set<number> | number[], id: number) => (x instanceof Set ? x.has(id) : x.includes(id));
  return s.all || has(s.nodes, i.node_id) || has(s.inbounds, i.id);
}

/** The tariff's chosen servers in a tree: a node ticked is whole, a protocol ticked alone is
 * just that one; unticking one protocol of a whole node keeps the rest of it ticked. */
export function TariffServersTree({ nodes, value, onChange }: { nodes: NodeInbounds[]; value: ServersDraft; onChange: (d: ServersDraft) => void }) {
  const set = (f: (d: ServersDraft) => void) => {
    const next = { all: false, nodes: new Set(value.nodes), inbounds: new Set(value.inbounds) };
    f(next);
    onChange(next);
  };
  return (
    <ServersTree
      nodes={nodes}
      nodeState={(id, list) => (value.nodes.has(id) ? "on" : list.some((i) => value.inbounds.has(i.id)) ? "some" : "off")}
      onNode={(id, list, on) =>
        set((d) => {
          for (const i of list) d.inbounds.delete(i.id);
          if (on) d.nodes.add(id);
          else d.nodes.delete(id);
        })
      }
      checked={(i) => value.nodes.has(i.node_id) || value.inbounds.has(i.id)}
      onInbound={(i, list, on) =>
        set((d) => {
          if (on) {
            d.inbounds.add(i.id);
            return;
          }
          if (d.nodes.delete(i.node_id)) for (const x of list) if (x.id !== i.id) d.inbounds.add(x.id);
          d.inbounds.delete(i.id);
        })
      }
      nodeHint={t("servers.wholeNode")}
    />
  );
}
