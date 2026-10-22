// The page for strangers: one at the panel's address (settings.site_template) and one on each
// node (its front_page), which a node shows from 0.5.1.1-rc.12 on.

/** The page the panel and every node have; null when they differ. */
export function commonPage<T extends string>(pages: T[]): T | null {
  const [first] = pages;
  return first !== undefined && pages.every((p) => p === first) ? first : null;
}

/** The first node version that shows the page: 0.5.1.1-rc.12, a release counting after its betas. */
const FIRST = [0, 5, 1, 1, 12];

/** Whether a node on version shows the page. A version that does not read as a release (none
 * told, a dev build) counts as one that does: nothing warns without a reason. */
export function frontPageReady(version?: string): boolean {
  const m = /^v?(\d+)\.(\d+)\.(\d+)(?:\.(\d+))?(?:-rc\.(\d+))?$/.exec(version?.trim() ?? "");
  if (!m) return true;
  const v = [m[1], m[2], m[3], m[4] ?? "0"].map(Number).concat(m[5] != null ? Number(m[5]) : Infinity);
  for (const [i, want] of FIRST.entries()) {
    const have = v[i] ?? 0;
    if (have !== want) return have > want;
  }
  return true;
}
