import { describe, expect, it } from "vitest";
import { installPlatform } from "./pwa";

// Real user agents of the browsers the install card tells apart.
const UA = {
  iphoneSafari: "Mozilla/5.0 (iPhone; CPU iPhone OS 17_5 like Mac OS X) AppleWebKit/605.1.15 (KHTML, like Gecko) Version/17.5 Mobile/15E148 Safari/604.1",
  iphoneChrome: "Mozilla/5.0 (iPhone; CPU iPhone OS 17_5 like Mac OS X) AppleWebKit/605.1.15 (KHTML, like Gecko) CriOS/126.0.6478.54 Mobile/15E148 Safari/604.1",
  macSafari: "Mozilla/5.0 (Macintosh; Intel Mac OS X 10_15_7) AppleWebKit/605.1.15 (KHTML, like Gecko) Version/17.5 Safari/605.1.15",
  macChrome: "Mozilla/5.0 (Macintosh; Intel Mac OS X 10_15_7) AppleWebKit/537.36 (KHTML, like Gecko) Chrome/141.0.0.0 Safari/537.36",
  winChrome: "Mozilla/5.0 (Windows NT 10.0; Win64; x64) AppleWebKit/537.36 (KHTML, like Gecko) Chrome/141.0.0.0 Safari/537.36",
  winEdge: "Mozilla/5.0 (Windows NT 10.0; Win64; x64) AppleWebKit/537.36 (KHTML, like Gecko) Chrome/141.0.0.0 Safari/537.36 Edg/141.0.0.0",
  yandex: "Mozilla/5.0 (Windows NT 10.0; Win64; x64) AppleWebKit/537.36 (KHTML, like Gecko) Chrome/138.0.0.0 YaBrowser/25.8.0.0 Safari/537.36",
  samsung: "Mozilla/5.0 (Linux; Android 14; SM-S918B) AppleWebKit/537.36 (KHTML, like Gecko) SamsungBrowser/26.0 Chrome/122.0.0.0 Mobile Safari/537.36",
  androidChrome: "Mozilla/5.0 (Linux; Android 10; K) AppleWebKit/537.36 (KHTML, like Gecko) Chrome/141.0.0.0 Mobile Safari/537.36",
  winFirefox: "Mozilla/5.0 (Windows NT 10.0; Win64; x64; rv:143.0) Gecko/20100101 Firefox/143.0",
  androidFirefox: "Mozilla/5.0 (Android 14; Mobile; rv:143.0) Gecko/143.0 Firefox/143.0",
  iphoneFirefox: "Mozilla/5.0 (iPhone; CPU iPhone OS 17_5 like Mac OS X) AppleWebKit/605.1.15 (KHTML, like Gecko) FxiOS/131.0 Mobile/15E148 Safari/605.1.15",
};

describe("installPlatform", () => {
  it("every browser on an iPhone or iPad installs through Share", () => {
    expect(installPlatform(UA.iphoneSafari, 5)).toBe("ios");
    expect(installPlatform(UA.iphoneChrome, 5)).toBe("ios");
    expect(installPlatform(UA.iphoneFirefox, 5)).toBe("ios");
    // iPadOS asks for desktop sites as a Mac: the touch screen tells it apart.
    expect(installPlatform(UA.macSafari, 5)).toBe("ios");
  });

  it("Safari on a Mac adds to the Dock", () => {
    expect(installPlatform(UA.macSafari, 0)).toBe("mac-safari");
  });

  it("Chromium browsers have the button, with their menu on a computer or on Android besides", () => {
    for (const ua of [UA.macChrome, UA.winChrome, UA.winEdge, UA.yandex]) {
      expect(installPlatform(ua, 0)).toBe("chromium");
    }
    for (const ua of [UA.samsung, UA.androidChrome]) {
      expect(installPlatform(ua, 5)).toBe("chromium-android");
    }
  });

  it("Firefox installs on Android only", () => {
    expect(installPlatform(UA.winFirefox, 0)).toBe("firefox");
    expect(installPlatform(UA.androidFirefox, 5)).toBe("firefox-android");
  });

  it("anything else is told which browsers can", () => {
    expect(installPlatform("curl/8.10.1", 0)).toBe("other");
  });
});
