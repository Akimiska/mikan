export const GiB = 1024 ** 3;

const nf1 = new Intl.NumberFormat("ru-RU", { maximumFractionDigits: 1 });
const nf0 = new Intl.NumberFormat("ru-RU", { maximumFractionDigits: 0 });

export function bytes(n: number): string {
  if (n < 1024) return `${nf0.format(n)} Б`;
  const units = ["КБ", "МБ", "ГБ", "ТБ", "ПБ"];
  let v = n / 1024;
  let i = 0;
  while (v >= 1024 && i < units.length - 1) {
    v /= 1024;
    i++;
  }
  return `${v >= 100 ? nf0.format(v) : nf1.format(v)} ${units[i]}`;
}

export function bits(bps: number): string {
  if (bps < 1000) return `${nf0.format(bps)} бит/с`;
  const units = ["Кбит/с", "Мбит/с", "Гбит/с"];
  let v = bps / 1000;
  let i = 0;
  while (v >= 1000 && i < units.length - 1) {
    v /= 1000;
    i++;
  }
  return `${v >= 100 ? nf0.format(v) : nf1.format(v)} ${units[i]}`;
}

export function num(n: number): string {
  return nf0.format(n);
}

export function plural(n: number, one: string, few: string, many: string): string {
  const m = Math.abs(n) % 100;
  const k = m % 10;
  if (m > 10 && m < 20) return many;
  if (k > 1 && k < 5) return few;
  if (k === 1) return one;
  return many;
}

export const days = (n: number) => `${n} ${plural(n, "день", "дня", "дней")}`;

const MONTHS = ["янв", "фев", "мар", "апр", "мая", "июн", "июл", "авг", "сен", "окт", "ноя", "дек"];
const MONTHS_LONG = ["января", "февраля", "марта", "апреля", "мая", "июня", "июля", "августа", "сентября", "октября", "ноября", "декабря"];

export function dateShort(iso: string): string {
  const d = new Date(iso);
  return `${d.getDate()} ${MONTHS[d.getMonth()]}`;
}

export function dateLong(iso: string): string {
  const d = new Date(iso);
  return `${d.getDate()} ${MONTHS_LONG[d.getMonth()]} ${d.getFullYear()}`;
}

export function time(iso: string): string {
  const d = new Date(iso);
  return `${String(d.getHours()).padStart(2, "0")}:${String(d.getMinutes()).padStart(2, "0")}`;
}

/** Whole days until iso (negative when in the past), counted by calendar-free 24 h steps. */
export function daysUntil(iso: string, now = Date.now()): number {
  return Math.ceil((new Date(iso).getTime() - now) / 86_400_000);
}

export function expiryText(iso: string | null | undefined): { text: string; tone: "" | "warn" | "bad" } {
  if (!iso) return { text: "бессрочно", tone: "" };
  const d = daysUntil(iso);
  if (d < 0) return { text: `истёк ${days(-d)} назад`, tone: "bad" };
  if (d === 0) return { text: "сегодня", tone: "warn" };
  if (d === 1) return { text: "завтра", tone: "warn" };
  return { text: days(d), tone: d <= 7 ? "warn" : "" };
}

export function ago(iso: string, now = Date.now()): string {
  const s = Math.max(0, Math.round((now - new Date(iso).getTime()) / 1000));
  if (s < 60) return "только что";
  const m = Math.round(s / 60);
  if (m < 60) return `${m} мин назад`;
  const h = Math.round(m / 60);
  if (h < 24) return `${h} ч назад`;
  const d = Math.round(h / 24);
  return `${days(d)} назад`;
}

export function uptime(iso: string, now = Date.now()): string {
  const s = Math.max(0, Math.round((now - new Date(iso).getTime()) / 1000));
  const d = Math.floor(s / 86400);
  const h = Math.floor((s % 86400) / 3600);
  const m = Math.floor((s % 3600) / 60);
  if (d > 0) return `${d} д ${h} ч`;
  if (h > 0) return `${h} ч ${m} мин`;
  return `${m} мин`;
}

/** Keeps the network part of an IP readable and hides the rest in lists. */
export function maskIP(ip: string): string {
  if (ip.includes(":")) return ip.split(":").slice(0, 3).join(":") + ":…";
  const p = ip.split(".");
  return p.length === 4 ? `${p[0]}.${p[1]}.•••.•••` : ip;
}
