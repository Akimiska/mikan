import { useMutation, useQueryClient } from "@tanstack/react-query";
import { Link, useNavigate, useSearch } from "@tanstack/react-router";
import { ExternalLink, PackagePlus, Plus, Puzzle, ShieldAlert, Trash2 } from "lucide-react";
import { useId, useState, type FormEvent } from "react";
import { ApiError, api, errorText, unwrap, type Schemas } from "../../api/client";
import { qk } from "../../api/hooks";
import { localized } from "../../components/manifest-form";
import { Confirm, Drawer, DrawerFooter } from "../../components/overlay";
import { QueryBoundary } from "../../components/query";
import { useToast } from "../../components/toast";
import { Button, EmptyState, ErrorState, Field, PageHeader, Pill, Segmented, Skeleton, Spinner } from "../../components/ui";
import { t } from "../../i18n";
import { fieldErrors } from "../../lib/fields";
import { safeHref } from "../../lib/url";
import { BackToAddons } from "./addons";
import {
  KIND_ICONS,
  PERMISSIONS,
  actsAsAdmin,
  entryOf,
  hasPages,
  marketName,
  pagesPath,
  sortPermissions,
  useMarketplace,
  type Kind,
  type Market,
  type MarketEntry,
  type MarketInstalled,
} from "./market-common";

/**
 * The marketplace of protocol 2 addons: bots, pages in the admin panel and the rest. Each one
 * gets exactly the permissions the admin approved; an addon of the admin's own (an image by its
 * digest, a process on the server) is code mikan has not seen, and says so everywhere.
 */
export function MarketplacePage() {
  const { open } = useSearch({ from: "/_app/addons/market" });
  const navigate = useNavigate({ from: "/addons/market" });
  const q = useMarketplace();
  const [approve, setApprove] = useState<MarketEntry | null>(null);
  const [own, setOwn] = useState<MarketInstalled | null>(null);
  const show = (v?: "catalog" | "own") => void navigate({ search: v ? { open: v } : {}, replace: true });
  const d = q.data;
  const ready = !!d && d.supported && d.host_ready;
  return (
    <>
      <PageHeader
        title={t("market.title")}
        sub={t("market.sub")}
        actions={
          <>
            <BackToAddons />
            <Button
              onClick={() => {
                setOwn(null);
                show("own");
              }}
              disabled={!ready}
            >
              <PackagePlus size={16} aria-hidden /> {t("market.own")}
            </Button>
            {/* The catalog may be looked at before the server can install from it. */}
            <Button variant="primary" onClick={() => show("catalog")} disabled={!d}>
              <Plus size={16} aria-hidden /> {t("market.catalog")}
            </Button>
          </>
        }
      />
      <div className="flex max-w-4xl flex-col gap-4">
        <QueryBoundary query={q} pending={<MarketSkeleton />} wrap={(state) => <section className="card glass">{state}</section>}>
          {(d) => (
            <>
              <Banners d={d} />
              <Installed
                d={d}
                ready={ready}
                onApprove={setApprove}
                onOwn={(a) => {
                  setOwn(a);
                  show("own");
                }}
                onCatalog={() => show("catalog")}
              />
            </>
          )}
        </QueryBoundary>
      </div>
      {d ? (
        <>
          <CatalogDrawer
            open={open === "catalog"}
            onOpenChange={(v) => show(v ? "catalog" : undefined)}
            d={d}
            ready={ready}
            onApprove={setApprove}
            onRetry={() => void q.refetch()}
            retrying={q.isFetching}
          />
          {/* The addon it updates stays through the closing animation; opening sets it again. */}
          <OwnDrawer open={open === "own" && ready} onOpenChange={(v) => show(v ? "own" : undefined)} update={own} />
          <ApproveDialog
            entry={approve}
            granted={approve ? (d.installed.find((a) => a.id === approve.id)?.granted ?? []) : []}
            onClose={() => setApprove(null)}
            onDone={() => open === "catalog" && show()}
          />
        </>
      ) : null}
    </>
  );
}

