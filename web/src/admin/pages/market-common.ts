import { useQuery } from "@tanstack/react-query";
import { Bot, LayoutPanelTop, Puzzle, type LucideIcon } from "lucide-react";
import { api, unwrap, type Schemas } from "../../api/client";
import { qk } from "../../api/hooks";
import { localized } from "../../components/manifest-form";
import type { Key } from "../../i18n";

// The protocol 2 addons' data and how an addon is named: what the addons page, its dialogs
// and the addon pages share.

export type Market = Schemas["MarketView"];
export type MarketEntry = Schemas["MarketEntry"];
export type MarketInstalled = Schemas["MarketInstalled"];
export type Kind = MarketInstalled["kind"];

/** The marketplace of protocol 2: followed every few seconds while the server works on a request. */
export function useMarketplace() {
  return useQuery({
    queryKey: qk.marketplace,
    queryFn: ({ signal }) => unwrap(api.GET("/api/v1/marketplace", { signal })),
    refetchInterval: (q) => (q.state.data?.pending ? 3_000 : false),
  });
}

/** Which addon answers in the subscribers' bot; empty while none does. */
export function useBotSlot() {
  return useQuery({
    queryKey: qk.botSlot,
    queryFn: ({ signal }) => unwrap(api.GET("/api/v1/telegram/bot-addon", { signal })),
  });
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
