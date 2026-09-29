import * as Menu from "@radix-ui/react-dropdown-menu";
import { CalendarPlus, Check, Copy, ExternalLink, Laptop, Layers, MoreHorizontal, Power, RefreshCw, RotateCcw, Send, Smartphone, Trash2, Unlink } from "lucide-react";
import { useEffect, useState } from "react";
import { api, errorText, unwrap, type Schemas, type User } from "../../api/client";
import { onePeriod, useBoundDevices, useDevices, useInbounds, userActions, useSettings, useTariffs, useUser, useUserMutation, useUserTraffic } from "../../api/hooks";
import { Confirm, Drawer } from "../../components/overlay";
import { useToast } from "../../components/toast";
import { Avatar, Button, ErrorState, Field, QR, Ring, Skeleton, StatePill, Switch } from "../../components/ui";
import { t } from "../../i18n";
import { ago, appName, bytes, dateLong, dateShort, days, expiryText, fromInputDate, inputDate, maskIP, months } from "../../lib/format";
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
        <Button variant="primary" loading={extend.isPending} onClick={() => extend.mutate({ id: u.id, ...onePeriod(u) }, { onSuccess: (r) => toast.ok(t("userDrawer.extendedUntil", { date: dateShort(r.expires_at!) })), onError: fail })}>
          <CalendarPlus size={18} aria-hidden /> {u.billing_day != null ? t("userDrawer.extendMonth") : t("userDrawer.extend30")}
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
      <TelegramSection u={u} />
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
  const fail = (x: unknown) => toast.error(errorText(x));
  // With a billing day a term runs from that day to the same day: extend by months.
  const chips: { label: string; body: Schemas["ExtendInputBody"] }[] =
    u.billing_day != null
      ? [1, 3, 6, 12].map((n) => ({ label: n === 12 ? t("userDrawer.year") : months(n), body: { months: n } }))
      : [7, 30, 90, 365].map((n) => ({ label: n === 365 ? t("userDrawer.year") : days(n), body: { days: n } }));
  return (
    <Section title={t("userDrawer.expiry")} aside={u.billing_day != null ? t("userDrawer.billingAside", { d: u.billing_day }) : undefined}>
      <div className="font-display text-lg font-medium tracking-tight">{u.expires_at ? t("users.until", { date: dateLong(u.expires_at) }) : t("userDrawer.forever")}</div>
      {u.expires_at ? <div className={`exp-days ${e.tone}`}>{e.tone === "bad" ? e.text : t("userDrawer.leftDays", { text: e.text })}</div> : null}
      <div className="mt-3 flex flex-wrap gap-2">
        {chips.map((c) => (
          <button
            key={c.label}
            type="button"
            className="chip-btn"
            disabled={extend.isPending}
            onClick={() => extend.mutate({ id: u.id, ...c.body }, { onSuccess: (r) => toast.ok(t("userDrawer.extendedUntil", { date: dateShort(r.expires_at!) })), onError: fail })}
          >
            +{c.label}
          </button>
        ))}
        {u.expires_at ? (
          <button type="button" className="chip-btn" disabled={update.isPending} onClick={() => update.mutate({ id: u.id, body: { never_expires: true } }, { onSuccess: () => toast.ok(t("userDrawer.nowForever")), onError: fail })}>
            {t("userDrawer.forever")}
          </button>
        ) : null}
      </div>
      <div className="mt-4 grid grid-cols-1 gap-x-3 sm:grid-cols-2">
        <BillingDayField u={u} />
        <ExactDateField u={u} />
      </div>
    </Section>
  );
}

