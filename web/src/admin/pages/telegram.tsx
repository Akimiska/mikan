import { useMutation, useQuery, useQueryClient } from "@tanstack/react-query";
import { ArrowDown, ArrowUp, Bot, Link2, Plus, Send, Trash2, TriangleAlert } from "lucide-react";
import { useEffect, useMemo, useState, type FormEvent } from "react";
import { api, ApiError, errorText, unwrap, type Schemas } from "../../api/client";
import { qk, useSettings } from "../../api/hooks";
import { ago, num } from "../../lib/format";
import { Confirm } from "../../components/overlay";
import { useToast } from "../../components/toast";
import { Bar, Button, ErrorState, Field, PageHeader, Pill, Segmented, Skeleton, Switch } from "../../components/ui";
import { t, tMaybe } from "../../i18n";

type View = Schemas["TelegramView"];
type Config = Schemas["Config"];
type MenuButton = Schemas["MenuButton"];
type TextKey = keyof Schemas["Texts"];

function useTelegram() {
  return useQuery({
    queryKey: qk.telegram,
    queryFn: () => unwrap(api.GET("/api/v1/telegram")),
    // A broadcast in progress moves every second; otherwise little changes.
    refetchInterval: (q) => (q.state.data?.broadcast?.active ? 2_000 : 10_000),
  });
}

function usePatchTelegram() {
  const qc = useQueryClient();
  return useMutation({
    mutationFn: (body: Schemas["PatchTelegramInputBody"]) => unwrap(api.PATCH("/api/v1/telegram", { body })),
    onSuccess: (v) => qc.setQueryData(qk.telegram, v),
  });
}

const same = (a: unknown, b: unknown) => JSON.stringify(a) === JSON.stringify(b);

export function TelegramPage() {
  const tg = useTelegram();
  const patch = usePatchTelegram();
  const toast = useToast();
  const [draft, setDraft] = useState<Config | null>(null);
  const saved = tg.data?.config;
  // A fresh copy of the saved setup whenever it changes under us (and nothing is edited).
  useEffect(() => {
    if (saved && (!draft || same(draft, saved))) setDraft(structuredClone(saved));
    // Only the server's copy drives this.
  }, [saved]);
  const dirty = !!draft && !!saved && !same(draft, saved);
  const save = () =>
    draft &&
    patch.mutate(
      { config: draft },
      {
        onSuccess: (v) => {
          setDraft(structuredClone(v.config));
          toast.ok(t("telegram.saved"));
        },
        onError: (e) => toast.error(errorText(e)),
      },
    );

  return (
    <>
      <PageHeader title={t("nav.telegram")} sub={t("telegram.subtitle")} />
      {tg.isPending || (tg.data && !draft) ? (
        <div className="grid gap-4 xl:grid-cols-[minmax(0,1fr)_360px]">
          <Skeleton style={{ height: 320, borderRadius: 20 }} />
          <Skeleton style={{ height: 420, borderRadius: 20 }} />
        </div>
      ) : tg.isError ? (
        <section className="card glass">
          <ErrorState text={errorText(tg.error)} onRetry={() => void tg.refetch()} />
        </section>
      ) : (
        <div className="grid items-start gap-4 xl:grid-cols-[minmax(0,1fr)_360px]">
          <div className="flex min-w-0 flex-col gap-4">
            <ConnectCard v={tg.data} />
            <MenuCard draft={draft!} setDraft={setDraft} />
            <TextsCard draft={draft!} setDraft={setDraft} defaults={tg.data.defaults} />
            <OptionsCard draft={draft!} setDraft={setDraft} v={tg.data} />
            <BroadcastCard v={tg.data} />
          </div>
          <div className="min-w-0 xl:sticky xl:top-4">
            <Preview draft={draft!} v={tg.data} />
          </div>
        </div>
      )}
      {dirty ? (
        <div className="bulk-bar glass-strong" role="region" aria-label={t("telegram.unsaved")}>
          <span className="text-[13px] font-medium">{t("telegram.unsaved")}</span>
          <Button variant="ghost" size="sm" onClick={() => saved && setDraft(structuredClone(saved))}>
            {t("telegram.discard")}
          </Button>
          <Button variant="primary" size="sm" loading={patch.isPending} onClick={save}>
            {t("common.save")}
          </Button>
        </div>
      ) : null}
    </>
  );
}

