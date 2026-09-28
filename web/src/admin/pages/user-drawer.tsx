import * as Menu from "@radix-ui/react-dropdown-menu";
import { CalendarPlus, Copy, ExternalLink, Laptop, MoreHorizontal, Power, RefreshCw, RotateCcw, Smartphone, Trash2 } from "lucide-react";
import { useEffect, useState } from "react";
import { errorText, type User } from "../../api/client";
import { useDevices, useInbounds, userActions, useTariffs, useUser, useUserMutation, useUserTraffic } from "../../api/hooks";
import { Confirm, Drawer } from "../../components/overlay";
import { useToast } from "../../components/toast";
import { Avatar, Button, ErrorState, QR, Ring, Skeleton, StatePill, Switch } from "../../components/ui";
import { ago, bytes, dateLong, dateShort, days, expiryText, maskIP } from "../../lib/format";
import { tariffSummary } from "./tariffs";

export function UserDrawer({ id, onClose }: { id?: number; onClose: () => void }) {
  const user = useUser(id);
  const u = user.data;
  return (
    <Drawer
      open={!!id}
      onOpenChange={(v) => !v && onClose()}
      title={u?.name ?? "Пользователь"}
      lead={u ? <Avatar name={u.name} seed={u.id} size="lg" /> : undefined}
      meta={
        u ? (
          <>
            <StatePill state={u.state} />
            <span>создан {dateShort(u.created_at)}</span>
            <span className="mono">#{u.id}</span>
            {u.online ? <span className="text-[var(--leaf-700)]">онлайн</span> : u.online_at ? <span>был {ago(u.online_at)}</span> : null}
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
        <Button variant="primary" loading={extend.isPending} onClick={() => extend.mutate({ id: u.id, days: 30 }, { onSuccess: (r) => toast.ok(`Продлено до ${dateShort(r.expires_at!)}`), onError: fail })}>
          <CalendarPlus size={18} aria-hidden /> Продлить на 30 дней
        </Button>
        <Button loading={reset.isPending} onClick={() => reset.mutate(u.id, { onSuccess: () => toast.ok("Трафик сброшен"), onError: fail })}>
          <RotateCcw size={18} aria-hidden /> Сбросить трафик
        </Button>
        <Button
          loading={update.isPending && update.variables?.body.disabled !== undefined}
          onClick={() =>
            update.mutate(
              { id: u.id, body: { disabled: !disabled } },
              { onSuccess: () => toast.ok(disabled ? `${u.name} снова может подключаться` : `${u.name} отключён — соединения закрыты`), onError: fail },
            )
          }
        >
          <Power size={18} aria-hidden /> {disabled ? "Включить" : "Отключить"}
        </Button>
        <Menu.Root>
          <Menu.Trigger asChild>
            <button type="button" className="icon-btn h-10 w-10" aria-label="Ещё действия">
              <MoreHorizontal size={18} />
            </button>
          </Menu.Trigger>
          <Menu.Portal>
            <Menu.Content className="menu glass-strong" align="end" sideOffset={6}>
              <Menu.Item className="menu-item" onSelect={() => setConfirm("reissue")}>
                <RefreshCw size={16} aria-hidden /> Перевыпустить ссылку
              </Menu.Item>
              <Menu.Separator className="menu-sep" />
              <Menu.Item className="menu-item danger" onSelect={() => setConfirm("delete")}>
                <Trash2 size={16} aria-hidden /> Удалить пользователя
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
        title="Перевыпустить ссылку?"
        text="Старая ссылка и все устройства на ней перестанут работать сразу. Новую ссылку нужно будет отправить пользователю."
        confirm="Перевыпустить"
        loading={reissue.isPending}
        onConfirm={() =>
          reissue.mutate(u.id, {
            onSuccess: () => {
              setConfirm(null);
              toast.ok("Готово: старая ссылка больше не работает");
            },
            onError: fail,
          })
        }
      />
      <Confirm
        open={confirm === "delete"}
        onOpenChange={(v) => !v && setConfirm(null)}
        title={`Удалить ${u.name}?`}
        text="Ссылка перестанет работать сразу, история трафика будет удалена. Отменить это нельзя."
        confirm="Удалить"
        danger
        loading={remove.isPending}
        onConfirm={() =>
          remove.mutate(u.id, {
            onSuccess: () => {
              setConfirm(null);
              toast.ok(`${u.name} удалён`);
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
  const t = tariffs.data?.find((x) => x.id === u.tariff_id);
  const next = tariffs.data?.find((x) => x.id === pick);
  return (
    <Section title="Тариф">
      <div className="panel-soft flex items-center justify-between gap-3 p-4">
        <div className="min-w-0">
          <div className="font-display text-base font-medium tracking-tight">{t?.name ?? "Свои условия"}</div>
          <div className="mt-0.5 text-xs text-[var(--ink-500)]">
            {u.traffic_limit != null ? bytes(u.traffic_limit) : "без лимита трафика"} · {u.device_limit != null ? `${u.device_limit} устр.` : "без лимита устройств"}
            {t?.price_label ? ` · ${t.price_label}` : ""}
          </div>
        </div>
        <Menu.Root>
          <Menu.Trigger asChild>
            <Button variant="ghost" size="sm">
              Сменить
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
        title={`Перевести на «${next?.name ?? ""}»?`}
        text="Лимиты возьмутся из тарифа, срок начнётся заново от сегодня. Израсходованный трафик сохранится."
        confirm="Перевести"
        loading={update.isPending}
        onConfirm={() =>
          update.mutate(
            { id: u.id, body: { tariff_id: pick! } },
            {
              onSuccess: () => {
                setPick(null);
                toast.ok("Тариф изменён");
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
    <Section title="Трафик" aside={u.resets_at ? `сбросится ${dateShort(u.resets_at)}` : "без сброса"}>
      <div className="grid grid-cols-1 items-center gap-5 sm:grid-cols-[112px_minmax(0,1fr)]">
        <Ring pct={u.traffic_limit != null ? pct : 100} label={u.traffic_limit != null ? `${Math.min(999, Math.round(pct))}%` : "∞"} sub={u.traffic_limit != null ? `из ${bytes(u.traffic_limit)}` : "без лимита"} />
        <div>
          <div className="grid grid-cols-2 gap-x-4 gap-y-3 text-xs text-[var(--ink-500)]">
            <Stat label="Израсходовано" value={bytes(used)} />
            <Stat label="Осталось" value={u.traffic_limit != null ? bytes(Math.max(0, u.traffic_limit - used)) : "∞"} />
            <Stat label="Скачано" value={bytes(u.used_down)} />
            <Stat label="Отдано" value={bytes(u.used_up)} />
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
                <span>сегодня</span>
              </div>
            </>
          ) : null}
        </div>
      </div>
      <div className="mt-3 text-xs text-[var(--ink-500)]">За всё время: {bytes(u.total_up + u.total_down)}</div>
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
    <Section title="Срок действия">
      <div className="font-display text-lg font-medium tracking-tight">{u.expires_at ? `до ${dateLong(u.expires_at)}` : "Бессрочно"}</div>
      {u.expires_at ? <div className={`exp-days ${e.tone}`}>{e.tone === "bad" ? e.text : `осталось ${e.text}`}</div> : null}
      <div className="mt-3 flex flex-wrap gap-2">
        {[7, 30, 90, 365].map((n) => (
          <button key={n} type="button" className="chip-btn" disabled={extend.isPending} onClick={() => extend.mutate({ id: u.id, days: n }, { onSuccess: () => toast.ok(`+${days(n)}`), onError: (x) => toast.error(errorText(x)) })}>
            +{n === 365 ? "год" : days(n)}
          </button>
        ))}
        {u.expires_at ? (
          <button type="button" className="chip-btn" disabled={update.isPending} onClick={() => update.mutate({ id: u.id, body: { never_expires: true } }, { onSuccess: () => toast.ok("Теперь бессрочно") })}>
            Бессрочно
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
      toast.ok("Ссылка скопирована");
    } catch {
      toast.error("Браузер не дал скопировать — выделите ссылку вручную");
    }
  };
  if (!u.sub_url) {
    return (
      <Section title="Подписка">
        <p className="text-[13px] text-[var(--ink-500)]">Укажите адрес сервера в настройках, чтобы появилась ссылка.</p>
      </Section>
    );
  }
  return (
    <Section title="Подписка">
      <div className="link-field">
        <span className="mono" title={u.sub_url}>
          {u.sub_url}
        </span>
        <button type="button" className="icon-btn" onClick={copy} aria-label="Скопировать ссылку">
          <Copy size={18} />
        </button>
      </div>
      <div className="mt-4 grid grid-cols-1 items-start gap-4 sm:grid-cols-[136px_minmax(0,1fr)]">
        <QR value={u.sub_url} />
        <div className="flex flex-col items-start gap-2">
          <p className="mb-1 text-[13px] text-[var(--ink-500)]">Отправьте ссылку клиенту или дайте отсканировать QR-код в приложении.</p>
          <a className="btn btn-glass btn-sm" href={u.sub_url} target="_blank" rel="noreferrer noopener">
            Страница подписки <ExternalLink size={14} aria-hidden />
          </a>
          <Button size="sm" variant="danger" onClick={onReissue}>
            <RefreshCw size={16} aria-hidden /> Перевыпустить ссылку
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
    <Section title="Устройства" aside={`${u.online_ips.length} онлайн · лимит ${u.device_limit ?? "∞"}`}>
      <div className="mb-3 flex items-center gap-2 text-[13px]">
        <span className="text-[var(--ink-600)]">Лимит устройств:</span>
        <div className="seg" role="group" aria-label="Лимит устройств">
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
        <p className="text-[13px] text-[var(--ink-500)]">Ещё ни одного подключения. Устройство появится здесь после первого входа.</p>
      ) : (
        <ul className="flex flex-col gap-2">
          {list.slice(0, 8).map((d) => (
            <li key={d.ip} className="panel-soft grid grid-cols-[36px_minmax(0,1fr)] items-center gap-3 p-2">
              <span className="grid h-9 w-9 place-items-center rounded-[10px] bg-[var(--hover)] text-[var(--ink-600)]">{d.client.toLowerCase().includes("windows") ? <Laptop size={18} /> : <Smartphone size={18} />}</span>
              <div className="min-w-0">
                <div className="text-[13px] font-medium">{d.client || "Устройство"}</div>
                <div className="text-xs text-[var(--ink-500)]">
                  <span className="mono">{maskIP(d.ip)}</span> · {d.online ? <span className="text-[var(--leaf-700)]">сейчас онлайн</span> : ago(d.last_seen)}
                </div>
              </div>
            </li>
          ))}
        </ul>
      )}
      <p className="mt-2 text-xs text-[var(--ink-500)]">Устройство — это IP-адрес с активным подключением. Место освобождается через минуту после отключения.</p>
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
      toast.error("Нужен хотя бы один протокол — иначе лучше отключить пользователя");
      return;
    }
    const ids = next.size === all.length ? [] : [...next];
    update.mutate({ id: u.id, body: { inbounds: ids } }, { onError: (e) => toast.error(errorText(e)) });
  };
  return (
    <Section title="Протоколы">
      {all.map((i) => (
        <div key={i.id} className="flex items-center justify-between gap-3 py-2">
          <div>
            <div className="text-[13px] font-medium">{i.title}</div>
            <div className="text-xs text-[var(--ink-500)]">
              {i.port}/{i.network}
            </div>
          </div>
          <Switch checked={allowed.has(i.id)} onChange={(v) => toggle(i.id, v)} label={i.title} disabled={update.isPending} />
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
    <Section title="Заметка" aside={update.isPending ? "сохраняем…" : undefined}>
      <textarea
        className="input"
        aria-label="Заметка о пользователе"
        placeholder="Например: оплата в Telegram, продлевать 1-го числа"
        maxLength={2000}
        value={note}
        onChange={(e) => setNote(e.target.value)}
        onBlur={() => {
          if (note !== u.note) update.mutate({ id: u.id, body: { note } }, { onSuccess: () => toast.ok("Заметка сохранена"), onError: (e) => toast.error(errorText(e)) });
        }}
      />
    </Section>
  );
}