function MarketSkeleton() {
  return (
    <section className="card glass" aria-busy>
      <Skeleton style={{ height: 20, width: 160, borderRadius: 8 }} />
      {[0, 1].map((i) => (
        <div key={i} className="mt-6 flex items-start gap-3">
          <Skeleton style={{ height: 40, width: 40, borderRadius: 12 }} />
          <div className="flex min-w-0 flex-1 flex-col gap-2">
            <Skeleton style={{ height: 16, width: "40%", borderRadius: 8 }} />
            <Skeleton style={{ height: 12, width: "70%", borderRadius: 8 }} />
          </div>
        </div>
      ))}
    </section>
  );
}

/** Why nothing can be installed, what the server is doing now, and how its last request went. */
function Banners({ d }: { d: Market }) {
  const last = d.last && !d.pending && d.last.state === "failed" ? d.last : null;
  return (
    <>
      {!d.supported ? (
        <div className="banner warn" role="status">
          {t("market.unsupported")}
        </div>
      ) : !d.host_ready ? (
        <div className="banner warn flex-wrap" role="status">
          <span className="min-w-0 flex-1">{t("market.hostOutdated")}</span>
          <code className="mono">mikan update</code>
        </div>
      ) : null}
      {d.pending ? (
        <div className="banner info" role="status">
          <Spinner />
          <span className="min-w-0 flex-1">{t(d.pending.action === "remove" ? "addons.removing" : "addons.installing", { name: marketName(d.pending.id, d) })}</span>
        </div>
      ) : null}
      {last ? (
        <div className="banner err" role="alert">
          <span className="min-w-0 flex-1 break-words">{t(last.action === "remove" ? "addons.removeFailed" : "addons.installFailed", { name: marketName(last.id, d), error: last.error ?? "" })}</span>
        </div>
      ) : null}
    </>
  );
}

function Installed({
  d,
  ready,
  onApprove,
  onOwn,
  onCatalog,
}: {
  d: Market;
  ready: boolean;
  onApprove: (e: MarketEntry) => void;
  onOwn: (a: MarketInstalled) => void;
  onCatalog: () => void;
}) {
  return (
    <section className="card glass reveal" style={{ "--i": 1 } as React.CSSProperties} aria-busy={!!d.pending}>
      <div className="card-head">
        <h2 className="card-title">{t("market.installed")}</h2>
      </div>
      {d.installed.length === 0 ? (
        <EmptyState icon={Puzzle} title={t("market.empty")} text={t("market.emptyText")}>
          <Button variant="primary" onClick={onCatalog}>
            {t("addons.openCatalog")}
          </Button>
        </EmptyState>
      ) : (
        <ul className="row-list">
          {d.installed.map((a) => (
            <InstalledRow key={a.id} a={a} d={d} busy={!!d.pending || !ready} onApprove={onApprove} onOwn={() => onOwn(a)} />
          ))}
        </ul>
      )}
    </section>
  );
}