function statusOf(v: View): { tone: "ok" | "warn" | "bad" | "off"; text: string } {
  if (!v.enabled) return { tone: "off", text: t("telegram.off") };
  if (v.running && !v.error) return { tone: "ok", text: t("telegram.running") };
  if (v.running) return { tone: "warn", text: t("telegram.reconnecting") };
  if (!v.error) return { tone: "off", text: t("telegram.starting") };
  return { tone: "bad", text: t("telegram.stopped") };
}

function ConnectCard({ v }: { v: View }) {
  const patch = usePatchTelegram();
  const toast = useToast();
  const [token, setToken] = useState("");
  const [editing, setEditing] = useState(false);
  const [removing, setRemoving] = useState(false);
  const [error, setError] = useState("");
  const submit = (e: FormEvent) => {
    e.preventDefault();
    setError("");
    patch.mutate(
      { token: token.trim(), enabled: true },
      {
        onSuccess: (r) => {
          setToken("");
          setEditing(false);
          toast.ok(t("telegram.connected", { name: r.bot?.username ?? "" }));
        },
        onError: (err) => {
          if (err instanceof ApiError && Object.keys(err.fields).length) setError(Object.values(err.fields)[0] ?? "");
          else setError(errorText(err));
        },
      },
    );
  };
  const st = statusOf(v);
  const showForm = !v.token_set || editing;
  return (
    <section className="card glass">
      <div className="card-head">
        <div>
          <h2 className="card-title">{t("telegram.connect")}</h2>
          <div className="card-sub">{t("telegram.connectSub")}</div>
        </div>
        {v.token_set ? <Switch checked={v.enabled} label={t("telegram.enabled")} disabled={patch.isPending} onChange={(on) => patch.mutate({ enabled: on }, { onError: (e) => toast.error(errorText(e)) })} /> : null}
      </div>
      {v.token_set && v.bot ? (
        <div className="panel-soft flex items-center gap-3 p-3">
          <span className="grid h-10 w-10 place-items-center rounded-xl bg-[var(--hover)] text-[var(--ink-700)]" aria-hidden>
            <Bot size={20} />
          </span>
          <div className="min-w-0 flex-1">
            <a className="font-semibold text-[var(--ink-900)] hover:underline" href={`https://t.me/${v.bot.username}`} target="_blank" rel="noreferrer noopener">
              @{v.bot.username}
            </a>
            <div className="truncate text-xs text-[var(--ink-500)]">{v.bot.name}</div>
          </div>
          <Pill tone={st.tone}>{st.text}</Pill>
        </div>
      ) : null}
      {v.error && v.enabled ? (
        <p className="mt-3 text-[13px] text-[var(--berry-600)]" role="alert">
          {tMaybe(`telegram.err.${v.error}`) ?? v.error}
        </p>
      ) : null}
      {v.token_set ? (
        <p className="mt-3 text-xs text-[var(--ink-500)]">
          {t("telegram.stats", { linked: v.linked, accounts: v.accounts })}
          {" · "}
          {v.mini_app_url ? t("telegram.miniAppOn") : t("telegram.miniAppNoCert")}
        </p>
      ) : null}
      {showForm ? (
        <form onSubmit={submit} className="mt-4" noValidate>
          {!v.token_set ? (
            <ol className="mb-4 flex list-decimal flex-col gap-1 pl-5 text-[13px] text-[var(--ink-600)]">
              <li>{t("telegram.step1")}</li>
              <li>{t("telegram.step2")}</li>
            </ol>
          ) : null}
          <Field label={t("telegram.token")} htmlFor="tg-token" hint={t("telegram.tokenHint")} error={error}>
            <input id="tg-token" className="input mono" type="password" autoComplete="off" spellCheck={false} value={token} onChange={(e) => setToken(e.target.value)} placeholder="123456789:AAH…" aria-invalid={!!error} />
          </Field>
          <div className="flex flex-wrap gap-2">
            <Button variant="primary" type="submit" loading={patch.isPending} disabled={!token.trim()}>
              <Link2 size={16} aria-hidden /> {t("telegram.connectButton")}
            </Button>
            {editing ? (
              <Button variant="ghost" onClick={() => setEditing(false)}>
                {t("common.cancel")}
              </Button>
            ) : null}
          </div>
        </form>
      ) : (
        <div className="mt-4 flex flex-wrap gap-2">
          <Button size="sm" onClick={() => setEditing(true)}>
            {t("telegram.changeToken")}
          </Button>
          <Button size="sm" variant="danger" onClick={() => setRemoving(true)}>
            <Trash2 size={16} aria-hidden /> {t("telegram.removeToken")}
          </Button>
        </div>
      )}
      <Confirm
        open={removing}
        onOpenChange={setRemoving}
        title={t("telegram.removeTitle")}
        text={t("telegram.removeText")}
        confirm={t("telegram.removeToken")}
        danger
        loading={patch.isPending}
        onConfirm={() => patch.mutate({ token: "" }, { onSuccess: () => setRemoving(false), onError: (e) => toast.error(errorText(e)) })}
      />
    </section>
  );
}

