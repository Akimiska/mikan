import { t } from "../i18n";

/** A node's name for the admin: the panel's own node may have none yet. */
export function nodeLabel(n: { name: string; local: boolean }): string {
  return n.name || (n.local ? t("nodes.localName") : "—");
}

/** A server of the terminal, the logs or the recordings ("panel" or "node:<id>") as the person reads it. */
export function serverName(target: string, nodes: { id: number; name: string }[]): string {
  if (target === "panel") return t("diag.serverPanel");
  const id = Number(target.slice("node:".length));
  return nodes.find((n) => n.id === id)?.name || t("diag.serverNode", { id });
}
