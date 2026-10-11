import { Link } from "@tanstack/react-router";
import { ArrowLeft, Bot, ChevronRight, ListFilter, Plus, ShieldBan, Wallet, type LucideIcon } from "lucide-react";
import type { ReactNode } from "react";
import { useFilters, useTelegram, useTorrent } from "../../api/hooks";
import { QueryBoundary } from "../../components/query";
import { PageHeader, Pill, Skeleton } from "../../components/ui";
import { t } from "../../i18n";
import { num } from "../../lib/format";
import { addonName, useAddons } from "./payment-addons";
import { KIND_ICONS, hasPages, marketName, pagesPath, useMarketplace } from "./market-common";
import { TorrentCard, TorrentHitsCard } from "./settings/torrent";

/**
 * The addons: the tools built into the panel (the Telegram bot, the torrent blocker), the
 * marketplace's addons (bots, pages) and its payment methods, each with its state and the way
 * to its settings. The
 * main menu keeps the everyday sections; what is switched on once lives here.
 */
export function AddonsPage() {
  return (
    <>
      <PageHeader title={t("nav.addons")} sub={t("addons.subtitle")} />
      <h2 className="section-title">{t("addons.tools")}</h2>
      <div className="addon-grid">
        <TelegramTile />
        <TorrentTile />
        <FiltersTile />
      </div>
      <h2 className="section-title mt-6">{t("market.section")}</h2>
      <MarketTiles />
      <h2 className="section-title mt-6">{t("addons.payments")}</h2>
      <PaymentTiles />
    </>
  );
}

/** One addon: its icon and state on top, what it does, and the way to its settings at the foot. */
function Tile({
  to,
  search,
  icon: Icon,
  title,
  sub,
  state,
  facts,
  action = t("addons.open"),
  add = false,
  i,
}: {
  to: string;
  search?: Record<string, string>;
  icon: LucideIcon;
  title: string;
  sub: string;
  state: ReactNode;
  /** What it is set to now, label and value; undefined while it loads. */
  facts?: [string, string][];
  action?: string;
  add?: boolean;
  i: number;
}) {
  return (
    <Link to={to} search={search} className={`card glass reveal addon-tile${add ? " add" : ""}`} style={{ "--i": i } as React.CSSProperties}>
      <span className="flex items-start justify-between gap-3">
        <span className="addon-icon" aria-hidden>
          <Icon size={24} />
        </span>
        {state}
      </span>
      <span className="min-w-0">
        <span className="addon-title">{title}</span>
        <span className="mt-1 block text-[13px] leading-5 text-[var(--ink-500)]">{sub}</span>
      </span>
      {facts?.length ? (
        <span className="addon-facts">
          {facts.map(([label, value]) => (
            <span key={label} className="min-w-0">
              <span className="block text-xs text-[var(--ink-500)]">{label}</span>
              <span className="block truncate text-[13px] font-semibold">{value}</span>
            </span>
          ))}
        </span>
      ) : null}
      <span className="addon-action">
        {action}
        <ChevronRight size={16} aria-hidden />
      </span>
    </Link>
  );
}

function TelegramTile() {
  const tg = useTelegram();
  const v = tg.data;
  const state = !v ? null : v.running ? <Pill tone="ok">{t("addons.on")}</Pill> : v.enabled ? <Pill tone="warn">{t("addons.stopped")}</Pill> : <Pill tone="off">{t("addons.off")}</Pill>;
  const facts: [string, string][] | undefined = v && [
    [t("addons.factBot"), v.bot ? `@${v.bot.username}` : t("addons.factNoBot")],
    [t("addons.factLinked"), num(v.linked)],
  ];
  return <Tile to="/addons/telegram" icon={Bot} title={t("addons.telegram")} sub={t("addons.telegramSub")} state={state} facts={facts} i={1} />;
}

function TorrentTile() {
  const q = useTorrent();
  const state = !q.data ? null : q.data.enabled ? <Pill tone="ok">{t("addons.on")}</Pill> : <Pill tone="off">{t("addons.off")}</Pill>;
  const d = q.data;
  const facts: [string, string][] | undefined = d && [
    [t("addons.factBan"), d.ban_minutes > 0 ? t("addons.factBanMinutes", { n: d.ban_minutes }) : t("addons.factBanReset")],
    [t("addons.factExempt"), d.exempt.length ? num(d.exempt.length) : t("addons.factNone")],
  ];
  return <Tile to="/addons/torrent" icon={ShieldBan} title={t("settings.torrent.title")} sub={t("addons.torrentSub")} state={state} facts={facts} i={2} />;
}