const ACTIONS = ["sub", "devices", "connect", "renew", "support", "app"] as const;

function actionLabel(a: string): string {
  return tMaybe(`telegram.action.${a}`) ?? a;
}

function MenuCard({ draft, setDraft }: { draft: Config; setDraft: (c: Config) => void }) {
  const set = (i: number, patch: Partial<MenuButton>) => setDraft({ ...draft, buttons: draft.buttons.map((b, j) => (j === i ? { ...b, ...patch } : b)) });
  const move = (i: number, d: -1 | 1) => {
    const list = [...draft.buttons];
    [list[i], list[i + d]] = [list[i + d]!, list[i]!];
    setDraft({ ...draft, buttons: list });
  };
  const add = (action: "url" | "page") => {
    const n = draft.buttons.filter((b) => b.action === "url" || b.action === "page").length + 1;
    setDraft({
      ...draft,
      buttons: [...draft.buttons, { id: `c${Date.now().toString(36)}`, action, label: action === "url" ? t("telegram.newLink") : t("telegram.newPage"), on: true, row: false, url: action === "url" ? "https://" : undefined, text: action === "page" ? "" : undefined }],
    });
    void n;
  };
  const custom = (b: MenuButton) => !ACTIONS.includes(b.action as (typeof ACTIONS)[number]);
  return (
    <section className="card glass">
      <div className="card-head">
        <div>
          <h2 className="card-title">{t("telegram.menu")}</h2>
          <div className="card-sub">{t("telegram.menuSub")}</div>
        </div>
      </div>
      <ul className="flex flex-col gap-2">
        {draft.buttons.map((b, i) => (
          <li key={b.id + i} className="panel-soft p-3">
            <div className="flex items-center gap-2">
              <Switch checked={b.on} label={t("telegram.showButton", { name: b.label })} onChange={(on) => set(i, { on })} />
              <input className="input h-10 min-w-0 flex-1" value={b.label} maxLength={40} onChange={(e) => set(i, { label: e.target.value })} aria-label={t("telegram.buttonLabel")} />
              <button type="button" className="icon-btn" disabled={i === 0} onClick={() => move(i, -1)} aria-label={t("telegram.moveUp")}>
                <ArrowUp size={16} />
              </button>
              <button type="button" className="icon-btn" disabled={i === draft.buttons.length - 1} onClick={() => move(i, 1)} aria-label={t("telegram.moveDown")}>
                <ArrowDown size={16} />
              </button>
              {custom(b) ? (
                <button type="button" className="icon-btn" onClick={() => setDraft({ ...draft, buttons: draft.buttons.filter((_, j) => j !== i) })} aria-label={t("telegram.deleteButton", { name: b.label })}>
                  <Trash2 size={16} />
                </button>
              ) : null}
            </div>
            <div className="mt-2 flex flex-wrap items-center gap-x-4 gap-y-1 text-xs text-[var(--ink-500)]">
              <span>{actionLabel(b.action)}</span>
              {i > 0 ? (
                <label className="flex items-center gap-1.5">
                  <input type="checkbox" className="check" checked={b.row} onChange={(e) => set(i, { row: e.target.checked })} /> {t("telegram.sameRow")}
                </label>
              ) : null}
            </div>
            {b.action === "url" ? <input className="input mono mt-2" value={b.url ?? ""} onChange={(e) => set(i, { url: e.target.value })} placeholder="https://… / tg://…" aria-label={t("telegram.buttonUrl")} /> : null}
            {b.action === "page" ? (
              <textarea className="input mt-2" value={b.text ?? ""} maxLength={3000} onChange={(e) => set(i, { text: e.target.value })} placeholder={t("telegram.pagePlaceholder")} aria-label={t("telegram.pageText")} />
            ) : null}
          </li>
        ))}
      </ul>
      <div className="mt-3 flex flex-wrap gap-2">
        <button type="button" className="chip-btn" onClick={() => add("url")} disabled={draft.buttons.length >= 20}>
          <Plus size={14} className="mr-1 inline" aria-hidden />
          {t("telegram.addLink")}
        </button>
        <button type="button" className="chip-btn" onClick={() => add("page")} disabled={draft.buttons.length >= 20}>
          <Plus size={14} className="mr-1 inline" aria-hidden />
          {t("telegram.addPage")}
        </button>
      </div>
    </section>
  );
}

