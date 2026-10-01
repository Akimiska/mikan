import type { Schemas } from "../api/client";
import { t } from "../i18n";

export type Fingerprint = Schemas["SettingsView"]["client_fingerprint"];

// Mirrors proto.Fingerprints: the uTLS profiles mihomo, Xray and sing-box all accept.
export const FINGERPRINTS: readonly Fingerprint[] = ["chrome", "firefox", "safari", "ios", "android", "edge", "360", "qq", "random", "randomized"];

const BROWSERS: Partial<Record<Fingerprint, string>> = {
  chrome: "Chrome",
  firefox: "Firefox",
  safari: "Safari (macOS)",
  ios: "Safari (iOS)",
  android: "Android (OkHttp)",
  edge: "Edge",
  "360": "360 Browser",
  qq: "QQ Browser",
};

/** What the select shows: a browser's name, or what a random profile does. */
export function fingerprintLabel(fp: Fingerprint): string {
  if (fp === "random") return t("settings.fpRandom");
  if (fp === "randomized") return t("settings.fpRandomized");
  return BROWSERS[fp] ?? fp;
}