function InstalledRow({ a, d, busy, onApprove, onOwn }: { a: MarketInstalled; d: Market; busy: boolean; onApprove: (e: MarketEntry) => void; onOwn: () => void }) {
  const qc = useQueryClient();
  const toast = useToast();
  const [remove, setRemove] = useState(false);
  const name = marketName(a.id, d);
  const entry = entryOf(a.id, d);
  const mine = a.source !== "official";
  const Icon = KIND_ICONS[a.kind];
  const drop = useMutation({
    mutationFn: () => unwrap(api.POST("/api/v1/marketplace/{id}/remove", { params: { path: { id: a.id } } })),
    onSuccess: (v) => {
      qc.setQueryData(qk.marketplace, v);
      setRemove(false);
      toast.ok(t("addons.requested"));
    },
    onError: (e) => toast.error(errorText(e)),
  });
  return (
    <li className="flex flex-col gap-3 py-4 first:pt-0 last:pb-0">
      <div className="flex items-start gap-3">
        <span className="addon-icon !h-10 !w-10 !rounded-xl" aria-hidden>
          <Icon size={20} />
        </span>
        <div className="min-w-0 flex-1">
          <div className="flex flex-wrap items-center gap-2 text-[13px] font-semibold">
            <span className="min-w-0 break-words">{name}</span>
            {a.version ? <span className="num text-xs font-normal text-[var(--ink-500)]">v{a.version}</span> : null}
            {a.status === "failed" ? <Pill tone="bad">{t("market.failed")}</Pill> : <Pill tone="ok">{t("market.running")}</Pill>}
            {mine ? (
              <span title={t("market.unverifiedHint")}>
                <Pill tone="warn">{t("market.unverified")}</Pill>
              </span>
            ) : null}
            {entry?.wider ? <Pill tone="warn">{t("market.widerReady")}</Pill> : entry?.update ? <Pill tone="off">{t("market.updateReady")}</Pill> : null}
          </div>
          <div className="mt-1 text-xs text-[var(--ink-500)]">
            {t(`market.kind.${a.kind}`)} · {t(`market.source.${a.source}`)}
          </div>
          {a.status === "failed" && a.error ? <div className="mt-1 break-words text-xs text-[var(--berry-600)]">{a.error}</div> : null}
          {a.source === "custom" ? (
            <div className="mt-1 break-all text-xs text-[var(--ink-500)]">
              {t("market.image")}: <span className="mono" title={a.digest}>{`${a.image}@${shortDigest(a.digest)}`}</span>
            </div>
          ) : a.source === "external" ? (
            <div className="mt-1 break-all text-xs text-[var(--ink-500)]">
              {t("market.address")}: {a.url ? <span className="mono">{a.url}</span> : t("market.noAddress")}
            </div>
          ) : null}
        </div>
      </div>
      {entry?.wider ? (
        <div className="banner warn flex-wrap" role="status">
          <ShieldAlert size={16} className="shrink-0" aria-hidden />
          <span className="min-w-0 flex-1">{t("market.wider")}</span>
          <Button size="sm" variant="primary" disabled={busy} onClick={() => onApprove(entry)}>
            {t("market.review")}
          </Button>
        </div>
      ) : null}
      <div>
        <div className="mb-1 text-xs text-[var(--ink-500)]">{t("market.granted")}</div>
        {a.granted.length ? (
          <ul className="flex flex-wrap gap-1" aria-label={t("market.granted")}>
            {sortPermissions(a.granted).map((p) => (
              <li key={p}>
                <Pill tone="off">{permLabel(p)}</Pill>
              </li>
            ))}
          </ul>
        ) : (
          <div className="text-xs">{t("market.noPerms")}</div>
        )}
      </div>
      <div className="flex flex-wrap items-center gap-2">
        <Button size="sm" variant="danger" className="mr-auto" onClick={() => setRemove(true)} disabled={busy}>
          <Trash2 size={16} aria-hidden /> {t("market.remove")}
        </Button>
        {mine ? (
          <Button size="sm" onClick={onOwn} disabled={busy}>
            {t("market.update")}
          </Button>
        ) : entry?.update && !entry.wider ? (
          <Button size="sm" onClick={() => onApprove(entry)} disabled={busy}>
            {t("market.update")}
          </Button>
        ) : null}
        {hasPages(a) ? (
          <Link to={pagesPath(a.id)} className="btn btn-glass btn-sm" aria-label={t("market.openPagesLabel", { name })}>
            {t("market.openPages")}
          </Link>
        ) : null}
      </div>
      <Confirm
        open={remove}
        onOpenChange={setRemove}
        title={t("addons.removeTitle", { name })}
        text={t("market.removeText")}
        confirm={t("market.remove")}
        danger
        loading={drop.isPending}
        onConfirm={() => drop.mutate()}
      />
    </li>
  );
}

function shortDigest(digest = ""): string {
  return digest.length > 19 ? `${digest.slice(0, 19)}…` : digest;
}

function permLabel(p: string): string {
  const k = PERMISSIONS.find((x) => x.id === p);
  return k ? t(k.label) : p;
}

/** The permissions an addon asks for, each with what it lets the addon do; new ones marked. */
function PermissionList({ perms, fresh = [] }: { perms: string[]; fresh?: string[] }) {
  if (!perms.length) return <p className="mt-4 text-[13px]">{t("market.approveNone")}</p>;
  return (
    <ul className="mt-4 flex flex-col gap-3">
      {sortPermissions(perms).map((p) => {
        const k = PERMISSIONS.find((x) => x.id === p);
        return (
          <li key={p} className="text-[13px]">
            <div className="flex flex-wrap items-center gap-2 font-semibold">
              {k ? t(k.label) : p}
              <span className="mono text-xs font-normal text-[var(--ink-500)]">{p}</span>
              {fresh.includes(p) ? <Pill tone="warn">{t("market.permNew")}</Pill> : null}
            </div>
            {k ? <div className="mt-1 text-xs text-[var(--ink-500)]">{t(k.text)}</div> : null}
          </li>
        );
      })}
    </ul>
  );
}