const TEXTS: { key: TextKey; label: string }[] = [
  { key: "welcome", label: "telegram.text.welcome" },
  { key: "main", label: "telegram.text.main" },
  { key: "renew", label: "telegram.text.renew" },
  { key: "expiring", label: "telegram.text.expiring" },
  { key: "expired", label: "telegram.text.expired" },
  { key: "traffic_90", label: "telegram.text.traffic_90" },
  { key: "traffic_end", label: "telegram.text.traffic_end" },
];

function TextsCard({ draft, setDraft, defaults }: { draft: Config; setDraft: (c: Config) => void; defaults: Schemas["Texts"] }) {
  return (
    <section className="card glass">
      <div className="card-head">
        <div>
          <h2 className="card-title">{t("telegram.texts")}</h2>
          <div className="card-sub">{t("telegram.textsSub")}</div>
        </div>
      </div>
      <Field label={t("telegram.lang")} hint={t("telegram.langHint")}>
        <Segmented
          label={t("telegram.lang")}
          value={draft.lang}
          onChange={(lang) => setDraft({ ...draft, lang })}
          options={[
            { value: "ru", label: "Русский" },
            { value: "en", label: "English" },
          ]}
        />
      </Field>
      {TEXTS.map(({ key, label }) => (
        <Field key={key} label={tMaybe(label) ?? key} htmlFor={`tg-${key}`}>
          <textarea id={`tg-${key}`} className="input" rows={key === "main" || key === "welcome" ? 5 : 2} maxLength={3000} value={draft.texts[key]} placeholder={defaults[key]} onChange={(e) => setDraft({ ...draft, texts: { ...draft.texts, [key]: e.target.value } })} />
        </Field>
      ))}
      <p className="text-xs text-[var(--ink-500)]">{t("telegram.variables")}</p>
    </section>
  );
}

const NOTICES: (keyof Schemas["Notify"])[] = ["expire_3d", "expire_1d", "expired", "traffic_90", "traffic_100"];

