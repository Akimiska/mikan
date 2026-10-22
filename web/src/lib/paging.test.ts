import { describe, expect, it } from "vitest";
import { nextOffset, pagedItems } from "./paging";

const page = (total: number, ...ids: number[]) => ({ total, items: ids.map((id) => ({ id })) });

describe("nextOffset", () => {
  it("asks for the rows after those loaded while some are left", () => {
    const first = page(5, 1, 2);
    expect(nextOffset(first, [first])).toBe(2);
    const second = page(5, 3, 4);
    expect(nextOffset(second, [first, second])).toBe(4);
  });
  it("stops once every row is loaded", () => {
    const only = page(2, 1, 2);
    expect(nextOffset(only, [only])).toBeUndefined();
    const empty = page(0);
    expect(nextOffset(empty, [empty])).toBeUndefined();
  });
  it("goes by the total of the last page: rows deleted meanwhile end the list sooner", () => {
    const first = page(4, 1, 2);
    const second = page(3, 4);
    expect(nextOffset(second, [first, second])).toBeUndefined();
  });
});

describe("pagedItems", () => {
  it("keeps each row once, where it first came", () => {
    // A row added meanwhile moved the offset: row 2 comes again on the second page.
    expect(pagedItems([page(4, 1, 2), page(4, 2, 3)]).map((x) => x.id)).toEqual([1, 2, 3]);
  });
  it("is empty before any page", () => {
    expect(pagedItems([])).toEqual([]);
  });
});
