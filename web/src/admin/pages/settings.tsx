import { useMutation, useQuery, useQueryClient } from "@tanstack/react-query";
import { Link } from "@tanstack/react-router";
import clsx from "clsx";
import { ChevronRight, Copy, KeyRound, LogOut, RefreshCw, ShieldCheck } from "lucide-react";
import { useEffect, useState, type FormEvent } from "react";
import { api, ApiError, errorText, unwrap, type Schemas } from "../../api/client";
import { meQuery, qk, useInbounds, usePaymentSettings, useSettings, useUpdates } from "../../api/hooks";
import { LangSwitch } from "../../components/lang";
import { Confirm } from "../../components/overlay";
import { useToast } from "../../components/toast";
import { Button, ErrorState, Field, PageHeader, Pill, QR, Skeleton, Switch } from "../../components/ui";
import { getLocale, LOCALES, t, tMaybe } from "../../i18n";
import { FingerprintSelect } from "../../components/fingerprint-select";
import { ago } from "../../lib/format";

export function SettingsPage() {
  const settings = useSettings();
  return (
    <>
      <PageHeader title={t("nav.settings")} sub={t("settings.subtitle")} actions={<LangSwitch />} />
      {settings.isPending ? (
        <Skeleton style={{ height: 320, borderRadius: 20 }} />
      ) : settings.isError ? (
        <section className="card glass">
          <ErrorState text={errorText(settings.error)} onRetry={() => void settings.refetch()} />
        </section>
      ) : (
        <div className="grid items-start gap-4 xl:grid-cols-2">
          <div className="flex min-w-0 flex-col gap-4">
            <ServerCard s={settings.data} />
            <SubscriptionCard s={settings.data} />
            <LanguageCard s={settings.data} />
            <AutoCard s={settings.data} />
            <DevicesCard s={settings.data} />
            <SalesCard />
            <ApiCard />
          </div>
          <div className="flex min-w-0 flex-col gap-4">
            <UpdatesCard />
            <AccessCard s={settings.data} />
            <CertificateCard s={settings.data} />
            <PasswordCard />
            <TwoFactorCard />
            <SessionsCard />
          </div>
        </div>
      )}
    </>
  );
}

function useSaveSettings() {
  const qc = useQueryClient();
  const toast = useToast();
  return useMutation({
    mutationFn: (body: Schemas["PatchSettingsInputBody"]) => unwrap(api.PATCH("/api/v1/settings", { body })),
    onSuccess: (data) => {
      qc.setQueryData(qk.settings, data);
      void qc.invalidateQueries({ queryKey: qk.users });
      void qc.invalidateQueries({ queryKey: qk.inbounds });
      toast.ok(t("settings.saved"));
    },
    onError: (e) => {
      if (!(e instanceof ApiError && Object.keys(e.fields).length)) toast.error(errorText(e));
    },
  });
}

function ServerCard({ s }: { s: Schemas["SettingsView"] }) {
  const save = useSaveSettings();
  const init = () => ({ public_host: s.public_host, domain: s.domain, quiet_hour_utc: String(s.quiet_hour_utc) });
  const [form, setForm] = useState(init);
  useEffect(() => setForm(init()), [s]);
  const errors = save.error instanceof ApiError ? save.error.fields : {};
  const submit = (e: FormEvent) => {
    e.preventDefault();
    save.mutate({ public_host: form.public_host, domain: form.domain, quiet_hour_utc: Number(form.quiet_hour_utc) });
  };
  const set = (k: keyof typeof form) => (e: React.ChangeEvent<HTMLInputElement>) => setForm((f) => ({ ...f, [k]: e.target.value }));
  return (
    <section className="card glass reveal">
      <form onSubmit={submit} noValidate>
        <div className="card-head">
          <h2 className="card-title">{t("settings.server")}</h2>
        </div>
        <Field label={t("settings.host")} htmlFor="s-host" hint={t("settings.hostHint")} error={errors.public_host}>
          <input id="s-host" className="input mono" value={form.public_host} onChange={set("public_host")} aria-invalid={!!errors.public_host} />
        </Field>
        <Field label={t("settings.domain")} htmlFor="s-domain" hint={t("settings.domainHint")} error={errors.domain}>
          <input id="s-domain" className="input mono" value={form.domain} onChange={set("domain")} placeholder="vpn.example.com" aria-invalid={!!errors.domain} />
        </Field>
        <Field label={t("settings.quietHour")} htmlFor="s-quiet" hint={t("settings.quietHourHint")}>
          <input id="s-quiet" className="input max-w-[100px]" inputMode="numeric" value={form.quiet_hour_utc} onChange={set("quiet_hour_utc")} />
        </Field>
        <Button type="submit" variant="primary" loading={save.isPending}>
          {t("common.save")}
        </Button>
      </form>
    </section>
  );
}