function OptionsCard({ draft, setDraft, v }: { draft: Config; setDraft: (c: Config) => void; v: View }) {
  const row = (title: string, sub: string, on: boolean, change: (v: boolean) => void) => (
    <li key={title} className="flex items-start justify-between gap-4 py-3">
      <div className="min-w-0">
        <div className="text-[13px] font-medium">{title}</div>
        <div className="mt-1 text-xs text-[var(--ink-500)]">{sub}</div>
      </div>
      <Switch checked={on} label={title} onChange={change} />
    </li>
  );
  return (
    <section className="card glass">
      <div className="card-head">
        <div>
          <h2 className="card-title">{t("telegram.options")}</h2>
          <div className="card-sub">{t("telegram.optionsSub")}</div>
        </div>
      </div>
      <ul className="row-list">
        {row(t("telegram.miniApp"), v.mini_app_url ? t("telegram.miniAppSub") : t("telegram.miniAppNoCert"), draft.mini_app, (on) => setDraft({ ...draft, mini_app: on }))}
        {row(t("telegram.cleanChat"), t("telegram.cleanChatSub"), draft.clean_chat, (on) => setDraft({ ...draft, clean_chat: on }))}
        {row(t("telegram.quietNight"), t("telegram.quietNightSub"), draft.quiet_night, (on) => setDraft({ ...draft, quiet_night: on }))}
        {NOTICES.map((k) => row(t(`telegram.notice.${k}`), t("telegram.noticeSub"), draft.notify[k], (on) => setDraft({ ...draft, notify: { ...draft.notify, [k]: on } })))}
      </ul>
    </section>
  );
}

function BroadcastCard({ v }: { v: View }) {
  const toast = useToast();
  const qc = useQueryClient();
  const [text, setText] = useState("");
  const [confirm, setConfirm] = useState(false);
  const send = useMutation({
    mutationFn: () => unwrap(api.POST("/api/v1/telegram/broadcast", { body: { text } })),
    onSuccess: (r) => {
      toast.ok(t("telegram.broadcastSent", { n: r.queued }));
      setText("");
      setConfirm(false);
      void qc.invalidateQueries({ queryKey: qk.telegram });
    },
    onError: (e) => toast.error(errorText(e)),
  });
  const busy = !!v.broadcast?.active;
  const ready = v.running && v.accounts > 0 && !busy;
  return (
    <section className="card glass">
      <div className="card-head">
        <div>
          <h2 className="card-title">{t("telegram.broadcast")}</h2>
          <div className="card-sub">{t("telegram.broadcastSub", { n: v.accounts })}</div>
        </div>
      </div>
      <textarea className="input" rows={4} maxLength={3500} value={text} onChange={(e) => setText(e.target.value)} placeholder={t("telegram.broadcastPlaceholder")} aria-label={t("telegram.broadcast")} disabled={!v.running} />
      <div className="mt-3 flex flex-wrap items-center gap-3">
        <Button variant="primary" disabled={!ready || !text.trim()} onClick={() => setConfirm(true)}>
          <Send size={16} aria-hidden /> {t("telegram.broadcastButton")}
        </Button>
        <span className="text-xs text-[var(--ink-500)]">{busy ? t("telegram.broadcasting") : !v.running ? t("telegram.broadcastOff") : t("telegram.broadcastHint")}</span>
      </div>
      {v.broadcast && <BroadcastProgress b={v.broadcast} />}
      <Confirm
        open={confirm}
        onOpenChange={setConfirm}
        title={t("telegram.broadcastTitle", { n: v.accounts })}
        text={t("telegram.broadcastText")}
        confirm={t("telegram.broadcastButton")}
        loading={send.isPending}
        onConfirm={() => send.mutate()}
      />
    </section>
  );
}

