import { Bot, LayoutPanelTop, Puzzle, type LucideIcon } from "lucide-react";
import { api, unwrap, type Schemas } from "../../api/client";
import { t, type Key } from "../../i18n";
import { localized } from "../../lib/localized";
import type { State } from "./addon-row";

// The protocol 2 addons' data and how an addon is named: what the addons page, its dialogs
// and the addon pages share.

export type Market = Schemas["MarketView"];
export type MarketEntry = Schemas["MarketEntry"];
export type MarketInstalled = Schemas["MarketInstalled"];
export type Kind = MarketInstalled["kind"];
type Payments = Schemas["AddonsView"];
type Telegram = Schemas["TelegramView"];

/** What an addon's id may be, as the server has it. */
export const ADDON_ID_RE = /^[a-z0-9][a-z0-9-]{0,31}$/;

/** The host takes requests: the panel supports addons and the host is new enough. */
export function marketReady(m: Market | undefined): boolean {
  return !!m && m.supported && m.host_ready;
}

/** Every addon the server has a request for: its actions wait until the server is done. */
export function queuedIds(m: Market | undefined, p: Payments | undefined): Set<string> {
  const ids = new Set<string>();
  for (const r of p?.queue ?? []) ids.add(r.id);
  if (p?.pending) ids.add(p.pending.id);
  if (m?.pending) ids.add(m.pending.id);
  return ids;
}

/** Install (or update) an addon of the catalog with the permissions its entry asks for. */
export function installMarket(e: MarketEntry) {
  return unwrap(api.POST("/api/v1/marketplace/{id}/install", { params: { path: { id: e.id } }, body: { permissions: e.permissions } }));
}

/** Install (or update) a payment method from the catalog. */
export function installPayment(id: string) {
  return unwrap(api.POST("/api/v1/addons/{id}/install", { params: { path: { id } } }));
}

/** How the panel's bot is doing; the words are the caller's. */
export type BotPhase = "off" | "ok" | "reconnecting" | "starting" | "down";

/** The bot of a set-up panel by its connection: on or off, running, reconnecting or down. */
export function botStatus(tg: Telegram, words: Record<BotPhase, Key>): State {
  const at = (tone: State["tone"], phase: BotPhase): State => ({ tone, text: t(words[phase]) });
  if (!tg.enabled) return at("off", "off");
  if (tg.running && !tg.error) return at("ok", "ok");
  if (tg.running) return at("warn", "reconnecting");
  if (!tg.error) return at("off", "starting");
  return at("bad", "down");
}

/** The catalog's entry of an installed addon; none for an addon of the admin's own. */
export function entryOf(id: string, d: Market | undefined): MarketEntry | undefined {
  const own = d?.installed.find((a) => a.id === id && a.source !== "official");
  return own ? undefined : d?.catalog.find((e) => e.id === id);
}

/** The addon's name in the catalog; an addon of the admin's own is known by its id. */
export function marketName(id: string, d: Market | undefined): string {
  return localized(entryOf(id, d)?.name, id);
}

export const KIND_ICONS: Record<Kind, LucideIcon> = { bot: Bot, ui: LayoutPanelTop, generic: Puzzle };

/** Every permission the panel knows, in the order the admin reads them, with its words. */
export const PERMISSIONS: { id: string; label: Key; text: Key }[] = [
  { id: "bot", label: "market.perm.bot", text: "market.perm.botText" },
  { id: "users:read", label: "market.perm.usersRead", text: "market.perm.usersReadText" },
  { id: "users:write", label: "market.perm.usersWrite", text: "market.perm.usersWriteText" },
  { id: "billing:invoice", label: "market.perm.invoice", text: "market.perm.invoiceText" },
  { id: "events", label: "market.perm.events", text: "market.perm.eventsText" },
  { id: "kv", label: "market.perm.kv", text: "market.perm.kvText" },
  { id: "ui", label: "market.perm.ui", text: "market.perm.uiText" },
];

/** In the order of PERMISSIONS; one the panel does not know stays last, by its name. */
export function sortPermissions(perms: string[]): string[] {
  const at = (p: string) => {
    const i = PERMISSIONS.findIndex((x) => x.id === p);
    return i < 0 ? PERMISSIONS.length : i;
  };
  return [...perms].sort((a, b) => at(a) - at(b) || a.localeCompare(b));
}

/** Talking to the clients for the panel or giving them days: the addon is as good as an admin. */
export function actsAsAdmin(perms: string[]): boolean {
  return perms.includes("bot") || perms.includes("users:write");
}

/** Its pages in the admin panel: granted and running. */
export function hasPages(a: MarketInstalled): boolean {
  return a.status === "running" && a.granted.includes("ui");
}

/** Where the addon's own pages open (the route of the addon pages, a plain path here). */
export function pagesPath(id: string): string {
  return `/addons/app/${encodeURIComponent(id)}`;
}
