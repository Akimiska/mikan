import * as Menu from "@radix-ui/react-dropdown-menu";
import { CalendarPlus, Copy, ExternalLink, Laptop, MoreHorizontal, Power, RefreshCw, RotateCcw, Smartphone, Trash2 } from "lucide-react";
import { useEffect, useState } from "react";
import { errorText, type User } from "../../api/client";
import { useDevices, useInbounds, userActions, useTariffs, useUser, useUserMutation, useUserTraffic } from "../../api/hooks";
import { Confirm, Drawer } from "../../components/overlay";
import { useToast } from "../../components/toast";
import { Avatar, Button, ErrorState, QR, Ring, Skeleton, StatePill, Switch } from "../../components/ui";
import { t } from "../../i18n";
import { ago, bytes, dateLong, dateShort, days, expiryText, maskIP } from "../../lib/format";
import { tariffSummary } from "./tariffs";

export function UserDrawer({ id, onClose }: { id?: number; onClose: () => void }) {
  const user = useUser(id);
  const u = user.data;
  return (
    <Drawer
      open={!!id}
      onOpenChange={(v) => !v && onClose()}
      title={u?.name ?? t("userDrawer.fallbackTitle")}
      lead={u ? <Avatar name={u.name} seed={u.id} size="lg" /> : undefined}
      meta={
        u ? (
          <>
            <StatePill state={u.state} />
            <span>{t("userDrawer.created", { date: dateShort(u.created_at) })}</span>
            <span className="mono">#{u.id}</span>
            {u.online ? <span className="text-[var(--leaf-700)]">{t("userDrawer.online")}</span> : u.online_at ? <span>{t("userDrawer.seen", { ago: ago(u.online_at) })}</span> : null}
          </>
        ) : undefined
      }
    >
      {user.isPending ? (
        <div className="space-y-4 pt-5">
          <Skeleton style={{ height: 40 }} />
          <Skeleton style={{ height: 120, borderRadius: 16 }} />
          <Skeleton style={{ height: 160, borderRadius: 16 }} />
        </div>
      ) : user.isError || !u ? (
        <ErrorState text={errorText(user.error)} onRetry={() => void user.refetch()} />
      ) : (
        <UserBody u={u} onDeleted={onClose} />
      )}
    </Drawer>
  );
}