const routingModes = [
  { id: "ru_direct", title: "settings.routingRuDirect", sub: "settings.routingRuDirectSub" },
  { id: "all", title: "settings.routingAll", sub: "settings.routingAllSub" },
] as const;

function SubscriptionCard({ s }: { s: Schemas["SettingsView"] }) {
  const save = useSaveSettings();
  const inbounds = useInbounds();
  const init = () => ({ brand: s.brand, support_url: s.support_url, sub_group_main: s.sub_group_main, sub_group_auto: s.sub_group_auto, sub_routing: s.sub_routing, client_fingerprint: s.client_fingerprint });
  const [form, setForm] = useState(init);
  const [fpOk, setFpOk] = useState(true);
  useEffect(() => setForm(init()), [s]);
  const errors = save.error instanceof ApiError ? save.error.fields : {};
  const submit = (e: FormEvent) => {
    e.preventDefault();
    save.mutate({ brand: form.brand, support_url: form.support_url, sub_group_main: form.sub_group_main.trim(), sub_group_auto: form.sub_group_auto.trim(), sub_routing: form.sub_routing, client_fingerprint: form.client_fingerprint });
  };
  const set = (k: keyof typeof form) => (e: React.ChangeEvent<HTMLInputElement>) => setForm((f) => ({ ...f, [k]: e.target.value }));
  const proxies = (inbounds.data ?? []).filter((i) => i.enabled).map((i) => i.sub_name);
  return (
    <section className="card glass reveal" style={{ "--i": 1 } as React.CSSProperties}>
      <form onSubmit={submit} noValidate>
        <div className="card-head">
          <div>
            <h2 className="card-title">{t("settings.subscription")}</h2>
            <div className="card-sub">{t("settings.subscriptionSub")}</div>
          </div>
        </div>
        <Field label={t("settings.brand")} htmlFor="s-brand" hint={t("settings.brandHint")}>
          <input id="s-brand" className="input" value={form.brand} onChange={set("brand")} maxLength={40} />
        </Field>
        <div className="grid gap-x-3 sm:grid-cols-2">
          <Field label={t("settings.groupMain")} htmlFor="s-gmain" hint={t("settings.groupMainHint")} error={errors.sub_group_main}>
            <input id="s-gmain" className="input" value={form.sub_group_main} onChange={set("sub_group_main")} maxLength={48} aria-invalid={!!errors.sub_group_main} autoComplete="off" />
          </Field>
          <Field label={t("settings.groupAuto")} htmlFor="s-gauto" hint={t("settings.groupAutoHint")} error={errors.sub_group_auto}>
            <input id="s-gauto" className="input" value={form.sub_group_auto} onChange={set("sub_group_auto")} maxLength={48} aria-invalid={!!errors.sub_group_auto} autoComplete="off" />
          </Field>
        </div>
        <div className="panel-soft mb-4 p-3" aria-label={t("settings.preview")}>
          <div className="mb-2 text-xs text-[var(--ink-500)]">{t("settings.previewHint")}</div>
          <div className="flex items-center gap-2">
            <b className="truncate text-[13px]">{form.sub_group_main || "—"}</b>
            <span className="rounded-md border border-[var(--hairline)] px-1.5 py-0.5 text-[10px] font-semibold tracking-wide text-[var(--ink-500)]">SELECTOR</span>
          </div>
          <div className="mt-2 flex flex-wrap gap-1.5">
            <span className="tag inline-flex items-center gap-1">
              {form.sub_group_auto || "—"}
              <span className="text-[10px] font-semibold tracking-wide text-[var(--ink-400)]">URLTEST</span>
            </span>
            {proxies.map((p) => (
              <span key={p} className="tag">
                {p}
              </span>
            ))}
          </div>
        </div>
        <Field label={t("settings.routing")} hint={t("settings.routingHint")}>
          <div className="grid gap-2 sm:grid-cols-2" role="radiogroup" aria-label={t("settings.routing")}>
            {routingModes.map((m) => (
              <button key={m.id} type="button" role="radio" aria-checked={form.sub_routing === m.id} className="opt" onClick={() => setForm((f) => ({ ...f, sub_routing: m.id }))}>
                <span className="font-semibold">{t(m.title)}</span>
                <span className="text-xs text-[var(--ink-500)]">{t(m.sub)}</span>
              </button>
            ))}
          </div>
        </Field>
        <Field label={t("settings.fingerprint")} htmlFor="s-fp" hint={t("settings.fingerprintHint")} error={errors.client_fingerprint}>
          <FingerprintSelect key={s.client_fingerprint} id="s-fp" value={form.client_fingerprint} onChange={(v) => setForm((f) => ({ ...f, client_fingerprint: v }))} invalid={!!errors.client_fingerprint} onValid={setFpOk} />
        </Field>
        <Field label={t("settings.support")} htmlFor="s-support" hint={t("settings.supportHint")} error={errors.support_url}>
          <input id="s-support" className="input" value={form.support_url} onChange={set("support_url")} placeholder="https://t.me/your_support" aria-invalid={!!errors.support_url} />
        </Field>
        <Button type="submit" variant="primary" loading={save.isPending} disabled={!fpOk || !form.client_fingerprint}>
          {t("common.save")}
        </Button>
      </form>
    </section>
  );
}