function AdminWarning() {
  return (
    <div className="banner err mt-4 items-start" role="alert">
      <ShieldAlert size={16} className="mt-0.5 shrink-0" aria-hidden />
      <span className="min-w-0 flex-1">{t("market.adminWarn")}</span>
    </div>
  );
}

/**
 * The approval: the admin sees exactly what the catalog's build asks for and sends that very
 * list back; a catalog that changed in between is refused by the server and shown again.
 */
function ApproveDialog({ entry, granted, onClose, onDone }: { entry: MarketEntry | null; granted: string[]; onClose: () => void; onDone: () => void }) {
  const qc = useQueryClient();
  const toast = useToast();
  const install = useMutation({
    mutationFn: (e: MarketEntry) => unwrap(api.POST("/api/v1/marketplace/{id}/install", { params: { path: { id: e.id } }, body: { permissions: e.permissions } })),
    onSuccess: (v) => {
      qc.setQueryData(qk.marketplace, v);
      toast.ok(t("addons.requested"));
      onClose();
      onDone();
    },
    onError: (e) => {
      toast.error(errorText(e));
      if (e instanceof ApiError && e.detail === "permissions_changed") {
        void qc.invalidateQueries({ queryKey: qk.marketplace });
        onClose();
      }
    },
  });
  const name = entry ? localized(entry.name, entry.id) : "";
  const fresh = entry?.installed ? entry.permissions.filter((p) => !granted.includes(p)) : [];
  return (
    <Confirm
      open={!!entry}
      onOpenChange={(v) => !v && onClose()}
      title={t(entry?.installed ? "market.approveUpdateTitle" : "market.approveTitle", { name })}
      text={t("market.approveText")}
      confirm={t(entry?.installed ? "market.approveUpdate" : "market.approve")}
      loading={install.isPending}
      onConfirm={() => entry && install.mutate(entry)}
    >
      {entry ? (
        <>
          <PermissionList perms={entry.permissions} fresh={fresh} />
          {actsAsAdmin(entry.permissions) ? <AdminWarning /> : null}
        </>
      ) : null}
    </Confirm>
  );
}

function CatalogDrawer({
  open,
  onOpenChange,
  d,
  ready,
  onApprove,
  onRetry,
  retrying,
}: {
  open: boolean;
  onOpenChange: (v: boolean) => void;
  d: Market;
  ready: boolean;
  onApprove: (e: MarketEntry) => void;
  onRetry: () => void;
  retrying: boolean;
}) {
  return (
    <Drawer open={open} onOpenChange={onOpenChange} title={t("market.catalog")} meta={t("addons.catalogMeta")}>
      {d.catalog_error ? (
        <ErrorState text={t("errors.api.catalog_unavailable")} onRetry={retrying ? undefined : onRetry} />
      ) : d.catalog.length === 0 ? (
        <EmptyState icon={Puzzle} title={t("addons.catalogEmpty")} text={t("market.catalogEmptyText")} />
      ) : (
        <ul className="row-list">
          {d.catalog.map((e) => (
            <CatalogRow key={e.id} e={e} busy={!!d.pending || !ready} onApprove={() => onApprove(e)} />
          ))}
        </ul>
      )}
    </Drawer>
  );
}