function BillingDayField({ u }: { u: User }) {
  const toast = useToast();
  const update = useUserMutation(userActions.update);
  const set = (d: number) =>
    update.mutate(
      { id: u.id, body: { billing_day: d } },
      { onSuccess: () => toast.ok(d ? t("userDrawer.billingDaySet", { d }) : t("userDrawer.billingDayCleared")), onError: (e) => toast.error(errorText(e)) },
    );
  return (
    <Field label={t("userDrawer.billingDay")} htmlFor={`u-bday-${u.id}`} hint={t("userDrawer.billingDayHint")}>
      <select id={`u-bday-${u.id}`} className="input" value={u.billing_day ?? 0} disabled={update.isPending} onChange={(e) => set(Number(e.target.value))}>
        <option value={0}>{t("userDrawer.billingDayNone")}</option>
        {Array.from({ length: 31 }, (_, i) => i + 1).map((d) => (
          <option key={d} value={d}>
            {t("userDrawer.billingDayOption", { d })}
          </option>
        ))}
      </select>
    </Field>
  );
}

function ExactDateField({ u }: { u: User }) {
  const toast = useToast();
  const update = useUserMutation(userActions.update);
  const current = u.expires_at ? inputDate(u.expires_at) : "";
  const [date, setDate] = useState(current);
  useEffect(() => setDate(current), [current]);
  const apply = () =>
    update.mutate(
      { id: u.id, body: { expires_at: fromInputDate(date, u.expires_at) } },
      { onSuccess: (r) => toast.ok(t("userDrawer.extendedUntil", { date: dateShort(r.expires_at!) })), onError: (e) => toast.error(errorText(e)) },
    );
  return (
    <Field label={t("userDrawer.exactDate")} htmlFor={`u-date-${u.id}`} hint={t("userDrawer.exactDateHint")}>
      <div className="flex items-center gap-2">
        <input id={`u-date-${u.id}`} type="date" className="input min-w-0" value={date} min={inputDate(new Date().toISOString())} onChange={(e) => setDate(e.target.value)} />
        {date && date !== current ? (
          <Button variant="primary" className="h-11 w-11 shrink-0 px-0" loading={update.isPending} onClick={apply} aria-label={t("common.save")} title={t("common.save")}>
            {update.isPending ? null : <Check size={18} aria-hidden />}
          </Button>
        ) : null}
      </div>
    </Field>
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

function TelegramSection({ u }: { u: User }) {
  const toast = useToast();
  const unlink = useUserMutation((id: number) => unwrap(api.DELETE("/api/v1/users/{id}/telegram", { params: { path: { id } } })));
  const [confirm, setConfirm] = useState(false);
  const tg = u.telegram;
  return (
    <Section title={t("userDrawer.telegram")}>
      {tg ? (
        <div className="panel-soft flex items-center gap-3 p-3">
          <span className="grid h-9 w-9 place-items-center rounded-[10px] bg-[var(--hover)] text-[var(--ink-600)]" aria-hidden>
            <Send size={18} />
          </span>
          <div className="min-w-0 flex-1">
            <div className="truncate text-[13px] font-medium">{tg.username ? `@${tg.username}` : tg.name || tg.id}</div>
            {tg.username && tg.name ? <div className="truncate text-xs text-[var(--ink-500)]">{tg.name}</div> : null}
          </div>
          <Button size="sm" variant="danger" onClick={() => setConfirm(true)}>
            {t("userDrawer.telegramUnlink")}
          </Button>
        </div>
      ) : (
        <p className="text-[13px] text-[var(--ink-500)]">{t("userDrawer.telegramNone")}</p>
      )}
      <Confirm
        open={confirm}
        onOpenChange={setConfirm}
        title={t("userDrawer.telegramUnlinkTitle")}
        text={t("userDrawer.telegramUnlinkText")}
        confirm={t("userDrawer.telegramUnlink")}
        danger
        loading={unlink.isPending}
        onConfirm={() =>
          unlink.mutate(u.id, {
            onSuccess: () => {
              setConfirm(false);
              toast.ok(t("userDrawer.telegramUnlinked"));
            },
            onError: (e) => toast.error(errorText(e)),
          })
        }
      />
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
      <div className="mb-4 flex items-center gap-2 text-[13px]">
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
      <BoundDevices u={u} />
      <h4 className="mt-5 mb-2 text-xs font-medium text-[var(--ink-500)]">{t("userDrawer.addresses")}</h4>
      {devices.isPending ? (
        <Skeleton style={{ height: 52, borderRadius: 16 }} />
      ) : list.length === 0 ? (
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

type BoundDevice = Schemas["BoundDeviceView"];

const desktopOS = /windows|mac|linux|darwin/i;

/** What to call a bound device: its model, else its system, else the app. */
function deviceName(d: BoundDevice): string {
  if (!d.hwid) return t("userDrawer.sharedPlace");
  return d.model || [d.os, d.os_version].filter(Boolean).join(" ") || appName(d.app) || t("userDrawer.device");
}

/** The line under a device's name: its system (when the name is the model), app and last visit. */
function deviceMeta(d: BoundDevice): string {
  const system = d.hwid && d.model ? [d.os, d.os_version].filter(Boolean).join(" ") : "";
  return [system, appName(d.app)].filter(Boolean).join(" · ");
}

function BoundDevices({ u }: { u: User }) {
  const settings = useSettings();
  const bound = useBoundDevices(u.id);
  const unbind = useUserMutation(userActions.unbindDevice);
  const toast = useToast();
  const [pick, setPick] = useState<BoundDevice | null>(null);
  const list = bound.data ?? [];
  if (!settings.data?.device_binding && list.length === 0) return null;
  return (
    <>
      <h4 className="mb-2 flex justify-between gap-2 text-xs font-medium text-[var(--ink-500)]">
        {t("userDrawer.boundTitle")}
        {bound.data ? <span className="num">{u.device_limit != null ? t("userDrawer.boundCount", { n: list.length, limit: u.device_limit }) : list.length}</span> : null}
      </h4>
      {bound.isPending ? (
        <Skeleton style={{ height: 52, borderRadius: 16 }} />
      ) : bound.isError ? (
        <ErrorState text={errorText(bound.error)} onRetry={() => void bound.refetch()} />
      ) : list.length === 0 ? (
        <p className="text-[13px] text-[var(--ink-500)]">{t("userDrawer.boundEmpty")}</p>
      ) : (
        <ul className="flex flex-col gap-2">
          {list.map((d) => {
            const meta = deviceMeta(d);
            return (
              <li key={d.id} className="panel-soft grid grid-cols-[36px_minmax(0,1fr)_auto] items-center gap-3 p-2">
                <span className="grid h-9 w-9 place-items-center rounded-[10px] bg-[var(--hover)] text-[var(--ink-600)]" aria-hidden>
                  {!d.hwid ? <Layers size={18} /> : desktopOS.test(d.os) ? <Laptop size={18} /> : <Smartphone size={18} />}
                </span>
                <div className="min-w-0">
                  <div className="truncate text-[13px] font-medium">{deviceName(d)}</div>
                  <div className="truncate text-xs text-[var(--ink-500)]">
                    {meta ? `${meta} · ` : ""}
                    {d.online ? <span className="text-[var(--leaf-700)]">{t("users.onlineNow")}</span> : ago(d.last_seen)}
                  </div>
                </div>
                <button type="button" className="icon-btn" aria-label={t("userDrawer.unbindLabel", { name: deviceName(d) })} title={t("userDrawer.unbind")} onClick={() => setPick(d)}>
                  <Unlink size={16} />
                </button>
              </li>
            );
          })}
        </ul>
      )}
      <p className="mt-2 text-xs text-[var(--ink-500)]">{settings.data?.device_require_hwid ? t("userDrawer.boundNoteStrict") : t("userDrawer.boundNote")}</p>
      <Confirm
        open={pick !== null}
        onOpenChange={(v) => !v && setPick(null)}
        title={t("userDrawer.unbindTitle", { name: pick ? deviceName(pick) : "" })}
        text={pick && !pick.hwid ? t("userDrawer.unbindSharedText") : t("userDrawer.unbindText")}
        confirm={t("userDrawer.unbind")}
        danger
        loading={unbind.isPending}
        onConfirm={() =>
          pick &&
          unbind.mutate(
            { id: u.id, device: pick.id },
            {
              onSuccess: () => {
                toast.ok(t("userDrawer.unbound", { name: deviceName(pick) }));
                setPick(null);
              },
              onError: (e) => toast.error(errorText(e)),
            },
          )
        }
      />
    </>
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