/** What visitors get until they pick a language; the header's switch is this browser's own. */
function LanguageCard({ s }: { s: Schemas["SettingsView"] }) {
  const save = useSaveSettings();
  const options = [
    { id: "auto", label: t("settings.langAuto") },
    ...LOCALES.map((l) => ({ id: l.id, label: l.label, lang: l.id })),
  ] as const;
  const current = (save.isPending && save.variables.default_lang) || s.default_lang;
  return (
    <section className="card glass reveal" style={{ "--i": 2 } as React.CSSProperties}>
      <div className="card-head">
        <div>
          <h2 className="card-title">{t("settings.lang")}</h2>
          <div className="card-sub">{t("settings.langSub")}</div>
        </div>
      </div>
      <div className="grid gap-2 sm:grid-cols-3" role="radiogroup" aria-label={t("settings.lang")} aria-busy={save.isPending}>
        {options.map((o) => (
          <button
            key={o.id}
            type="button"
            role="radio"
            aria-checked={current === o.id}
            className="opt"
            lang={"lang" in o ? o.lang : undefined}
            disabled={save.isPending}
            onClick={() => o.id !== s.default_lang && save.mutate({ default_lang: o.id })}
          >
            <span className="font-semibold">{o.label}</span>
          </button>
        ))}
      </div>
      <p className="mt-3 text-xs text-[var(--ink-500)]">{t("settings.langNote")}</p>
    </section>
  );
}

/** Global switches of the automatic fixes; each connection can opt out in its settings. */
function AutoCard({ s }: { s: Schemas["SettingsView"] }) {
  const save = useSaveSettings();
  const rows = [
    { key: "auto_port", title: t("settings.autoPort"), sub: t("settings.autoPortSub"), on: s.auto_port },
    { key: "auto_sni", title: t("settings.autoSni"), sub: t("settings.autoSniSub"), on: s.auto_sni },
  ] as const;
  return (
    <section className="card glass reveal" style={{ "--i": 3 } as React.CSSProperties}>
      <div className="card-head">
        <div>
          <h2 className="card-title">{t("settings.auto")}</h2>
          <div className="card-sub">{t("settings.autoSub")}</div>
        </div>
      </div>
      <ul className="row-list">
        {rows.map((r) => (
          <li key={r.key} className="flex items-start justify-between gap-4 py-3">
            <div className="min-w-0">
              <div className="text-[13px] font-medium">{r.title}</div>
              <div className="mt-1 text-xs text-[var(--ink-500)]">{r.sub}</div>
            </div>
            <Switch checked={r.on} label={r.title} disabled={save.isPending} onChange={(v) => save.mutate({ [r.key]: v })} />
          </li>
        ))}
      </ul>
      <p className="mt-3 text-xs text-[var(--ink-500)]">{t("settings.autoNote")}</p>
    </section>
  );
}