function CatalogRow({ e, busy, onApprove }: { e: MarketEntry; busy: boolean; onApprove: () => void }) {
  const Icon = KIND_ICONS[e.kind];
  const href = safeHref(e.homepage);
  return (
    <li className="flex flex-col gap-3 py-4 first:pt-0">
      <div className="flex items-start gap-3">
        <span className="addon-icon !h-10 !w-10 !rounded-xl" aria-hidden>
          <Icon size={20} />
        </span>
        <div className="min-w-0 flex-1">
          <div className="flex flex-wrap items-center gap-2 text-[13px] font-semibold">
            {localized(e.name, e.id)}
            <span className="num text-xs font-normal text-[var(--ink-500)]">v{e.version}</span>
          </div>
          <div className="mt-1 text-xs text-[var(--ink-500)]">{t(`market.kind.${e.kind}`)}</div>
          {localized(e.description, "") ? <div className="mt-1 text-xs">{localized(e.description, "")}</div> : null}
          {href ? (
            <a className="link-btn mt-1 inline-flex items-center gap-1 text-xs" href={href} target="_blank" rel="noopener noreferrer">
              {t("addons.homepage")} <ExternalLink size={12} aria-hidden />
            </a>
          ) : null}
        </div>
        {e.installed && !e.update ? (
          <Pill tone="ok">{t("addons.installed")}</Pill>
        ) : (
          <Button size="sm" variant={e.installed ? "glass" : "primary"} disabled={busy} onClick={onApprove}>
            {e.wider ? t("market.review") : e.installed ? t("market.update") : t("addons.install")}
          </Button>
        )}
      </div>
      <div className="pl-[52px]">
        <div className="mb-1 text-xs text-[var(--ink-500)]">{t("market.factPerms")}</div>
        {e.permissions.length ? (
          <ul className="flex flex-wrap gap-1" aria-label={t("market.factPerms")}>
            {sortPermissions(e.permissions).map((p) => (
              <li key={p}>
                <Pill tone={p === "bot" || p === "users:write" ? "warn" : "off"}>{permLabel(p)}</Pill>
              </li>
            ))}
          </ul>
        ) : (
          <div className="text-xs">{t("market.noPerms")}</div>
        )}
      </div>
    </li>
  );
}

type Own = {
  source: "custom" | "external";
  id: string;
  image: string;
  digest: string;
  url: string;
  kind: Kind;
  mode: "http" | "client";
  permissions: string[];
  mem: string;
  confirm: boolean;
};

const ID_RE = /^[a-z0-9][a-z0-9-]{0,31}$/;
const DIGEST_RE = /^sha256:[a-f0-9]{64}$/;
// The host's grammar is checked on the server; this catches what an admin types most often.
const IMAGE_RE = /^[a-z0-9]+(?:[._-]+[a-z0-9]+)*(?::[0-9]{1,5})?(?:\/[a-z0-9]+(?:(?:[._]|__|-+)[a-z0-9]+)*)*$/;
const URL_RE = /^http:\/\/127\.0\.0\.1:([0-9]{4,5})\/?$/;

/** What is wrong before asking the server, as the server would say it. */
function check(o: Own): Record<string, string> {
  const out: Record<string, string> = {};
  if (!ID_RE.test(o.id)) out.id = t("errors.api.addon_id_invalid");
  if (o.source === "custom") {
    const last = o.image.split("/").pop() ?? "";
    if (last.includes(":") || o.image.includes("@")) out.image = t("market.ownImageTagged");
    else if (!IMAGE_RE.test(o.image) || o.image.length > 255) out.image = t("errors.api.addon_image_invalid");
    if (!DIGEST_RE.test(o.digest)) out.digest = t("errors.api.addon_digest_invalid");
  } else {
    const m = URL_RE.exec(o.url);
    const port = m ? Number(m[1]) : 0;
    if (o.url ? !(port >= 1024 && port <= 65535 && String(port) === m?.[1]) : o.mode !== "client") out.url = t("errors.api.addon_url_invalid");
  }
  if (o.mem) {
    const n = Number(o.mem);
    if (!Number.isInteger(n) || n < 64 || n > 256) out.mem_mb = t("errors.api.addon_mem_invalid");
  }
  return out;
}

function ownOf(a: MarketInstalled | null): Own {
  if (!a) return { source: "custom", id: "", image: "", digest: "", url: "", kind: "bot", mode: "http", permissions: [], mem: "", confirm: false };
  return {
    source: a.source === "external" ? "external" : "custom",
    id: a.id,
    image: a.image ?? "",
    digest: a.digest ?? "",
    url: a.url ?? "",
    kind: a.kind,
    mode: a.mode,
    permissions: a.granted,
    mem: a.mem_mb ? String(a.mem_mb) : "",
    confirm: false,
  };
}