function UserBody({ u, onDeleted }: { u: User; onDeleted: () => void }) {
  const toast = useToast();
  const extend = useUserMutation(userActions.extend);
  const reset = useUserMutation(userActions.reset);
  const update = useUserMutation(userActions.update);
  const reissue = useUserMutation(userActions.reissue);
  const remove = useUserMutation(userActions.remove);
  const [confirm, setConfirm] = useState<"reissue" | "delete" | null>(null);
  const fail = (e: unknown) => toast.error(errorText(e));
  const disabled = u.state === "disabled";

  return (
    <>
      <div className="flex flex-wrap gap-2 py-4">
        <Button variant="primary" loading={extend.isPending} onClick={() => extend.mutate({ id: u.id, days: 30 }, { onSuccess: (r) => toast.ok(t("userDrawer.extendedUntil", { date: dateShort(r.expires_at!) })), onError: fail })}>
          <CalendarPlus size={18} aria-hidden /> {t("userDrawer.extend30")}
        </Button>
        <Button loading={reset.isPending} onClick={() => reset.mutate(u.id, { onSuccess: () => toast.ok(t("userDrawer.trafficReset")), onError: fail })}>
          <RotateCcw size={18} aria-hidden /> {t("users.resetTraffic")}
        </Button>
        <Button
          loading={update.isPending && update.variables?.body.disabled !== undefined}
          onClick={() =>
            update.mutate(
              { id: u.id, body: { disabled: !disabled } },
              { onSuccess: () => toast.ok(disabled ? t("userDrawer.enabledToast", { name: u.name }) : t("userDrawer.disabledToast", { name: u.name })), onError: fail },
            )
          }
        >
          <Power size={18} aria-hidden /> {disabled ? t("common.enable") : t("users.disable")}
        </Button>
        <Menu.Root>
          <Menu.Trigger asChild>
            <button type="button" className="icon-btn h-10 w-10" aria-label={t("userDrawer.more")}>
              <MoreHorizontal size={18} />
            </button>
          </Menu.Trigger>
          <Menu.Portal>
            <Menu.Content className="menu glass-strong" align="end" sideOffset={6}>
              <Menu.Item className="menu-item" onSelect={() => setConfirm("reissue")}>
                <RefreshCw size={16} aria-hidden /> {t("userDrawer.reissue")}
              </Menu.Item>
              <Menu.Separator className="menu-sep" />
              <Menu.Item className="menu-item danger" onSelect={() => setConfirm("delete")}>
                <Trash2 size={16} aria-hidden /> {t("userDrawer.delete")}
              </Menu.Item>
            </Menu.Content>
          </Menu.Portal>
        </Menu.Root>
      </div>

      <TariffSection u={u} />
      <TrafficSection u={u} />
      <ExpirySection u={u} />
      <SubscriptionSection u={u} onReissue={() => setConfirm("reissue")} />
      <DevicesSection u={u} />
      <ProtocolsSection u={u} />
      <NoteSection u={u} />

      <Confirm
        open={confirm === "reissue"}
        onOpenChange={(v) => !v && setConfirm(null)}
        title={t("userDrawer.reissueTitle")}
        text={t("userDrawer.reissueText")}
        confirm={t("userDrawer.reissueConfirm")}
        loading={reissue.isPending}
        onConfirm={() =>
          reissue.mutate(u.id, {
            onSuccess: () => {
              setConfirm(null);
              toast.ok(t("userDrawer.reissued"));
            },
            onError: fail,
          })
        }
      />
      <Confirm
        open={confirm === "delete"}
        onOpenChange={(v) => !v && setConfirm(null)}
        title={t("userDrawer.deleteTitle", { name: u.name })}
        text={t("userDrawer.deleteText")}
        confirm={t("common.delete")}
        danger
        loading={remove.isPending}
        onConfirm={() =>
          remove.mutate(u.id, {
            onSuccess: () => {
              setConfirm(null);
              toast.ok(t("userDrawer.deleted", { name: u.name }));
              onDeleted();
            },
            onError: fail,
          })
        }
      />
    </>
  );
}

function Section({ title, aside, children }: { title: string; aside?: React.ReactNode; children: React.ReactNode }) {
  return (
    <section className="dr-sec">
      <h3>
        {title}
        {aside ? <span className="font-normal text-[var(--ink-500)]">{aside}</span> : null}
      </h3>
      {children}
    </section>
  );
}

