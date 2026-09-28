import { useMutation, useQuery, useQueryClient } from "@tanstack/react-query";
import { Copy, KeyRound, LogOut, ShieldCheck } from "lucide-react";
import { useEffect, useState, type FormEvent } from "react";
import { api, ApiError, errorText, unwrap, type Schemas } from "../../api/client";
import { meQuery, qk, useSettings } from "../../api/hooks";
import { Confirm } from "../../components/overlay";
import { useToast } from "../../components/toast";
import { Button, ErrorState, Field, PageHeader, Pill, QR, Skeleton } from "../../components/ui";
import { ago } from "../../lib/format";

export function SettingsPage() {
  const settings = useSettings();
  return (
    <>
      <PageHeader title="Настройки" sub="Адрес сервера, оформление подписки и безопасность входа" />
      {settings.isPending ? (
        <Skeleton style={{ height: 320, borderRadius: 20 }} />
      ) : settings.isError ? (
        <section className="card glass">
          <ErrorState text={errorText(settings.error)} onRetry={() => void settings.refetch()} />
        </section>
      ) : (
        <div className="grid items-start gap-4 xl:grid-cols-2">
          <GeneralCard s={settings.data} />
          <div className="flex flex-col gap-4">
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
      toast.ok("Настройки сохранены");
    },
    onError: (e) => {
      if (!(e instanceof ApiError && Object.keys(e.fields).length)) toast.error(errorText(e));
    },
  });
}

function GeneralCard({ s }: { s: Schemas["SettingsView"] }) {
  const save = useSaveSettings();
  const [form, setForm] = useState({ brand: s.brand, support_url: s.support_url, public_host: s.public_host, domain: s.domain, quiet_hour_utc: s.quiet_hour_utc });
  useEffect(() => setForm({ brand: s.brand, support_url: s.support_url, public_host: s.public_host, domain: s.domain, quiet_hour_utc: s.quiet_hour_utc }), [s]);
  const errors = save.error instanceof ApiError ? save.error.fields : {};
  const submit = (e: FormEvent) => {
    e.preventDefault();
    save.mutate({ ...form, quiet_hour_utc: Number(form.quiet_hour_utc) });
  };
  const set = (k: keyof typeof form) => (e: React.ChangeEvent<HTMLInputElement>) => setForm((f) => ({ ...f, [k]: e.target.value }));
  return (
    <section className="card glass reveal">
      <form onSubmit={submit} noValidate>
        <div className="card-head">
          <h2 className="card-title">Сервер и подписка</h2>
        </div>
        <Field label="Адрес сервера" htmlFor="s-host" hint="IP-адрес, по которому клиенты подключаются к VPN." error={errors.public_host}>
          <input id="s-host" className="input mono" value={form.public_host} onChange={set("public_host")} aria-invalid={!!errors.public_host} />
        </Field>
        <Field label="Домен" htmlFor="s-domain" hint="Необязательно. Если указан — используется в ссылках вместо IP." error={errors.domain}>
          <input id="s-domain" className="input mono" value={form.domain} onChange={set("domain")} placeholder="vpn.example.com" aria-invalid={!!errors.domain} />
        </Field>
        <Field label="Название сервиса" htmlFor="s-brand" hint="Так профиль будет называться в приложениях клиентов.">
          <input id="s-brand" className="input" value={form.brand} onChange={set("brand")} maxLength={40} />
        </Field>
        <Field label="Поддержка" htmlFor="s-support" hint="Ссылка на Telegram или сайт — кнопка «Написать в поддержку» на странице подписки." error={errors.support_url}>
          <input id="s-support" className="input" value={form.support_url} onChange={set("support_url")} placeholder="https://t.me/your_support" aria-invalid={!!errors.support_url} />
        </Field>
        <Field label="Тихий час (UTC)" htmlFor="s-quiet" hint="Раз в сутки в этот час пул слотов обслуживается: клиенты Hysteria2 и TUIC ненадолго переподключаются.">
          <input id="s-quiet" className="input max-w-[100px]" inputMode="numeric" value={String(form.quiet_hour_utc)} onChange={set("quiet_hour_utc")} />
        </Field>
        <Button type="submit" variant="primary" loading={save.isPending}>
          Сохранить
        </Button>
      </form>
    </section>
  );
}

