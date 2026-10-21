// The servers' own addresses as one-tap choices for the checks that take an address: the
// panel's IP and domain, the subscription domain and each node's.
import { useNodes, useSettings } from "../../api/hooks";
import { t } from "../../i18n";

export type ServerPick = { value: string; label: string };

/** The host of a node's API address, host:port or [v6]:port. */
function hostOf(address: string): string {
  const v6 = /^\[([^\]]+)\](?::\d+)?$/.exec(address);
  if (v6?.[1]) return v6[1];
  return address.replace(/:\d+$/, "");
}

/** Every address of the panel and the nodes, each once, the panel's first. */
export function useServerPicks(): ServerPick[] {
  const settings = useSettings();
  const nodes = useNodes();
  const out: ServerPick[] = [];
  const add = (value: string | null | undefined, label: string) => {
    const v = value?.trim();
    if (v && !out.some((p) => p.value.toLowerCase() === v.toLowerCase())) out.push({ value: v, label });
  };
  const s = settings.data;
  add(s?.public_host, t("serverPicks.panel"));
  add(s?.domain, t("serverPicks.panel"));
  add(s?.sub_domain, t("serverPicks.sub"));
  for (const n of nodes.data ?? []) {
    if (n.address) add(hostOf(n.address), n.name);
    add(n.host, n.name);
    add(n.domain, n.name);
  }
  return out;
}

/** The choices under an address field; the one in the field is marked. */
export function ServerPicks({ picks, value, onPick }: { picks: ServerPick[]; value: string; onPick: (v: string) => void }) {
  if (!picks.length) return null;
  return (
    <div className="mb-4 flex flex-wrap items-center gap-2" role="group" aria-label={t("serverPicks.title")}>
      <span className="text-xs text-[var(--ink-500)]">{t("serverPicks.title")}</span>
      {picks.map((p) => (
        <button key={p.value} type="button" className="chip" aria-pressed={value.trim() === p.value} onClick={() => onPick(p.value)} aria-label={`${p.label}: ${p.value}`} title={p.value}>
          <span className="opacity-70">{p.label}</span>
          <span className="max-w-[220px] truncate font-mono text-xs">{p.value}</span>
        </button>
      ))}
    </div>
  );
}