function TariffSection({ u }: { u: User }) {
  const tariffs = useTariffs();
  const toast = useToast();
  const update = useUserMutation(userActions.update);
  const [pick, setPick] = useState<number | null>(null);
  const current = tariffs.data?.find((x) => x.id === u.tariff_id);
  const next = tariffs.data?.find((x) => x.id === pick);
  return (
    <Section title={t("users.colTariff")}>
      <div className="panel-soft flex items-center justify-between gap-3 p-4">
        <div className="min-w-0">
          <div className="font-display text-base font-medium tracking-tight">{current?.name ?? t("userDrawer.customTerms")}</div>
          <div className="mt-0.5 text-xs text-[var(--ink-500)]">
            {u.traffic_limit != null ? bytes(u.traffic_limit) : t("userDrawer.noTrafficLimit")} · {u.device_limit != null ? t("userDrawer.devicesShort", { n: u.device_limit }) : t("userDrawer.noDeviceLimit")}
            {current?.price_label ? ` · ${current.price_label}` : ""}
          </div>
        </div>
        <Menu.Root>
          <Menu.Trigger asChild>
            <Button variant="ghost" size="sm">
              {t("userDrawer.change")}
            </Button>
          </Menu.Trigger>
          <Menu.Portal>
            <Menu.Content className="menu glass-strong" align="end" sideOffset={6}>
              {(tariffs.data ?? []).map((x) => (
                <Menu.Item key={x.id} className="menu-item" onSelect={() => setPick(x.id)}>
                  <span className="flex-1">{x.name}</span>
                  <span className="text-xs font-normal text-[var(--ink-500)]">{tariffSummary(x)}</span>
                </Menu.Item>
              ))}
            </Menu.Content>
          </Menu.Portal>
        </Menu.Root>
      </div>
      <Confirm
        open={pick !== null}
        onOpenChange={(v) => !v && setPick(null)}
        title={t("userDrawer.switchTitle", { name: next?.name ?? "" })}
        text={t("userDrawer.switchText")}
        confirm={t("userDrawer.switchConfirm")}
        loading={update.isPending}
        onConfirm={() =>
          update.mutate(
            { id: u.id, body: { tariff_id: pick! } },
            {
              onSuccess: () => {
                setPick(null);
                toast.ok(t("userDrawer.tariffChanged"));
              },
              onError: (e) => toast.error(errorText(e)),
            },
          )
        }
      />
    </Section>
  );
}

function TrafficSection({ u }: { u: User }) {
  const traffic = useUserTraffic(u.id);
  const used = u.used_up + u.used_down;
  const pct = u.traffic_limit ? (used / u.traffic_limit) * 100 : 0;
  const pts = traffic.data?.points ?? [];
  const max = Math.max(1, ...pts.map((p) => p.up + p.down));
  return (
    <Section title={t("users.colTraffic")} aside={u.resets_at ? t("userDrawer.resetsOn", { date: dateShort(u.resets_at) }) : t("userDrawer.noReset")}>
      <div className="grid grid-cols-1 items-center gap-5 sm:grid-cols-[112px_minmax(0,1fr)]">
        <Ring
          pct={u.traffic_limit != null ? pct : 100}
          label={u.traffic_limit != null ? `${Math.min(999, Math.round(pct))}%` : "∞"}
          sub={u.traffic_limit != null ? t("users.of", { total: bytes(u.traffic_limit) }) : t("users.unlimited")}
        />
        <div>
          <div className="grid grid-cols-2 gap-x-4 gap-y-3 text-xs text-[var(--ink-500)]">
            <Stat label={t("userDrawer.used")} value={bytes(used)} />
            <Stat label={t("userDrawer.left")} value={u.traffic_limit != null ? bytes(Math.max(0, u.traffic_limit - used)) : "∞"} />
            <Stat label={t("chart.down")} value={bytes(u.used_down)} />
            <Stat label={t("chart.up")} value={bytes(u.used_up)} />
          </div>
          {pts.length > 0 ? (
            <>
              <div className="mt-4 flex h-10 items-end gap-1" aria-hidden>
                {pts.slice(-14).map((p) => (
                  <i key={p.t} className="min-h-[2px] flex-1 rounded-t-[4px] rounded-b-[2px] bg-[var(--mikan-200)] last:bg-[var(--mikan-500)]" style={{ height: `${((p.up + p.down) / max) * 100}%` }} />
                ))}
              </div>
              <div className="mt-1.5 flex justify-between text-[11px] text-[var(--ink-400)]">
                <span>{dateShort(pts.slice(-14)[0]!.t)}</span>
                <span>{t("time.today")}</span>
              </div>
            </>
          ) : null}
        </div>
      </div>
      <div className="mt-3 text-xs text-[var(--ink-500)]">{t("userDrawer.allTime", { bytes: bytes(u.total_up + u.total_down) })}</div>
    </Section>
  );
}

function Stat({ label, value }: { label: string; value: string }) {
  return (
    <div>
      {label}
      <b className="num mt-0.5 block text-[15px] leading-5 font-medium text-[var(--ink-900)]">{value}</b>
    </div>
  );
}

