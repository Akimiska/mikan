/** A list typed one entry a line, commas splitting too: the entries trimmed, the empty left
 * out. The panel puts them in their canonical form and order. */
export const splitList = (s: string): string[] =>
  s
    .split(/[\n,]+/)
    .map((x) => x.trim())
    .filter(Boolean);
