import { getLocale, t } from "../i18n";

export const GiB = 1024 ** 3;

// Formatters follow the UI language; the apps remount on a switch, so caching per call
// site is not needed, only per locale.
const cache = new Map<string, Intl.NumberFormat | Intl.DateTimeFormat>();
function nf(max: number): Intl.NumberFormat {
  const key = `n${getLocale()}${max}`;
  let f = cache.get(key) as Intl.NumberFormat | undefined;
  if (!f) cache.set(key, (f = new Intl.NumberFormat(getLocale(), { maximumFractionDigits: max })));
  return f;
}
function df(opts: Intl.DateTimeFormatOptions, id: string): Intl.DateTimeFormat {
  const key = `d${getLocale()}${id}`;
  let f = cache.get(key) as Intl.DateTimeFormat | undefined;
  if (!f) cache.set(key, (f = new Intl.DateTimeFormat(getLocale(), opts)));
  return f;
}

function scaled(v: number, units: string[], step: number, first: string): string {
  if (v < step) return `${nf(0).format(v)} ${first}`;
  let x = v / step;
  let i = 0;
  while (x >= step && i < units.length - 1) {
    x /= step;
    i++;
  }
  return `${x >= 100 ? nf(0).format(x) : nf(1).format(x)} ${units[i]}`;
}

export function bytes(n: number): string {
  return scaled(n, [t("units.kb"), t("units.mb"), t("units.gb"), t("units.tb"), t("units.pb")], 1024, t("units.b"));
}

export function bits(bps: number): string {
  return scaled(bps, [t("units.kbps"), t("units.mbps"), t("units.gbps")], 1000, t("units.bps"));
}

export function num(n: number): string {
  return nf(0).format(n);
}

export const days = (n: number) => t("time.days", { n });

export function dateShort(iso: string): string {
  return df({ day: "numeric", month: "short" }, "short").format(new Date(iso));
}

export function dateLong(iso: string): string {
  return df({ day: "numeric", month: "long", year: "numeric" }, "long").format(new Date(iso));
}

export function time(iso: string): string {
  return df({ hour: "2-digit", minute: "2-digit", hour12: false }, "time").format(new Date(iso));
}

/** Whole days until iso (negative when in the past), counted by calendar-free 24 h steps. */
export function daysUntil(iso: string, now = Date.now()): number {
  return Math.ceil((new Date(iso).getTime() - now) / 86_400_000);
}

export function expiryText(iso: string | null | undefined): { text: string; tone: "" | "warn" | "bad" } {
  if (!iso) return { text: t("time.forever"), tone: "" };
  const d = daysUntil(iso);
  if (d < 0) return { text: t("time.expiredAgo", { n: -d }), tone: "bad" };
  if (d === 0) return { text: t("time.today"), tone: "warn" };
  if (d === 1) return { text: t("time.tomorrow"), tone: "warn" };
  return { text: days(d), tone: d <= 7 ? "warn" : "" };
}

export function ago(iso: string, now = Date.now()): string {
  const s = Math.max(0, Math.round((now - new Date(iso).getTime()) / 1000));
  if (s < 60) return t("time.justNow");
  const m = Math.round(s / 60);
  if (m < 60) return t("time.minAgo", { n: m });
  const h = Math.round(m / 60);
  if (h < 24) return t("time.hAgo", { n: h });
  return t("time.daysAgo", { n: Math.round(h / 24) });
}

export function uptime(iso: string, now = Date.now()): string {
  const s = Math.max(0, Math.round((now - new Date(iso).getTime()) / 1000));
  const d = Math.floor(s / 86400);
  const h = Math.floor((s % 86400) / 3600);
  const m = Math.floor((s % 3600) / 60);
  if (d > 0) return t("time.uptimeDays", { d, h });
  if (h > 0) return t("time.uptimeHours", { h, m });
  return t("time.uptimeMinutes", { m });
}

/** Keeps the network part of an IP readable and hides the rest in lists. */
export function maskIP(ip: string): string {
  if (ip.includes(":")) return ip.split(":").slice(0, 3).join(":") + ":…";
  const p = ip.split(".");
  return p.length === 4 ? `${p[0]}.${p[1]}.•••.•••` : ip;
}