function ExpirySection({ u }: { u: User }) {
  const toast = useToast();
  const extend = useUserMutation(userActions.extend);
  const update = useUserMutation(userActions.update);
  const e = expiryText(u.expires_at);
  return (
    <Section title={t("userDrawer.expiry")}>
      <div className="font-display text-lg font-medium tracking-tight">{u.expires_at ? t("users.until", { date: dateLong(u.expires_at) }) : t("userDrawer.forever")}</div>
      {u.expires_at ? <div className={`exp-days ${e.tone}`}>{e.tone === "bad" ? e.text : t("userDrawer.leftDays", { text: e.text })}</div> : null}
      <div className="mt-3 flex flex-wrap gap-2">
        {[7, 30, 90, 365].map((n) => (
          <button key={n} type="button" className="chip-btn" disabled={extend.isPending} onClick={() => extend.mutate({ id: u.id, days: n }, { onSuccess: () => toast.ok(`+${days(n)}`), onError: (x) => toast.error(errorText(x)) })}>
            +{n === 365 ? t("userDrawer.year") : days(n)}
          </button>
        ))}
        {u.expires_at ? (
          <button type="button" className="chip-btn" disabled={update.isPending} onClick={() => update.mutate({ id: u.id, body: { never_expires: true } }, { onSuccess: () => toast.ok(t("userDrawer.nowForever")) })}>
            {t("userDrawer.forever")}
          </button>
        ) : null}
      </div>
    </Section>
  );
}

function SubscriptionSection({ u, onReissue }: { u: User; onReissue: () => void }) {
  const toast = useToast();
  const copy = async () => {
    try {
      await navigator.clipboard.writeText(u.sub_url);
      toast.ok(t("common.linkCopied"));
    } catch {
      toast.error(t("common.copyFailed"));
    }
  };
  if (!u.sub_url) {
    return (
      <Section title={t("userDrawer.subscription")}>
        <p className="text-[13px] text-[var(--ink-500)]">{t("userDrawer.noHost")}</p>
      </Section>
    );
  }
  return (
    <Section title={t("userDrawer.subscription")}>
      <div className="link-field">
        <span className="mono" title={u.sub_url}>
          {u.sub_url}
        </span>
        <button type="button" className="icon-btn" onClick={copy} aria-label={t("common.copyLink")}>
          <Copy size={18} />
        </button>
      </div>
      <div className="mt-4 grid grid-cols-1 items-start gap-4 sm:grid-cols-[136px_minmax(0,1fr)]">
        <QR value={u.sub_url} />
        <div className="flex flex-col items-start gap-2">
          <p className="mb-1 text-[13px] text-[var(--ink-500)]">{t("userDrawer.sendHint")}</p>
          <a className="btn btn-glass btn-sm" href={u.sub_url} target="_blank" rel="noreferrer noopener">
            {t("userDrawer.subPage")} <ExternalLink size={14} aria-hidden />
          </a>
          <Button size="sm" variant="danger" onClick={onReissue}>
            <RefreshCw size={16} aria-hidden /> {t("userDrawer.reissue")}
          </Button>
        </div>
      </div>
    </Section>
  );
}

