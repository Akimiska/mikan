import { useNodes } from "../api/hooks";
import { t } from "../i18n";

/** A node's name for the admin: the panel's own node may have none yet. */
export function nodeLabel(n: { name: string; local: boolean }): string {
  return n.name || (n.local ? t("nodes.localName") : "—");
}

/** The servers of the terminal, the logs and the recordings: the panel's own ("panel"), and
 * each node on a server of its own ("node:<id>"). */
export function useServers(): { id: string; label: string }[] {
  const nodes = useNodes();
  return [{ id: "panel", label: t("diag.serverPanel") }, ...(nodes.data ?? []).filter((n) => !n.local).map((n) => ({ id: `node:${n.id}`, label: nodeLabel(n) }))];
}

/** A server of the terminal, the logs or the recordings ("panel" or "node:<id>") as the person reads it. */
export function serverName(target: string, nodes: { id: number; name: string }[]): string {
  if (target === "panel") return t("diag.serverPanel");
  const id = Number(target.slice("node:".length));
  return nodes.find((n) => n.id === id)?.name || t("diag.serverNode", { id });
}