/** An addon of the admin's own, new or a new build of one: nothing signed vouches for it. */
function OwnDrawer({ open, onOpenChange, update }: { open: boolean; onOpenChange: (v: boolean) => void; update: MarketInstalled | null }) {
  const qc = useQueryClient();
  const toast = useToast();
  const [o, setO] = useState<Own>(() => ownOf(update));
  const [tried, setTried] = useState(false);
  const install = useMutation({
    mutationFn: (body: Schemas["MarketCustomInputBody"]) => unwrap(api.POST("/api/v1/marketplace/custom", { body })),
    onSuccess: (v) => {
      qc.setQueryData(qk.marketplace, v);
      toast.ok(t("addons.requested"));
      onOpenChange(false);
    },
  });
  // Each opening starts from the addon it is for (or from nothing), not from the last attempt.
  const [wasOpen, setWasOpen] = useState(open);
  if (open !== wasOpen) {
    setWasOpen(open);
    if (open) {
      setO(ownOf(update));
      setTried(false);
      install.reset();
    }
  }
  const local = check(o);
  const server = fieldErrors(install.error);
  const submit = (e: FormEvent) => {
    e.preventDefault();
    setTried(true);
    if (Object.keys(local).length || !o.confirm || install.isPending) return;
    install.mutate({
      id: o.id,
      source: o.source,
      image: o.source === "custom" ? o.image : undefined,
      digest: o.source === "custom" ? o.digest : undefined,
      url: o.source === "external" && o.url ? o.url.replace(/\/$/, "") : undefined,
      kind: o.kind,
      mode: o.mode,
      permissions: o.permissions,
      mem_mb: o.source === "custom" && o.mem ? Number(o.mem) : undefined,
      confirm: o.confirm,
    });
  };
  return (
    <Drawer
      open={open}
      onOpenChange={onOpenChange}
      title={update ? t("market.ownUpdateTitle", { name: update.id }) : t("market.ownTitle")}
      meta={t("market.ownMeta")}
      footer={
        <DrawerFooter form="own-addon" onCancel={() => onOpenChange(false)} loading={install.isPending} disabled={!o.confirm}>
          {update ? t("market.ownUpdate") : t("market.ownInstall")}
        </DrawerFooter>
      }
    >
      <OwnForm
        o={o}
        set={(p) => setO((cur) => ({ ...cur, ...p }))}
        update={!!update}
        errors={{ ...(tried ? local : {}), ...server }}
        general={install.error && !Object.keys(server).length ? errorText(install.error) : ""}
        onSubmit={submit}
      />
    </Drawer>
  );
}