/** How far the last broadcast went; the bot sends up to 20 a second. */
function BroadcastProgress({ b }: { b: Schemas["TelegramBroadcast"] }) {
  const done = b.sent + b.failed;
  const sec = Math.ceil((b.total - done) / 20);
  const eta = sec < 60 ? t("telegram.broadcastEtaSec", { n: Math.max(1, sec) }) : t("telegram.broadcastEtaMin", { n: Math.ceil(sec / 60) });
  return (
    <div className="mt-4 rounded-2xl border border-[var(--hairline)] bg-[var(--glass-strong)] p-3" aria-live="polite">
      <div className="flex items-baseline justify-between gap-3 text-[13px]">
        <span className="font-medium">{b.active ? t("telegram.broadcastGoing") : t("telegram.broadcastLast", { when: ago(new Date(b.started * 1000).toISOString()) })}</span>
        <span className="tabular-nums text-[var(--ink-500)]">{t("telegram.broadcastCount", { done: num(b.sent), total: num(b.total) })}</span>
      </div>
      <div className="mt-2" role="progressbar" aria-label={t("telegram.broadcast")} aria-valuemin={0} aria-valuemax={b.total} aria-valuenow={done}>
        <Bar pct={b.total ? (done / b.total) * 100 : 100} />
      </div>
      {(b.active || b.failed > 0) && (
        <div className="mt-2 flex flex-wrap gap-x-3 gap-y-1 text-xs text-[var(--ink-500)]">
          {b.active && <span>{eta}</span>}
          {b.failed > 0 && <span>{t("telegram.broadcastFailed", { n: num(b.failed) })}</span>}
        </div>
      )}
    </div>
  );
}

/** The main menu as a subscriber sees it in Telegram, with sample data. */
function Preview({ draft, v }: { draft: Config; v: View }) {
  const settings = useSettings();
  const brand = settings.data?.brand || "VPN";
  const support = !!settings.data?.support_url;
  const sample: Record<string, string> = useMemo(
    () => ({
      brand,
      name: t("telegram.sample.name"),
      state: t("telegram.sample.state"),
      term: t("telegram.sample.term"),
      traffic: t("telegram.sample.traffic"),
      devices: t("telegram.sample.devices"),
      until: t("telegram.sample.until"),
      days: t("telegram.sample.days"),
      used: t("telegram.sample.used"),
      left: t("telegram.sample.left"),
      limit: t("telegram.sample.limit"),
      reset: t("telegram.sample.reset"),
    }),
    [brand],
  );
  const text = (draft.texts.main || v.defaults.main).replace(/\{(\w+)\}/g, (m, k: string) => sample[k] ?? m);
  const rows: string[][] = [];
  for (const b of draft.buttons) {
    if (!b.on || (b.action === "support" && !support) || (b.action === "app" && !(draft.mini_app && v.mini_app_url))) continue;
    const last = rows[rows.length - 1];
    if (b.row && last && last.length < 3) last.push(b.label);
    else rows.push([b.label]);
  }
  return (
    <section className="card glass" aria-label={t("telegram.preview")}>
      <div className="card-head">
        <div>
          <h2 className="card-title">{t("telegram.preview")}</h2>
          <div className="card-sub">{t("telegram.previewSub")}</div>
        </div>
      </div>
      <div className="tg-chat">
        <div className="tg-bubble">{text}</div>
        <div className="tg-keyboard">
          {rows.map((r, i) => (
            <div key={i} className="tg-row">
              {r.map((label, j) => (
                <span key={j} className="tg-btn">
                  {label}
                </span>
              ))}
            </div>
          ))}
        </div>
      </div>
      {!v.mini_app_url && draft.buttons.some((b) => b.action === "app" && b.on) ? (
        <p className="mt-3 flex items-start gap-2 text-xs text-[var(--ink-500)]">
          <TriangleAlert size={14} className="mt-0.5 shrink-0 text-[var(--honey-600)]" aria-hidden /> {t("telegram.miniAppNoCert")}
        </p>
      ) : null}
    </section>
  );
}
