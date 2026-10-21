import { describe, expect, it } from "vitest";
import { cleanCSS } from "../sub/page";
import { same } from "./draft";
import { splitList } from "./list";
import { isKopecks, isStars, parseDecimal, rubKopecks } from "./validate";

describe("numbers as typed", () => {
  it("take a comma for the decimal point", () => {
    expect(parseDecimal("12,5")).toBe(12.5);
    expect(parseDecimal("12.5")).toBe(12.5);
    expect(rubKopecks("199,50")).toBe(19950);
    expect(rubKopecks("0.1")).toBe(10);
    expect(Number.isNaN(parseDecimal("abc"))).toBe(true);
  });
  it("bound the prices the server accepts", () => {
    expect(isStars(1)).toBe(true);
    expect(isStars(1.5)).toBe(false);
    expect(isStars(0)).toBe(false);
    expect(isKopecks(Number.NaN)).toBe(false);
  });
});

describe("splitList", () => {
  it("splits by lines and commas, trimmed, the empty left out", () => {
    expect(splitList(" a.com\n\nb.com, c.com ,\n")).toEqual(["a.com", "b.com", "c.com"]);
    expect(splitList("")).toEqual([]);
  });
});

describe("same", () => {
  it("compares by value", () => {
    expect(same({ a: [1, 2] }, { a: [1, 2] })).toBe(true);
    expect(same({ a: 1 }, { a: 2 })).toBe(false);
  });
});

describe("cleanCSS", () => {
  it("drops what the server drops: <, escapes, @import, other sites' urls", () => {
    expect(cleanCSS("a{}</style><script>")).toBe("a{}/style>script>");
    expect(cleanCSS("@im\\port url(x)")).toBe("url(x)");
    expect(cleanCSS("@IMPORT 'x';")).toBe("'x';");
    expect(cleanCSS("b{background:url( 'https://evil.example/a.png')}")).toBe("b{background:none}");
    expect(cleanCSS("b{background:url(//evil.example/a.png)}")).toBe("b{background:none}");
    expect(cleanCSS("b{background:url(/a.png)}")).toBe("b{background:url(/a.png)}");
  });
  it("keeps dropping until nothing is left to drop", () => {
    expect(cleanCSS("@imp@importort x")).toBe("x");
  });
});