function OwnForm({
  o,
  set,
  update,
  errors,
  general,
  onSubmit,
}: {
  o: Own;
  set: (p: Partial<Own>) => void;
  update: boolean;
  errors: Record<string, string>;
  general: string;
  onSubmit: (e: FormEvent) => void;
}) {
  const uid = useId();
  const toggle = (p: string) => set({ permissions: o.permissions.includes(p) ? o.permissions.filter((x) => x !== p) : [...o.permissions, p] });
  const trustNote = `${uid}-trust-note`;
  return (
    <form id="own-addon" onSubmit={onSubmit} noValidate className="flex flex-col gap-4">
      <div className="banner warn items-start" role="note">
        <ShieldAlert size={16} className="mt-0.5 shrink-0" aria-hidden />
        <span className="min-w-0 flex-1">{t("market.ownWarn")}</span>
      </div>
      {general ? (
        <div className="banner err" role="alert">
          {general}
        </div>
      ) : null}
      {update ? null : (
        <Field label={t("market.ownSource")} hint={t("market.ownSourceHint")}>
          <Segmented
            label={t("market.ownSource")}
            value={o.source}
            onChange={(source) => set({ source })}
            options={[
              { value: "custom", label: t("market.ownSourceCustom") },
              { value: "external", label: t("market.ownSourceExternal") },
            ]}
          />
        </Field>
      )}
      <Field label={t("market.ownId")} htmlFor={`${uid}-id`} hint={t("market.ownIdHint")} error={errors.id}>
        <input
          id={`${uid}-id`}
          className="input mono"
          value={o.id}
          maxLength={32}
          readOnly={update}
          autoComplete="off"
          spellCheck={false}
          aria-invalid={!!errors.id}
          onChange={(e) => set({ id: e.target.value.trim() })}
        />
      </Field>
      {o.source === "custom" ? (
        <>
          <Field label={t("market.ownImage")} htmlFor={`${uid}-image`} hint={t("market.ownImageHint")} error={errors.image}>
            <input
              id={`${uid}-image`}
              className="input mono"
              value={o.image}
              maxLength={255}
              placeholder="ghcr.io/owner/name"
              autoComplete="off"
              spellCheck={false}
              aria-invalid={!!errors.image}
              onChange={(e) => set({ image: e.target.value.trim() })}
            />
          </Field>
          <Field label={t("market.ownDigest")} htmlFor={`${uid}-digest`} hint={t("market.ownDigestHint")} error={errors.digest}>
            <input
              id={`${uid}-digest`}
              className="input mono"
              value={o.digest}
              maxLength={71}
              placeholder="sha256:…"
              autoComplete="off"
              spellCheck={false}
              aria-invalid={!!errors.digest}
              onChange={(e) => set({ digest: e.target.value.trim() })}
            />
          </Field>
        </>
      ) : (
        <Field label={t("market.ownUrl")} htmlFor={`${uid}-url`} hint={t("market.ownUrlHint")} error={errors.url}>
          <input
            id={`${uid}-url`}
            className="input mono"
            value={o.url}
            maxLength={64}
            placeholder="http://127.0.0.1:8080"
            autoComplete="off"
            spellCheck={false}
            aria-invalid={!!errors.url}
            onChange={(e) => set({ url: e.target.value.trim() })}
          />
        </Field>
      )}
      <Field label={t("market.ownKind")}>
        <Segmented
          label={t("market.ownKind")}
          value={o.kind}
          onChange={(kind) => set({ kind })}
          options={(["bot", "ui", "generic"] as const).map((k) => ({ value: k, label: t(`market.kind.${k}`) }))}
        />
      </Field>
      <Field label={t("market.ownMode")} hint={t("market.ownModeHint")}>
        <Segmented
          label={t("market.ownMode")}
          value={o.mode}
          onChange={(mode) => set({ mode })}
          options={(["http", "client"] as const).map((m) => ({ value: m, label: t(`market.mode.${m}`) }))}
        />
      </Field>
      <Field label={t("market.ownPerms")} hint={t("market.ownPermsHint")} error={errors.permissions}>
        <div className="flex flex-col gap-3">
          {PERMISSIONS.map((p) => (
            <label key={p.id} className="flex items-start gap-2 text-[13px]">
              <input type="checkbox" className="check mt-0.5 shrink-0" checked={o.permissions.includes(p.id)} onChange={() => toggle(p.id)} />
              <span className="min-w-0">
                <span className="font-semibold">{t(p.label)}</span> <span className="mono text-xs text-[var(--ink-500)]">{p.id}</span>
                <span className="mt-0.5 block text-xs text-[var(--ink-500)]">{t(p.text)}</span>
              </span>
            </label>
          ))}
        </div>
      </Field>
      {actsAsAdmin(o.permissions) ? <AdminWarning /> : null}
      {o.source === "custom" ? (
        <Field label={t("market.ownMem")} htmlFor={`${uid}-mem`} hint={t("market.ownMemHint")} error={errors.mem_mb}>
          <input
            id={`${uid}-mem`}
            className="input num max-w-[160px]"
            inputMode="numeric"
            value={o.mem}
            maxLength={3}
            placeholder="128"
            aria-invalid={!!errors.mem_mb}
            onChange={(e) => set({ mem: e.target.value.replace(/\D/g, "") })}
          />
        </Field>
      ) : null}
      <div className="field">
        <label className="flex items-start gap-2 text-[13px] font-semibold">
          <input
            type="checkbox"
            className="check mt-0.5 shrink-0"
            checked={o.confirm}
            aria-invalid={!!errors.confirm}
            aria-describedby={trustNote}
            onChange={(e) => set({ confirm: e.target.checked })}
          />
          <span>{t("market.ownTrust")}</span>
        </label>
        {errors.confirm ? (
          <span className="err" role="alert" id={trustNote}>
            {errors.confirm}
          </span>
        ) : (
          <span className="hint" id={trustNote}>
            {t("market.ownTrustHint")}
          </span>
        )}
      </div>
    </form>
  );
}