// The switch for selling at all. Off, Payments leaves the menu; the page stays reachable
// from here for the history.
function SalesCard() {
  const qc = useQueryClient();
  const toast = useToast();
  const ps = usePaymentSettings();
  const save = useMutation({
    mutationFn: (enabled: boolean) => unwrap(api.PATCH("/api/v1/payments/settings", { body: { enabled } })),
    onSuccess: (v) => {
      qc.setQueryData(qk.paymentSettings, v);
      toast.ok(v.enabled ? t("settings.salesOnToast") : t("settings.salesOffToast"));
    },
    onError: (e) => toast.error(errorText(e)),
  });
  const on = ps.data?.enabled === true;
  return (
    <section className="card glass reveal" style={{ "--i": 5 } as React.CSSProperties}>
      <div className="card-head">
        <div>
          <h2 className="card-title">{t("settings.sales")}</h2>
          <div className="card-sub">{t("settings.salesSub")}</div>
        </div>
        {ps.isPending ? (
          <Skeleton style={{ width: 40, height: 24, borderRadius: 12 }} />
        ) : ps.isError ? null : (
          <Switch checked={on} label={t("settings.sales")} disabled={save.isPending} onChange={(v) => save.mutate(v)} />
        )}
      </div>
      {ps.isError ? (
        <ErrorState text={errorText(ps.error)} onRetry={() => void ps.refetch()} />
      ) : ps.data ? (
        <div className="flex flex-wrap items-center justify-between gap-3">
          <p className="min-w-0 flex-1 text-xs text-[var(--ink-500)]">{on ? t("settings.salesOnNote") : t("settings.salesOffNote")}</p>
          <Link to="/payments" className="btn btn-glass btn-sm">
            {t("settings.openPayments")} <ChevronRight size={16} aria-hidden />
          </Link>
        </div>
      ) : null}
    </section>
  );
}

// API keys and the reference live on their own page: they are for scripts, not daily work.
function ApiCard() {
  return (
    <section className="card glass reveal" style={{ "--i": 5 } as React.CSSProperties}>
      <div className="card-head">
        <div>
          <h2 className="card-title">{t("settings.api")}</h2>
          <div className="card-sub">{t("settings.apiSub")}</div>
        </div>
        <Link to="/settings/api" className="btn btn-glass btn-sm">
          {t("settings.apiOpen")} <ChevronRight size={16} aria-hidden />
        </Link>
      </div>
    </section>
  );
}

function DevicesCard({ s }: { s: Schemas["SettingsView"] }) {
  const save = useSaveSettings();
  return (
    <section className="card glass reveal" style={{ "--i": 4 } as React.CSSProperties}>
      <div className="card-head">
        <div>
          <h2 className="card-title">{t("settings.devices")}</h2>
          <div className="card-sub">{t("settings.devicesSub")}</div>
        </div>
      </div>
      <ul className="row-list">
        <li className="flex items-start justify-between gap-4 py-3">
          <div className="min-w-0">
            <div className="text-[13px] font-medium">{t("settings.binding")}</div>
            <div className="mt-1 text-xs text-[var(--ink-500)]">{t("settings.bindingSub")}</div>
          </div>
          <Switch checked={s.device_binding} label={t("settings.binding")} disabled={save.isPending} onChange={(v) => save.mutate({ device_binding: v })} />
        </li>
        <li className="flex items-start justify-between gap-4 py-3">
          <div className="min-w-0">
            <div className="text-[13px] font-medium">{t("settings.requireHwid")}</div>
            <div className="mt-1 text-xs text-[var(--ink-500)]">{t("settings.requireHwidSub")}</div>
          </div>
          <Switch checked={s.device_require_hwid} label={t("settings.requireHwid")} disabled={save.isPending || !s.device_binding} onChange={(v) => save.mutate({ device_require_hwid: v })} />
        </li>
      </ul>
      <p className="mt-3 text-xs text-[var(--ink-500)]">{t("settings.devicesNote")}</p>
    </section>
  );
}