function DevicesSection({ u }: { u: User }) {
  const devices = useDevices(u.id);
  const toast = useToast();
  const update = useUserMutation(userActions.update);
  const list = devices.data ?? [];
  const setLimit = (n: number | null) =>
    update.mutate({ id: u.id, body: n === null ? { devices_unlimited: true } : { device_limit: n } }, { onError: (e) => toast.error(errorText(e)) });
  return (
    <Section title={t("userDrawer.devices")} aside={t("userDrawer.devicesAside", { online: u.online_ips.length, limit: u.device_limit ?? "∞" })}>
      <div className="mb-3 flex items-center gap-2 text-[13px]">
        <span className="text-[var(--ink-600)]">{t("userDrawer.deviceLimit")}</span>
        <div className="seg" role="group" aria-label={t("userDrawer.deviceLimit")}>
          {[1, 2, 3, 5, 10].map((n) => (
            <button key={n} type="button" aria-pressed={u.device_limit === n} onClick={() => setLimit(n)}>
              {n}
            </button>
          ))}
          <button type="button" aria-pressed={u.device_limit == null} onClick={() => setLimit(null)}>
            ∞
          </button>
        </div>
      </div>
      {list.length === 0 ? (
        <p className="text-[13px] text-[var(--ink-500)]">{t("userDrawer.noDevices")}</p>
      ) : (
        <ul className="flex flex-col gap-2">
          {list.slice(0, 8).map((d) => (
            <li key={d.ip} className="panel-soft grid grid-cols-[36px_minmax(0,1fr)] items-center gap-3 p-2">
              <span className="grid h-9 w-9 place-items-center rounded-[10px] bg-[var(--hover)] text-[var(--ink-600)]">{d.client.toLowerCase().includes("windows") ? <Laptop size={18} /> : <Smartphone size={18} />}</span>
              <div className="min-w-0">
                <div className="text-[13px] font-medium">{d.client || t("userDrawer.device")}</div>
                <div className="text-xs text-[var(--ink-500)]">
                  <span className="mono">{maskIP(d.ip)}</span> · {d.online ? <span className="text-[var(--leaf-700)]">{t("users.onlineNow")}</span> : ago(d.last_seen)}
                </div>
              </div>
            </li>
          ))}
        </ul>
      )}
      <p className="mt-2 text-xs text-[var(--ink-500)]">{t("userDrawer.devicesNote")}</p>
    </Section>
  );
}

function ProtocolsSection({ u }: { u: User }) {
  const inbounds = useInbounds();
  const update = useUserMutation(userActions.update);
  const toast = useToast();
  const all = (inbounds.data ?? []).filter((i) => i.enabled);
  const allowed = new Set(u.inbounds.length ? u.inbounds : all.map((i) => i.id));
  const toggle = (id: number, on: boolean) => {
    const next = new Set(allowed);
    if (on) next.add(id);
    else next.delete(id);
    if (next.size === 0) {
      toast.error(t("userDrawer.needOneProtocol"));
      return;
    }
    const ids = next.size === all.length ? [] : [...next];
    update.mutate({ id: u.id, body: { inbounds: ids } }, { onError: (e) => toast.error(errorText(e)) });
  };
  return (
    <Section title={t("userDrawer.protocols")}>
      {all.map((i) => (
        <div key={i.id} className="flex items-center justify-between gap-3 py-2">
          <div>
            <div className="text-[13px] font-medium">{i.sub_name}</div>
            <div className="text-xs text-[var(--ink-500)]">
              {i.title} · {i.port}/{i.network}
            </div>
          </div>
          <Switch checked={allowed.has(i.id)} onChange={(v) => toggle(i.id, v)} label={i.sub_name} disabled={update.isPending} />
        </div>
      ))}
    </Section>
  );
}

function NoteSection({ u }: { u: User }) {
  const [note, setNote] = useState(u.note);
  const update = useUserMutation(userActions.update);
  const toast = useToast();
  useEffect(() => setNote(u.note), [u.note]);
  return (
    <Section title={t("userDrawer.note")} aside={update.isPending ? t("userDrawer.saving") : undefined}>
      <textarea
        className="input"
        aria-label={t("userDrawer.noteLabel")}
        placeholder={t("userDrawer.notePlaceholder")}
        maxLength={2000}
        value={note}
        onChange={(e) => setNote(e.target.value)}
        onBlur={() => {
          if (note !== u.note) update.mutate({ id: u.id, body: { note } }, { onSuccess: () => toast.ok(t("userDrawer.noteSaved")), onError: (e) => toast.error(errorText(e)) });
        }}
      />
    </Section>
  );
}