function FiltersTile() {
  const q = useFilters();
  const on = q.data ? q.data.egress.enabled || q.data.ingress.enabled : undefined;
  const state = on === undefined ? null : on ? <Pill tone="ok">{t("addons.on")}</Pill> : <Pill tone="off">{t("addons.off")}</Pill>;
  const d = q.data;
  // The mail ports are one switch, so one rule.
  const out = d ? d.egress.ports.length + d.egress.networks.length + d.egress.domains.length + (d.egress.mail ? 1 : 0) : 0;
  const nets = d?.ingress.networks.length ?? 0;
  const facts: [string, string][] | undefined = d && [
    [t("addons.factOut"), d.egress.enabled && out > 0 ? t("addons.factRules", { n: out }) : t("addons.factNone")],
    [
      t("addons.factIn"),
      d.ingress.enabled && nets > 0 ? (d.ingress.allow ? t("addons.factInAllow", { n: nets }) : t("addons.factInDeny", { n: nets })) : t("addons.factInAny"),
    ],
  ];
  return <Tile to="/addons/filters" icon={ListFilter} title={t("filters.title")} sub={t("addons.filtersSub")} state={state} facts={facts} i={3} />;
}

// The marketplace's other addons (bots, pages): installed ones with their state and where they
// come from, and the way to the catalog. An addon with pages opens on them; the rest, and the
// installing and removing, are on the marketplace's page.
function MarketTiles() {
  const q = useMarketplace();
  return (
    <QueryBoundary
      query={q}
      pending={
        <div className="addon-grid">
          <Skeleton style={{ height: 200, borderRadius: 20 }} />
        </div>
      }
      wrap={(state) => <section className="card glass">{state}</section>}
    >
      {(d) => (
        <div className="addon-grid">
          {d.installed.map((a, i) => {
            const pages = hasPages(a);
            return (
              <Tile
                key={a.id}
                to={pages ? pagesPath(a.id) : "/addons/market"}
                icon={KIND_ICONS[a.kind]}
                title={marketName(a.id, d)}
                sub={t(`market.kind.${a.kind}`)}
                state={
                  <span className="flex flex-wrap justify-end gap-1">
                    {a.source !== "official" ? <Pill tone="warn">{t("market.unverified")}</Pill> : null}
                    {a.status === "failed" ? <Pill tone="bad">{t("addons.stateFailed")}</Pill> : <Pill tone="ok">{t("market.running")}</Pill>}
                  </span>
                }
                facts={[
                  [t("market.factSource"), t(`market.source.${a.source}`)],
                  [t("market.factPerms"), t("market.permsN", { n: a.granted.length })],
                ]}
                action={pages ? t("market.openPages") : t("market.manage")}
                i={4 + i}
              />
            );
          })}
          <Tile
            to="/addons/market"
            search={{ open: "catalog" }}
            icon={Plus}
            title={t("market.add")}
            sub={t("market.addSub", { n: d.catalog.length })}
            state={null}
            action={t("addons.openCatalog")}
            add
            i={4 + d.installed.length}
          />
        </div>
      )}
    </QueryBoundary>
  );
}

// The payment methods of the marketplace: installed ones with their state, and the way to
// install and set them up, which stays in Payments.
function PaymentTiles() {
  const q = useAddons();
  return (
    <QueryBoundary
      query={q}
      pending={
        <div className="addon-grid">
          <Skeleton style={{ height: 200, borderRadius: 20 }} />
        </div>
      }
      wrap={(state) => <section className="card glass">{state}</section>}
    >
      {(d) => (
        <div className="addon-grid">
          {d.installed.map((a, i) => (
            <Tile
              key={a.id}
              to="/payments"
              search={{ tab: "methods" }}
              icon={Wallet}
              title={addonName(a.id, d)}
              sub={t("addons.paymentSub")}
              state={a.status === "failed" ? <Pill tone="bad">{t("addons.stateFailed")}</Pill> : a.enabled ? <Pill tone="ok">{t("addons.on")}</Pill> : <Pill tone="off">{t("addons.off")}</Pill>}
              i={4 + i}
            />
          ))}
          <Tile
            to="/payments"
            search={{ tab: "methods" }}
            icon={Plus}
            title={t("addons.paymentAdd")}
            sub={t("addons.paymentAddSub", { n: d.catalog.length })}
            state={null}
            action={t("addons.openCatalog")}
            add
            i={4 + d.installed.length}
          />
        </div>
      )}
    </QueryBoundary>
  );
}

/** The torrent blocker, an addon of its own: its switch and its catches. */
export function TorrentPage() {
  return (
    <>
      <PageHeader title={t("settings.torrent.title")} sub={t("addons.torrentSub")} actions={<BackToAddons />} />
      <div className="flex max-w-4xl flex-col gap-4">
        <TorrentCard />
        <TorrentHitsCard />
      </div>
    </>
  );
}

export function BackToAddons() {
  return (
    <Link to="/addons" className="btn btn-glass">
      <ArrowLeft size={16} aria-hidden />
      {t("addons.back")}
    </Link>
  );
}