/** The release the panel runs, the newest one and the host updater's last run. */
function UpdatesCard() {
  const u = useUpdates();
  const qc = useQueryClient();
  const toast = useToast();
  const [confirm, setConfirm] = useState(false);
  const put = (d: Schemas["UpdatesView"]) => qc.setQueryData(qk.updates, d);
  const fail = (e: unknown) => toast.error(errorText(e));
  const check = useMutation({ mutationFn: () => unwrap(api.POST("/api/v1/updates/check")), onSuccess: put, onError: fail });
  const auto = useMutation({
    mutationFn: (v: boolean) => unwrap(api.PATCH("/api/v1/updates", { body: { auto: v } })),
    onSuccess: (d) => {
      put(d);
      toast.ok(t("settings.saved"));
    },
    onError: fail,
  });
  const request = useMutation({
    mutationFn: () => unwrap(api.POST("/api/v1/updates/request")),
    onSuccess: (d) => {
      put(d);
      setConfirm(false);
    },
    onError: fail,
  });
  if (u.isPending) return <Skeleton style={{ height: 180, borderRadius: 20 }} />;
  if (u.isError) {
    return (
      <section className="card glass">
        <ErrorState text={errorText(u.error)} onRetry={() => void u.refetch()} />
      </section>
    );
  }
  const v = u.data;
  const notes = v.notes[getLocale()] || v.notes.en || "";
  const running = v.host?.state === "running";
  const waiting = v.requested_at > 0 || running;
  const stale = v.requested_at > 0 && !running && Date.now() / 1000 - v.requested_at > 120;
  const lastAt = v.host?.at ? ago(v.host.at) : "";
  return (
    <section id="updates" className="card glass reveal">
      <div className="card-head">
        <div>
          <h2 className="card-title">{t("settings.updates")}</h2>
          <div className="card-sub">{t("settings.updatesSub")}</div>
        </div>
        <Button size="sm" loading={check.isPending} onClick={() => check.mutate()}>
          <RefreshCw size={16} aria-hidden /> {t("settings.updatesCheck")}
        </Button>
      </div>
      <div className="flex flex-wrap items-center gap-x-3 gap-y-2">
        <span className="font-semibold">mikan {v.current}</span>
        {v.available ? (
          <Pill tone="warn">{t("settings.updatesOut", { v: v.latest })}</Pill>
        ) : v.latest ? (
          <Pill tone="ok">{t("settings.updatesLatest")}</Pill>
        ) : (
          <Pill tone="off">{v.error === "no_release" ? t("settings.updatesNoRelease") : v.checked_at ? t("settings.updatesUnknown") : t("settings.updatesNever")}</Pill>
        )}
        {v.checked_at > 0 ? <span className="text-xs text-[var(--ink-500)]">{t("settings.updatesChecked", { ago: ago(new Date(v.checked_at * 1000).toISOString()) })}</span> : null}
      </div>
      {v.error && v.error !== "no_release" ? <p className="mt-2 text-xs text-[var(--berry-600)]">{t("settings.updatesError", { e: v.error })}</p> : null}
      {v.available && notes ? (
        <div className="panel-soft mt-4 p-3">
          <div className="mb-2 text-xs font-semibold text-[var(--ink-500)]">{t("settings.updatesChanges", { v: v.latest })}</div>
          <ul className="notes">
            {notes
              .split("\n")
              .filter((l) => l.trim())
              .map((l, i) => (
                <li key={i}>{l.replace(/^[-*]\s*/, "")}</li>
              ))}
          </ul>
        </div>
      ) : null}
      {v.available || waiting ? (
        <div className="mt-4 flex flex-wrap items-center gap-3">
          <Button variant="primary" loading={request.isPending || (waiting && !stale)} disabled={waiting} onClick={() => setConfirm(true)}>
            {waiting ? t("settings.updatesWaiting") : t("settings.updatesNow", { v: v.latest })}
          </Button>
        </div>
      ) : null}
      {waiting && !stale ? <p className="mt-2 text-xs text-[var(--ink-500)]">{t("settings.updatesWaitingText")}</p> : null}
      {stale ? <p className="mt-2 text-xs text-[var(--honey-600)]">{t("settings.updatesStale")}</p> : null}
      {v.host && !waiting ? (
        <p className={clsx("mt-3 text-xs", v.host.state === "failed" ? "text-[var(--berry-600)]" : "text-[var(--ink-500)]")}>
          {v.host.state === "failed"
            ? t("settings.updatesLastFailed", { v: v.host.version || v.latest, from: v.host.from, e: v.host.error.split("\n")[0] ?? "" })
            : t("settings.updatesLastOk", { v: v.host.version, from: v.host.from, ago: lastAt })}
        </p>
      ) : null}
      <ul className="row-list mt-3">
        <li className="flex items-start justify-between gap-4 py-3">
          <div className="min-w-0">
            <div className="text-[13px] font-medium">{t("settings.updatesAuto")}</div>
            <div className="mt-1 text-xs text-[var(--ink-500)]">{t("settings.updatesAutoSub")}</div>
          </div>
          <Switch checked={v.auto} label={t("settings.updatesAuto")} disabled={auto.isPending} onChange={(on) => auto.mutate(on)} />
        </li>
      </ul>
      <Confirm
        open={confirm}
        onOpenChange={setConfirm}
        title={t("settings.updatesConfirmTitle", { v: v.latest })}
        text={t("settings.updatesConfirmText")}
        confirm={t("settings.updatesConfirm")}
        loading={request.isPending}
        onConfirm={() => request.mutate()}
      />
    </section>
  );
}