function AccessCard({ s }: { s: Schemas["SettingsView"] }) {
  const toast = useToast();
  const [confirm, setConfirm] = useState(false);
  const reset = useMutation({
    mutationFn: () => unwrap(api.POST("/api/v1/settings/reset-admin-path")),
    onSuccess: (r) => {
      toast.ok("Новая ссылка выдана — переходим");
      window.setTimeout(() => window.location.assign(r.admin_url), 5500);
    },
    onError: (e) => toast.error(errorText(e)),
  });
  const copy = async () => {
    try {
      await navigator.clipboard.writeText(s.admin_url);
      toast.ok("Ссылка на панель скопирована");
    } catch {
      toast.error("Не удалось скопировать");
    }
  };
  return (
    <section className="card glass reveal" style={{ "--i": 1 } as React.CSSProperties}>
      <div className="card-head">
        <div>
          <h2 className="card-title">Ссылка на панель</h2>
          <div className="card-sub">Любой другой адрес на этом порту отвечает «404 Not Found».</div>
        </div>
      </div>
      <div className="link-field">
        <span className="mono">{s.admin_url || "адрес сервера не задан"}</span>
        <button type="button" className="icon-btn" onClick={copy} aria-label="Скопировать ссылку на панель">
          <Copy size={18} />
        </button>
      </div>
      <div className="mt-3 flex flex-wrap items-center gap-3">
        <Button variant="danger" size="sm" onClick={() => setConfirm(true)}>
          <KeyRound size={16} aria-hidden /> Выдать новую ссылку
        </Button>
        <span className="text-xs text-[var(--ink-500)]">Если ссылка утекла. Старая перестанет работать.</span>
      </div>
      <Confirm
        open={confirm}
        onOpenChange={setConfirm}
        title="Выдать новую ссылку на панель?"
        text="Старая перестанет открываться через несколько секунд, мы сразу перейдём на новую. Сохраните её — без неё войти можно будет только через сервер (mikan url)."
        confirm="Выдать"
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
      toast.ok("Запросили сертификат — это займёт до минуты");
      window.setTimeout(() => void qc.invalidateQueries({ queryKey: qk.settings }), 15_000);
    },
    onError: (e) => toast.error(errorText(e)),
  });
  const ok = c.kind === "letsencrypt";
  return (
    <section className="card glass reveal" style={{ "--i": 2 } as React.CSSProperties}>
      <div className="card-head">
        <div>
          <h2 className="card-title">Сертификат панели</h2>
          <div className="card-sub">
            {ok ? `Let's Encrypt для ${c.identifier}, действует до ${new Date(c.not_after).toLocaleString("ru-RU", { day: "numeric", month: "long", hour: "2-digit", minute: "2-digit" })}` : "Самоподписанный: браузер предупредит, а часть приложений не примет подписку"}
          </div>
        </div>
        {ok ? <Pill tone="ok">действителен</Pill> : <Pill tone="warn">временный</Pill>}
      </div>
      {c.error ? (
        <p className="mb-3 text-[13px] text-[var(--berry-600)]" role="alert">
          {c.error}
        </p>
      ) : null}
      <p className="mb-3 text-xs text-[var(--ink-500)]">Продлевается автоматически. Для выпуска нужен открытый порт 80 — он занимается на несколько секунд.</p>
      <Button size="sm" loading={renew.isPending} onClick={() => renew.mutate()}>
        Запросить сейчас
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
      toast.ok("Пароль изменён, другие сессии завершены");
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
          <h2 className="card-title">Пароль</h2>
        </div>
        <Field label="Текущий пароль" htmlFor="p-cur" error={errors.current}>
          <input id="p-cur" type="password" className="input" autoComplete="current-password" value={current} onChange={(e) => setCurrent(e.target.value)} />
        </Field>
        <Field label="Новый пароль" htmlFor="p-new" hint="Не короче 12 символов. Удобно — фраза из нескольких слов." error={errors.new}>
          <input id="p-new" type="password" className="input" autoComplete="new-password" value={next} onChange={(e) => setNext(e.target.value)} minLength={12} />
        </Field>
        <Button type="submit" variant="primary" loading={change.isPending} disabled={!current || next.length < 12}>
          Сменить пароль
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
      toast.ok("2FA выключена");
    },
    onError: (e) => toast.error(errorText(e)),
  });

  return (
    <section className="card glass reveal" style={{ "--i": 3 } as React.CSSProperties}>
      <div className="card-head">
        <div>
          <h2 className="card-title">Двухфакторная защита</h2>
          <div className="card-sub">{enabled ? "Включена: при входе нужен код из приложения" : "Код из приложения-аутентификатора при каждом входе"}</div>
        </div>
        <ShieldCheck size={20} className={enabled ? "text-[var(--leaf-500)]" : "text-[var(--ink-300)]"} aria-hidden />
      </div>
      {codes ? (
        <div>
          <p className="mb-3 text-[13px] text-[var(--ink-600)]">Сохраните резервные коды — каждый срабатывает один раз, если телефона не будет под рукой. Больше мы их не покажем.</p>
          <div className="panel-soft mono grid grid-cols-2 gap-2 p-3 text-sm">
            {codes.map((c) => (
              <span key={c}>{c}</span>
            ))}
          </div>
          <Button className="mt-3" variant="primary" onClick={() => setCodes(null)}>
            Я сохранил коды
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
            <QR value={setup.uri} size={160} label="QR-код для приложения-аутентификатора" />
            <div>
              <p className="mb-3 text-[13px] text-[var(--ink-600)]">Отсканируйте код в Google Authenticator, 1Password или Aegis и введите 6 цифр.</p>
              <p className="mono mb-3 text-xs break-all text-[var(--ink-500)]">{setup.secret}</p>
              <Field label="Код из приложения" htmlFor="totp-code" error={enable.error instanceof ApiError ? (enable.error.fields.code ?? errorText(enable.error)) : undefined}>
                <input id="totp-code" className="input mono max-w-[160px] tracking-[0.2em]" inputMode="numeric" maxLength={6} value={code} onChange={(e) => setCode(e.target.value.trim())} autoComplete="one-time-code" />
              </Field>
              <div className="flex gap-2">
                <Button type="submit" variant="primary" loading={enable.isPending} disabled={code.length !== 6}>
                  Включить
                </Button>
                <Button variant="ghost" onClick={() => setSetup(null)}>
                  Отмена
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
            <Field label="Пароль" htmlFor="off-pw">
              <input id="off-pw" type="password" className="input" value={pw} onChange={(e) => setPw(e.target.value)} autoComplete="current-password" />
            </Field>
            <Field label="Код из приложения" htmlFor="off-code">
              <input id="off-code" className="input mono max-w-[160px]" inputMode="numeric" maxLength={6} value={code} onChange={(e) => setCode(e.target.value.trim())} />
            </Field>
            <div className="flex gap-2">
              <Button type="submit" variant="danger-solid" loading={off.isPending} disabled={!pw || code.length !== 6}>
                Выключить 2FA
              </Button>
              <Button variant="ghost" onClick={() => setDisable(false)}>
                Отмена
              </Button>
            </div>
          </form>
        ) : (
          <Button variant="danger" onClick={() => setDisable(true)}>
            Выключить
          </Button>
        )
      ) : (
        <Button variant="primary" loading={start.isPending} onClick={() => start.mutate()}>
          Включить 2FA
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
      toast.ok("Сессия завершена");
    },
    onError: (e) => toast.error(errorText(e)),
  });
  return (
    <section className="card glass reveal" style={{ "--i": 4 } as React.CSSProperties}>
      <div className="card-head">
        <div>
          <h2 className="card-title">Активные сессии</h2>
          <div className="card-sub">Где сейчас открыта панель</div>
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
                  <span className="mono">{s.ip}</span> · {s.current ? <span className="text-[var(--leaf-700)]">эта сессия</span> : `активна ${ago(s.last_seen_at)}`}
                </div>
              </div>
              {!s.current ? (
                <Button size="sm" variant="danger" loading={revoke.isPending && revoke.variables === s.id} onClick={() => revoke.mutate(s.id)}>
                  <LogOut size={16} aria-hidden /> Завершить
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
  const br = /Edg\//.test(ua) ? "Edge" : /YaBrowser/.test(ua) ? "Яндекс Браузер" : /Firefox\//.test(ua) ? "Firefox" : /Chrome\//.test(ua) ? "Chrome" : /Safari\//.test(ua) ? "Safari" : "Браузер";
  return os ? `${br}, ${os}` : br;
}
