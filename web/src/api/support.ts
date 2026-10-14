// Support access from both sides of it: the admin's grant (what the panel is open to, the
// operator's journal) and what an operator sees of their own session (whoami).
import { useQuery } from "@tanstack/react-query";
import { api, unwrap, type Schemas } from "./client";

export type SupportGrant = Schemas["SupportGrantView"];
export type SupportWhoami = Schemas["SupportWhoami"];
export type SupportAction = Schemas["AuditEntry"];

export const supportKey = {
  whoami: ["support", "whoami"] as const,
  grants: ["support", "grants"] as const,
  recordings: (source: string) => ["support", "recordings", source] as const,
  recording: (source: string, name: string) => ["support", "recording", source, name] as const,
};

/** An operator's own session. A panel that answers it is being used as support; an admin gets a 403. */
export const whoamiQuery = {
  queryKey: supportKey.whoami,
  queryFn: ({ signal }: { signal: AbortSignal }) => unwrap(api.GET("/api/v1/support/whoami", { signal })),
  staleTime: 15_000,
  retry: false,
};

/**
 * What the operator's session is (level, end, nodes), or undefined when the page is the
 * admin's own. The router loads it for an operator before any page renders (see
 * router.tsx); `poll` keeps it fresh in one place so a grant that ended is noticed.
 */
export function useSupportMode(poll = false): SupportWhoami | undefined {
  const known = useQuery({ ...whoamiQuery, enabled: false }).data;
  // Asked again only where it is known to be an operator's page: an admin would be refused.
  const fresh = useQuery({ ...whoamiQuery, enabled: poll && !!known, refetchInterval: 30_000 }).data;
  return fresh ?? known;
}

/** The admin's grant: the last one issued, and the journal of what happened under it. */
export function useSupportGrants(enabled = true) {
  return useQuery({
    queryKey: supportKey.grants,
    queryFn: ({ signal }) => unwrap(api.GET("/api/v1/support/grants", { signal })),
    enabled,
    // A live grant is watched (the time, the operator coming in); a quiet one is not.
    refetchInterval: (q) => (q.state.data?.grant?.status === "active" ? 20_000 : false),
  });
}

/** The grant is live: not ended by its time or by the admin. */
export function isLive(g: SupportGrant | null | undefined): g is SupportGrant {
  return !!g && g.status === "active" && new Date(g.expires_at).getTime() > Date.now();
}

/** A server of the terminal or the recordings: "panel" or "node:<id>". */
export function targetNode(target: string): number | null {
  const m = /^node:(\d+)$/.exec(target);
  return m ? Number(m[1]) : null;
}