function AccessCard({ s }: { s: Schemas["SettingsView"] }) {
  const toast = useToast();
  const [confirm, setConfirm] = useState(false);
  const reset = useMutation({
    mutationFn: () => unwrap(api.POST("/api/v1/settings/reset-admin-path")),
    onSuccess: (r) => {
      toast.ok(t("settings.newLinkIssued"));
      window.setTimeout(() => window.location.assign(r.admin_url), 5500);
    },
    onError: (e) => toast.error(errorText(e)),
  });
  const copy = async () => {
    try {
      await navigator.clipboard.writeText(s.admin_url);
      toast.ok(t("settings.adminLinkCopied"));
    } catch {
      toast.error(t("common.copyFailed"));
    }
  };
  return (
    <section className="card glass reveal" style={{ "--i": 1 } as React.CSSProperties}>
      <div className="card-head">
        <div>
          <h2 className="card-title">{t("settings.adminLink")}</h2>
          <div className="card-sub">{t("settings.adminLinkSub")}</div>
        </div>
      </div>
      <div className="link-field">
        <span className="mono">{s.admin_url || t("settings.noHost")}</span>
        <button type="button" className="icon-btn" onClick={copy} aria-label={t("settings.copyAdminLink")}>
          <Copy size={18} />
        </button>
      </div>
      <div className="mt-3 flex flex-wrap items-center gap-3">
        <Button variant="danger" size="sm" onClick={() => setConfirm(true)}>
          <KeyRound size={16} aria-hidden /> {t("settings.newLink")}
        </Button>
        <span className="text-xs text-[var(--ink-500)]">{t("settings.newLinkHint")}</span>
      </div>
      <Confirm
        open={confirm}
        onOpenChange={setConfirm}
        title={t("settings.newLinkTitle")}
        text={t("settings.newLinkText")}
        confirm={t("settings.newLinkConfirm")}
        danger
        loading={reset.isPending || reset.isSuccess}
        onConfirm={() => reset.mutate()}
      />
    </section>
  );
}

function CertificateCard({ s }: { s: Schemas["SettingsView"] }) {
  const qc = useQueryClient();
  const toast = useToast();
  const c = s.certificate;
  const renew = useMutation({
    mutationFn: () => unwrap(api.POST("/api/v1/settings/certificate/renew")),
    onSuccess: () => {
      toast.ok(t("settings.certRequested"));
      window.setTimeout(() => void qc.invalidateQueries({ queryKey: qk.settings }), 15_000);
    },
    onError: (e) => toast.error(errorText(e)),
  });
  const ok = c.kind === "letsencrypt";
  const until = new Date(c.not_after).toLocaleString(getLocale(), { day: "numeric", month: "long", hour: "2-digit", minute: "2-digit" });
  return (
    <section className="card glass reveal" style={{ "--i": 2 } as React.CSSProperties}>
      <div className="card-head">
        <div>
          <h2 className="card-title">{t("settings.cert")}</h2>
          <div className="card-sub">{ok ? t("settings.certLe", { id: c.identifier, until }) : t("settings.certSelf")}</div>
        </div>
        {ok ? <Pill tone="ok">{t("settings.certValid")}</Pill> : <Pill tone="warn">{t("settings.certTemp")}</Pill>}
      </div>
      {c.error ? (
        <p className="mb-3 text-[13px] text-[var(--berry-600)]" role="alert">
          {tMaybe(`errors.acme.${c.error}`) ?? c.error}
        </p>
      ) : null}
      <p className="mb-3 text-xs text-[var(--ink-500)]">{t("settings.certNote")}</p>
      <Button size="sm" loading={renew.isPending} onClick={() => renew.mutate()}>
        {t("settings.certRenew")}
      </Button>
    </section>
  );
}

