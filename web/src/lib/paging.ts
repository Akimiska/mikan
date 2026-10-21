// Lists that come a page at a time ("show more"): by an offset, or by a cursor of the last row.

/** The offset of a list's next page, or none once every row is loaded. */
export const nextOffset = <P extends { total: number; items: unknown[] }>(last: P, pages: P[]) => {
  const n = pages.reduce((sum, p) => sum + p.items.length, 0);
  return n < last.total ? n : undefined;
};

/** The rows of the pages loaded so far, each once: an offset moves when rows come meanwhile. */
export function pagedItems<T extends { id: number }>(pages: { items: T[] }[]): T[] {
  return [...new Map(pages.flatMap((p) => p.items).map((x) => [x.id, x])).values()];
}
