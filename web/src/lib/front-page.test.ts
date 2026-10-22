import { describe, expect, it } from "vitest";
import { commonPage, frontPageReady } from "./front-page";

describe("commonPage", () => {
  it("is the page the panel and every node have", () => {
    expect(commonPage(["cats", "cats", "cats"])).toBe("cats");
    expect(commonPage([""])).toBe("");
  });
  it("is none when they differ, or when there is nothing", () => {
    expect(commonPage(["cats", "", "cats"])).toBeNull();
    expect(commonPage(["maintenance", "cats"])).toBeNull();
    expect(commonPage([])).toBeNull();
  });
});

describe("frontPageReady", () => {
  it("holds from 0.5.1.1-rc.12 on, releases included", () => {
    for (const v of ["0.5.1.1-rc.12", "0.5.1.1-rc.13", "0.5.1.1", "0.5.1.2-rc.1", "0.5.2.0", "0.6.0.0", "1.0.0", "v0.5.1.1-rc.12"]) {
      expect(frontPageReady(v), v).toBe(true);
    }
  });
  it("does not before it", () => {
    for (const v of ["0.5.1.1-rc.11", "0.5.1.1-rc.2", "0.5.1.0", "0.5.0.4", "0.4.5"]) {
      expect(frontPageReady(v), v).toBe(false);
    }
  });
  it("warns of nothing for a version it cannot read", () => {
    for (const v of [undefined, "", "dev", "0.5.1.1-beta"]) {
      expect(frontPageReady(v), String(v)).toBe(true);
    }
  });
});