function PasswordCard() {
  const toast = useToast();
  const [current, setCurrent] = useState("");
  const [next, setNext] = useState("");
  const change = useMutation({
    mutationFn: () => unwrap(api.POST("/api/v1/auth/password", { body: { current, new: next } })),
    onSuccess: () => {
      setCurrent("");
      setNext("");
      toast.ok(t("settings.passwordChanged"));
    },
  });
  const errors = change.error instanceof ApiError ? change.error.fields : {};
  return (
    <section className="card glass reveal" style={{ "--i": 2 } as React.CSSProperties}>
      <form
        onSubmit={(e) => {
          e.preventDefault();
          change.mutate();
        }}
        noValidate
      >
        <div className="card-head">
          <h2 className="card-title">{t("login.password")}</h2>
        </div>
        <Field label={t("settings.currentPassword")} htmlFor="p-cur" error={errors.current}>
          <input id="p-cur" type="password" className="input" autoComplete="current-password" value={current} onChange={(e) => setCurrent(e.target.value)} />
        </Field>
        <Field label={t("settings.newPassword")} htmlFor="p-new" hint={t("settings.newPasswordHint")} error={errors.new}>
          <input id="p-new" type="password" className="input" autoComplete="new-password" value={next} onChange={(e) => setNext(e.target.value)} minLength={12} />
        </Field>
        <Button type="submit" variant="primary" loading={change.isPending} disabled={!current || next.length < 12}>
          {t("settings.changePassword")}
        </Button>
      </form>
    </section>
  );
}

function TwoFactorCard() {
  const me = useQuery(meQuery);
  const qc = useQueryClient();
  const toast = useToast();
  const [setup, setSetup] = useState<{ secret: string; uri: string } | null>(null);
  const [code, setCode] = useState("");
  const [codes, setCodes] = useState<string[] | null>(null);
  const [disable, setDisable] = useState(false);
  const [pw, setPw] = useState("");
  const enabled = me.data?.admin.totp_enabled;

  const start = useMutation({ mutationFn: () => unwrap(api.POST("/api/v1/auth/totp/setup")), onSuccess: setSetup, onError: (e) => toast.error(errorText(e)) });
  const enable = useMutation({
    mutationFn: () => unwrap(api.POST("/api/v1/auth/totp/enable", { body: { code } })),
    onSuccess: (r) => {
      setCodes(r.recovery_codes);
      setSetup(null);
      setCode("");
      void qc.invalidateQueries({ queryKey: qk.me });
    },
  });
  const off = useMutation({
    mutationFn: () => unwrap(api.POST("/api/v1/auth/totp/disable", { body: { password: pw, code } })),
    onSuccess: () => {
      setDisable(false);
      setPw("");
      setCode("");
      void qc.invalidateQueries({ queryKey: qk.me });
      toast.ok(t("settings.twoFactorOffToast"));
    },
    onError: (e) => toast.error(errorText(e)),
  });

  return (
    <section className="card glass reveal" style={{ "--i": 3 } as React.CSSProperties}>
      <div className="card-head">
        <div>
          <h2 className="card-title">{t("settings.twoFactor")}</h2>
          <div className="card-sub">{enabled ? t("settings.twoFactorOn") : t("settings.twoFactorOff")}</div>
        </div>
        <ShieldCheck size={20} className={enabled ? "text-[var(--leaf-500)]" : "text-[var(--ink-300)]"} aria-hidden />
      </div>
      {codes ? (
        <div>
          <p className="mb-3 text-[13px] text-[var(--ink-600)]">{t("settings.recoveryText")}</p>
          <div className="panel-soft mono grid grid-cols-2 gap-2 p-3 text-sm">
            {codes.map((c) => (
              <span key={c}>{c}</span>
            ))}
          </div>
          <Button className="mt-3" variant="primary" onClick={() => setCodes(null)}>
            {t("settings.recoverySaved")}
          </Button>
        </div>
      ) : setup ? (
        <form
          onSubmit={(e) => {
            e.preventDefault();
            enable.mutate();
          }}
        >
          <div className="grid gap-4 sm:grid-cols-[160px_minmax(0,1fr)]">
            <QR value={setup.uri} size={160} label={t("settings.totpQr")} />
            <div>
              <p className="mb-3 text-[13px] text-[var(--ink-600)]">{t("settings.totpScan")}</p>
              <p className="mono mb-3 text-xs break-all text-[var(--ink-500)]">{setup.secret}</p>
              <Field label={t("settings.totpCode")} htmlFor="totp-code" error={enable.error instanceof ApiError ? (enable.error.fields.code ?? errorText(enable.error)) : undefined}>
                <input id="totp-code" className="input mono max-w-[160px] tracking-[0.2em]" inputMode="numeric" maxLength={6} value={code} onChange={(e) => setCode(e.target.value.trim())} autoComplete="one-time-code" />
              </Field>
              <div className="flex gap-2">
                <Button type="submit" variant="primary" loading={enable.isPending} disabled={code.length !== 6}>
                  {t("common.enable")}
                </Button>
                <Button variant="ghost" onClick={() => setSetup(null)}>
                  {t("common.cancel")}
                </Button>
              </div>
            </div>
          </div>
        </form>
      ) : enabled ? (
        disable ? (
          <form
            onSubmit={(e) => {
              e.preventDefault();
              off.mutate();
            }}
          >
            <Field label={t("login.password")} htmlFor="off-pw">
              <input id="off-pw" type="password" className="input" value={pw} onChange={(e) => setPw(e.target.value)} autoComplete="current-password" />
            </Field>
            <Field label={t("settings.totpCode")} htmlFor="off-code">
              <input id="off-code" className="input mono max-w-[160px]" inputMode="numeric" maxLength={6} value={code} onChange={(e) => setCode(e.target.value.trim())} />
            </Field>
            <div className="flex gap-2">
              <Button type="submit" variant="danger-solid" loading={off.isPending} disabled={!pw || code.length !== 6}>
                {t("settings.twoFactorDisable")}
              </Button>
              <Button variant="ghost" onClick={() => setDisable(false)}>
                {t("common.cancel")}
              </Button>
            </div>
          </form>
        ) : (
          <Button variant="danger" onClick={() => setDisable(true)}>
            {t("common.disable")}
          </Button>
        )
      ) : (
        <Button variant="primary" loading={start.isPending} onClick={() => start.mutate()}>
          {t("settings.twoFactorEnable")}
        </Button>
      )}
    </section>
  );
}

function SessionsCard() {
  const qc = useQueryClient();
  const toast = useToast();
  const sessions = useQuery({ queryKey: qk.sessions, queryFn: () => unwrap(api.GET("/api/v1/auth/sessions")) });
  const revoke = useMutation({
    mutationFn: (id: string) => unwrap(api.DELETE("/api/v1/auth/sessions/{id}", { params: { path: { id } } })),
    onSuccess: () => {
      void qc.invalidateQueries({ queryKey: qk.sessions });
      toast.ok(t("settings.sessionEnded"));
    },
    onError: (e) => toast.error(errorText(e)),
  });
  return (
    <section className="card glass reveal" style={{ "--i": 4 } as React.CSSProperties}>
      <div className="card-head">
        <div>
          <h2 className="card-title">{t("settings.sessions")}</h2>
          <div className="card-sub">{t("settings.sessionsSub")}</div>
        </div>
      </div>
      {sessions.isPending ? (
        <Skeleton style={{ height: 80 }} />
      ) : (
        <ul className="row-list">
          {(sessions.data ?? []).map((s) => (
            <li key={s.id} className="flex items-center justify-between gap-3 py-3">
              <div className="min-w-0">
                <div className="truncate text-[13px] font-medium">{browserName(s.user_agent)}</div>
                <div className="text-xs text-[var(--ink-500)]">
                  <span className="mono">{s.ip}</span> · {s.current ? <span className="text-[var(--leaf-700)]">{t("settings.thisSession")}</span> : t("settings.activeAgo", { ago: ago(s.last_seen_at) })}
                </div>
              </div>
              {!s.current ? (
                <Button size="sm" variant="danger" loading={revoke.isPending && revoke.variables === s.id} onClick={() => revoke.mutate(s.id)}>
                  <LogOut size={16} aria-hidden /> {t("settings.endSession")}
                </Button>
              ) : null}
            </li>
          ))}
        </ul>
      )}
    </section>
  );
}

function browserName(ua: string): string {
  const os = /iPhone|iPad/.test(ua) ? "iOS" : /Android/.test(ua) ? "Android" : /Mac OS X/.test(ua) ? "macOS" : /Windows/.test(ua) ? "Windows" : /Linux/.test(ua) ? "Linux" : "";
  const br = /Edg\//.test(ua) ? "Edge" : /YaBrowser/.test(ua) ? t("settings.yandexBrowser") : /Firefox\//.test(ua) ? "Firefox" : /Chrome\//.test(ua) ? "Chrome" : /Safari\//.test(ua) ? "Safari" : t("settings.browser");
  return os ? `${br}, ${os}` : br;
}
