// "Support access" in Settings → Security: the admin lets a person into the panel for a few
// hours without giving them a password or a shell. The card says what the person can and
// cannot do, opens the access (a link shown once), shows who came in and what they did, ends
// it at once, and keeps the recordings of terminal sessions.
import { useMutation, useQuery, useQueryClient } from "@tanstack/react-query";
import { ChevronRight, ClipboardCopy, Copy, ExternalLink, FileVideo, LifeBuoy, Plus, ShieldAlert, Trash2 } from "lucide-react";
import { useState, type FormEvent } from "react";
import { api, errorText, unwrap, type Schemas } from "../../../api/client";
import { meQuery, useNodes } from "../../../api/hooks";
import { isLive, supportKey, useSupportGrants, type SupportAction, type SupportGrant } from "../../../api/support";
import { CastPlayer } from "../../../components/cast-player";
import { Confirm, Drawer, DrawerFooter } from "../../../components/overlay";
import { QueryBoundary } from "../../../components/query";
import { SwitchRow } from "../../../components/switch";
import { useToast } from "../../../components/toast";
import { Button, EmptyState, Field, Pill, QR, Segmented, Skeleton } from "../../../components/ui";
import { getLocale, t } from "../../../i18n";
import { useCopy } from "../../../lib/copy";
import { fieldErrors } from "../../../lib/fields";
import { ago, bytes, time, timeLeft } from "../../../lib/format";
import { nodeLabel, serverName } from "../../../lib/node-label";
import { useNow } from "../../../lib/now";

/** The support desk's bot: the link goes there. */
const SUPPORT_BOT = "https://t.me/MikanSup_bot";

type Level = SupportGrant["level"];
type Hours = 2 | 6 | 24;
const LEVELS: Level[] = ["view", "change", "terminal"];
const HOURS: Hours[] = [2, 6, 24];

export function SupportCard() {
  const grants = useSupportGrants();
  const [form, setForm] = useState(false);
  // The link of the access just opened. The panel keeps only its hash: it is shown here, once.
  const [made, setMade] = useState<string | null>(null);
  const [rec, setRec] = useState<{ source: string; name?: string } | null>(null);
  const g = grants.data?.grant ?? null;
  const live = isLive(g);
  const actions = grants.data?.actions ?? [];
  const hadTerminal = g?.level === "terminal" || actions.some((a) => a.action === "support.terminal.start");
  return (
    <section id="support" className="card glass reveal" aria-labelledby="support-title">
      <div className="card-head">
        <div>
          <h2 className="card-title" id="support-title">
            {t("support.card.title")}
          </h2>
          <div className="card-sub">{t("support.card.sub")}</div>
        </div>
        {grants.data ? live ? <Pill tone="ok">{t("support.card.open")}</Pill> : <Pill tone="off">{t("support.card.closed")}</Pill> : null}
      </div>
      <Scope />
      <QueryBoundary query={grants} pending={<Skeleton style={{ height: 112, borderRadius: 16 }} />} wrap={(state) => <div>{state}</div>}>
        {() => (
          <>
            {made ? <MadeLink link={made} onDone={() => setMade(null)} /> : null}
            {isLive(g) ? <ActiveGrant g={g} onReplace={() => setForm(true)} onRevoked={() => setMade(null)} /> : <Idle g={g} onOpen={() => setForm(true)} />}
            {hadTerminal ? (
              <div className="mt-4">
                <Button size="sm" onClick={() => setRec({ source: g?.terminal_targets[0] ?? "panel" })}>
                  <FileVideo size={16} aria-hidden /> {t("support.rec.open")}
                </Button>
              </div>
            ) : null}
            <Journal actions={actions} onRecording={(source, name) => setRec({ source, name })} />
          </>
        )}
      </QueryBoundary>
      <GrantDrawer
        open={form}
        replacing={live}
        onClose={() => setForm(false)}
        onMade={(link) => {
          setForm(false);
          setMade(link);
        }}
      />
      <RecordingsDrawer state={rec} onChange={setRec} />
    </section>
  );
}

/** What the person can and cannot do, said before anything is opened. */
function Scope() {
  const can = ["can1", "can2", "can3", "can4"] as const;
  const cannot = ["cannot1", "cannot2", "cannot3", "cannot4"] as const;
  return (
    <div className="mb-4 grid gap-3 sm:grid-cols-2">
      <div className="panel-soft p-3">
        <h3 className="mb-2 text-[13px] font-semibold text-[var(--ink-900)]">{t("support.scope.canTitle")}</h3>
        <ul className="list-disc space-y-1 pl-4 text-[13px] text-[var(--ink-600)]">
          {can.map((k) => (
            <li key={k}>{t(`support.scope.${k}`)}</li>
          ))}
        </ul>
      </div>
      <div className="panel-soft p-3">
        <h3 className="mb-2 text-[13px] font-semibold text-[var(--ink-900)]">{t("support.scope.cannotTitle")}</h3>
        <ul className="list-disc space-y-1 pl-4 text-[13px] text-[var(--ink-600)]">
          {cannot.map((k) => (
            <li key={k}>{t(`support.scope.${k}`)}</li>
          ))}
        </ul>
      </div>
    </div>
  );
}

/** No live access: what the last one was, and the way to open one. */
function Idle({ g, onOpen }: { g: SupportGrant | null; onOpen: () => void }) {
  return (
    <div>
      {g ? (
        <p className="mb-3 text-[13px] text-[var(--ink-600)]">
          {t("support.card.last", { level: t(`support.level.${g.level}`), how: g.status === "revoked" ? t("support.card.byYou") : t("support.card.byTime"), ago: ago(g.revoked_at ?? g.expires_at) })}
        </p>
      ) : (
        <p className="mb-3 text-[13px] text-[var(--ink-600)]">{t("support.card.never")}</p>
      )}
      <Button variant="primary" onClick={onOpen}>
        <LifeBuoy size={16} aria-hidden /> {t("support.card.openAccess")}
      </Button>
    </div>
  );
}

/** The access in force: its terms, whether the person has been in, and the way to end it. */
function ActiveGrant({ g, onReplace, onRevoked }: { g: SupportGrant; onReplace: () => void; onRevoked: () => void }) {
  const now = useNow(15_000);
  const qc = useQueryClient();
  const toast = useToast();
  const nodes = useNodes();
  const [confirm, setConfirm] = useState(false);
  const revoke = useMutation({
    mutationFn: () => unwrap(api.DELETE("/api/v1/support/grants/{id}", { params: { path: { id: g.id } } })),
    onSuccess: () => {
      setConfirm(false);
      onRevoked();
      void qc.invalidateQueries({ queryKey: supportKey.grants });
      toast.ok(t("support.revoked"));
    },
    onError: (e) => {
      setConfirm(false);
      toast.error(errorText(e));
    },
  });
  const seen = g.last_used_at ?? g.entered_at;
  const names = nodes.data?.map((n) => ({ id: n.id, name: nodeLabel(n) })) ?? [];
  return (
    <div>
      <dl className="mb-4 grid grid-cols-[minmax(0,auto)_minmax(0,1fr)] gap-x-4 gap-y-2 text-[13px]">
        <dt className="text-[var(--ink-500)]">{t("support.card.level")}</dt>
        <dd className="font-medium">{t(`support.level.${g.level}`)}</dd>
        <dt className="text-[var(--ink-500)]">{t("support.card.until")}</dt>
        <dd className="font-medium">
          {time(g.expires_at)} <span className="font-normal text-[var(--ink-500)]">({t("support.left", { left: timeLeft(g.expires_at, now) })})</span>
        </dd>
        <dt className="text-[var(--ink-500)]">{t("support.card.people")}</dt>
        <dd className="font-medium">{g.show_people ? t("support.card.peopleShown") : t("support.card.peopleHidden")}</dd>
        {g.level === "terminal" ? (
          <>
            <dt className="text-[var(--ink-500)]">{t("support.card.servers")}</dt>
            <dd className="font-medium">{g.terminal_targets.map((x) => serverName(x, names)).join(", ")}</dd>
          </>
        ) : null}
        {g.note ? (
          <>
            <dt className="text-[var(--ink-500)]">{t("support.card.note")}</dt>
            <dd className="break-words">{g.note}</dd>
          </>
        ) : null}
        <dt className="text-[var(--ink-500)]">{t("support.card.operator")}</dt>
        <dd>
          {seen ? (
            <>
              {t("support.operatorSeen", { ago: ago(seen, now) })}
              {g.last_ip ? <span className="mono"> {g.last_ip}</span> : null}
            </>
          ) : (
            t("support.operatorNot")
          )}
        </dd>
      </dl>
      <div className="flex flex-wrap gap-2">
        <Button variant="danger" onClick={() => setConfirm(true)}>
          <Trash2 size={16} aria-hidden /> {t("support.revoke")}
        </Button>
        <Button onClick={onReplace}>
          <Plus size={16} aria-hidden /> {t("support.card.replace")}
        </Button>
      </div>
      <Confirm
        open={confirm}
        onOpenChange={setConfirm}
        title={t("support.card.revokeTitle")}
        text={t("support.card.revokeText")}
        confirm={t("support.revoke")}
        danger
        loading={revoke.isPending}
        onConfirm={() => revoke.mutate()}
      />
    </div>
  );
}

/** The access link, shown once: copied, as a QR, or sent on to the support bot. */
function MadeLink({ link, onDone }: { link: string; onDone: () => void }) {
  const copyText = useCopy();
  const copy = () => copyText(link, t("support.link.copied"));
  return (
    <div className="panel-soft mb-4 p-4" role="region" aria-label={t("support.link.title")}>
      <div className="banner warn mb-3">
        <ShieldAlert size={18} className="shrink-0" aria-hidden />
        <span>{t("support.link.once")}</span>
      </div>
      <div className="link-field">
        <span className="mono" title={link}>
          {link}
        </span>
        <button type="button" className="icon-btn" onClick={() => void copy()} aria-label={t("support.link.copy")}>
          <Copy size={18} aria-hidden />
        </button>
      </div>
      <div className="mt-3 grid gap-4 sm:grid-cols-[136px_minmax(0,1fr)]">
        <QR value={link} size={136} label={t("support.link.qr")} />
        <div className="flex flex-col gap-3">
          <p className="text-[13px] text-[var(--ink-600)]">{t("support.link.how")}</p>
          <div className="flex flex-wrap gap-2">
            <Button
              variant="primary"
              onClick={() =>
                void copy().then((ok) => {
                  if (ok) window.open(SUPPORT_BOT, "_blank", "noopener,noreferrer");
                })
              }
            >
              <ExternalLink size={16} aria-hidden /> {t("support.link.toBot")}
            </Button>
            <Button variant="ghost" onClick={onDone}>
              {t("support.link.done")}
            </Button>
          </div>
          <p className="text-xs text-[var(--ink-500)]">{t("support.link.lost")}</p>
        </div>
      </div>
    </div>
  );
}

/* ----------------------------------------------------------------- journal */

type Details = Record<string, unknown>;
const detailsOf = (a: SupportAction): Details => (a.details && typeof a.details === "object" ? (a.details as Details) : {});
const str = (v: unknown) => (typeof v === "string" ? v : "");
const num = (v: unknown) => (typeof v === "number" ? v : 0);

/** One line of the journal in words; an action the operator took that has no words is its code. */
function actionText(a: SupportAction, names: { id: number; name: string }[]): string {
  const d = detailsOf(a);
  switch (a.action) {
    case "support.grant":
      return t("support.journal.grant", { level: t(`support.level.${(str(d.level) || "view") as Level}`), hours: num(d.hours) });
    case "support.enter":
      return t("support.journal.enter", { via: str(d.via) === "api" ? t("support.journal.viaApi") : t("support.journal.viaBrowser") });
    case "support.revoke":
      return str(d.reason) === "replaced" ? t("support.journal.replaced") : t("support.journal.revoke");
    case "support.expire":
      return t("support.journal.expire");
    case "support.terminal.start":
      return t("support.journal.termStart", { server: serverName(str(d.target) || "panel", names) });
    case "support.terminal.end":
      return t("support.journal.termEnd", {
        server: serverName(str(d.target) || "panel", names),
        sec: num(d.seconds),
      });
    default:
      return a.target_id ? `${a.action} · ${a.target_type} #${a.target_id}` : a.action;
  }
}

/** The lines that are the operator's doing whatever the journal wrote beside them. */
const OPERATOR_LINES = ["support.enter", "support.terminal.start", "support.terminal.end"];

/** Whether the line is the operator's own doing (it names a support actor). */
const byOperator = (a: SupportAction) => str(detailsOf(a).actor).startsWith("support:");

function Journal({ actions, onRecording }: { actions: SupportAction[]; onRecording: (source: string, name: string) => void }) {
  const nodes = useNodes();
  const names = nodes.data?.map((n) => ({ id: n.id, name: nodeLabel(n) })) ?? [];
  if (actions.length === 0) return null;
  return (
    <details className="disclosure mt-4">
      <summary>
        <ChevronRight size={16} aria-hidden />
        <span className="disclosure-title">{t("support.journal.title", { n: actions.length })}</span>
      </summary>
      <div className="disclosure-body">
        <p className="mb-2 text-xs text-[var(--ink-500)]">{t("support.journal.lead")}</p>
        <ul className="row-list max-h-[360px] overflow-y-auto" tabIndex={0} aria-label={t("support.journal.title", { n: actions.length })}>
          {actions.map((a) => {
            const rec = a.action === "support.terminal.end" ? str(detailsOf(a).recording) : "";
            return (
              <li key={a.id} className="flex flex-wrap items-center justify-between gap-x-3 gap-y-1 py-2">
                <div className="min-w-0 flex-1">
                  <div className="break-words text-[13px] text-[var(--ink-900)]">
                    {byOperator(a) || OPERATOR_LINES.includes(a.action) ? <span className="tag mr-2">{t("support.operator")}</span> : null}
                    {actionText(a, names)}
                  </div>
                  <div className="text-xs text-[var(--ink-500)]">
                    {new Date(a.at).toLocaleString(getLocale(), { day: "numeric", month: "short", hour: "2-digit", minute: "2-digit", second: "2-digit" })}
                    {a.ip ? <span className="mono"> · {a.ip}</span> : null}
                  </div>
                </div>
                {rec ? (
                  <Button size="sm" variant="ghost" onClick={() => onRecording(str(detailsOf(a).target) || "panel", rec)}>
                    <FileVideo size={16} aria-hidden /> {t("support.rec.watch")}
                  </Button>
                ) : null}
              </li>
            );
          })}
        </ul>
      </div>
    </details>
  );
}

/* ----------------------------------------------------------------- the form */

function GrantDrawer({ open, replacing, onClose, onMade }: { open: boolean; replacing: boolean; onClose: () => void; onMade: (link: string) => void }) {
  const qc = useQueryClient();
  const nodes = useNodes();
  const me = useQuery(meQuery);
  const totp = !!me.data?.admin.totp_enabled;
  const [hours, setHours] = useState<Hours>(2);
  const [level, setLevel] = useState<Level>("view");
  const [people, setPeople] = useState(false);
  const [note, setNote] = useState("");
  const [targets, setTargets] = useState<string[]>([]);
  const [password, setPassword] = useState("");
  const [code, setCode] = useState("");
  const [understood, setUnderstood] = useState(false);
  const terminal = level === "terminal";
  const servers = [{ id: "panel", label: t("diag.serverPanel") }, ...(nodes.data ?? []).filter((n) => !n.local).map((n) => ({ id: `node:${n.id}`, label: nodeLabel(n) }))];
  const create = useMutation({
    mutationFn: () =>
      unwrap(
        api.POST("/api/v1/support/grants", {
          body: { hours, level, show_people: people, note: note.trim() || undefined, ...(terminal ? { terminal_targets: targets, password, totp: code || undefined } : {}) },
        }),
      ),
    onSuccess: (r) => {
      void qc.invalidateQueries({ queryKey: supportKey.grants });
      reset();
      // A link that starts with "/" has no public address behind it: the page's own is used.
      onMade(new URL(r.link, window.location.origin).href);
    },
  });
  const reset = () => {
    setHours(2);
    setLevel("view");
    setPeople(false);
    setNote("");
    setTargets([]);
    setPassword("");
    setCode("");
    setUnderstood(false);
    create.reset();
  };
  const errors = fieldErrors(create.error);
  const ready = !terminal || (targets.length > 0 && !!password && (!totp || code.length === 6) && understood);
  const submit = (e: FormEvent) => {
    e.preventDefault();
    if (ready && !create.isPending) create.mutate();
  };
  const close = () => {
    reset();
    onClose();
  };
  const toggle = (id: string) => setTargets((cur) => (cur.includes(id) ? cur.filter((x) => x !== id) : [...cur, id]));
  return (
    <Drawer
      open={open}
      onOpenChange={(v) => !v && close()}
      title={t("support.form.title")}
      footer={
        <DrawerFooter form="support-grant" onCancel={close} loading={create.isPending} disabled={!ready}>
          {t("support.form.submit")}
        </DrawerFooter>
      }
    >
      <form id="support-grant" onSubmit={submit} className="pt-5" noValidate>
        {replacing ? (
          <div className="banner warn mb-4" role="note">
            <ShieldAlert size={18} className="shrink-0" aria-hidden />
            <span>{t("support.form.replaces")}</span>
          </div>
        ) : null}
        {create.error && !Object.keys(errors).length ? <div className="banner err mb-4" role="alert">{errorText(create.error)}</div> : null}
        <Field label={t("support.form.hours")}>
          <Segmented<`${Hours}`> label={t("support.form.hours")} value={`${hours}`} onChange={(v) => setHours(Number(v) as Hours)} options={HOURS.map((h) => ({ value: `${h}` as const, label: t("support.form.hoursN", { n: h }) }))} />
        </Field>
        <Field label={t("support.form.level")} hint={t(`support.form.levelHint.${level}`)}>
          <Segmented<Level> label={t("support.form.level")} value={level} onChange={setLevel} options={LEVELS.map((l) => ({ value: l, label: t(`support.level.${l}`) }))} />
        </Field>
        <SwitchRow className="mb-4" label={t("support.form.people")} sub={people ? t("support.form.peopleOn") : t("support.form.peopleOff")} warn={people} checked={people} onChange={setPeople} />
        <Field label={t("support.form.note")} htmlFor="support-note" hint={t("support.form.noteHint")} error={errors.note}>
          <input id="support-note" className="input" value={note} maxLength={200} onChange={(e) => setNote(e.target.value)} autoComplete="off" aria-invalid={!!errors.note} />
        </Field>
        {terminal ? (
          <fieldset className="mb-4 rounded-2xl border border-[var(--berry-600)] p-4">
            <legend className="px-2 text-[13px] font-semibold text-[var(--berry-600)]">{t("support.form.termTitle")}</legend>
            <div className="banner err mb-4" role="note">
              <ShieldAlert size={18} className="shrink-0" aria-hidden />
              <span>{t("support.form.termWarn")}</span>
            </div>
            <Field label={t("support.form.targets")} hint={t("support.form.targetsHint")} error={errors.terminal_targets}>
              <div className="flex flex-col gap-2">
                {servers.map((s) => (
                  <label key={s.id} className="flex items-center gap-2 text-[13px]">
                    <input type="checkbox" className="check shrink-0" checked={targets.includes(s.id)} onChange={() => toggle(s.id)} />
                    <span className="min-w-0 truncate">{s.label}</span>
                  </label>
                ))}
              </div>
            </Field>
            <Field label={t("login.password")} htmlFor="support-pw" hint={t("support.form.passwordHint")} error={errors.password}>
              <input id="support-pw" type="password" className="input" value={password} onChange={(e) => setPassword(e.target.value)} autoComplete="current-password" aria-invalid={!!errors.password} />
            </Field>
            {totp ? (
              <Field label={t("settings.totpCode")} htmlFor="support-totp" error={errors.totp}>
                <input id="support-totp" className="input mono max-w-[160px] tracking-[0.2em]" inputMode="numeric" maxLength={6} value={code} onChange={(e) => setCode(e.target.value.trim())} autoComplete="one-time-code" aria-invalid={!!errors.totp} />
              </Field>
            ) : null}
            <label className="flex items-start gap-2 text-[13px] text-[var(--ink-700)]">
              <input type="checkbox" className="check mt-0.5 shrink-0" checked={understood} onChange={(e) => setUnderstood(e.target.checked)} />
              <span>{t("support.form.understood")}</span>
            </label>
          </fieldset>
        ) : null}
      </form>
    </Drawer>
  );
}

/* ----------------------------------------------------------------- recordings */

type Rec = Schemas["RecordingInfo"];

/** The terminal sessions kept on a server and the player for one of them (admin only). */
function RecordingsDrawer({ state, onChange }: { state: { source: string; name?: string } | null; onChange: (s: { source: string; name?: string } | null) => void }) {
  const nodes = useNodes();
  const source = state?.source ?? "panel";
  const name = state?.name;
  const servers = [{ id: "panel", label: t("diag.serverPanel") }, ...(nodes.data ?? []).filter((n) => !n.local).map((n) => ({ id: `node:${n.id}`, label: nodeLabel(n) }))];
  const list = useQuery({
    queryKey: supportKey.recordings(source),
    queryFn: ({ signal }) => unwrap(api.GET("/api/v1/support/recordings", { params: { query: { source } }, signal })),
    enabled: !!state,
  });
  return (
    <Drawer
      wide
      open={!!state}
      onOpenChange={(v) => !v && onChange(null)}
      title={name ? t("support.rec.viewTitle") : t("support.rec.title")}
      meta={name ? `${serverName(source, nodes.data?.map((n) => ({ id: n.id, name: nodeLabel(n) })) ?? [])} · ${name}` : undefined}
      footer={
        name ? (
          <Button variant="ghost" onClick={() => onChange({ source })}>
            {t("support.rec.back")}
          </Button>
        ) : undefined
      }
    >
      <div className="pt-5">
        {name ? (
          <RecordingView source={source} name={name} />
        ) : (
          <>
            <p className="mb-4 text-[13px] text-[var(--ink-600)]">{t("support.rec.lead")}</p>
            <Field label={t("diag.server")} htmlFor="rec-source">
              <select id="rec-source" className="input" value={source} onChange={(e) => onChange({ source: e.target.value })}>
                {servers.map((s) => (
                  <option key={s.id} value={s.id}>
                    {s.label}
                  </option>
                ))}
              </select>
            </Field>
            <QueryBoundary query={list} pending={<Skeleton style={{ height: 120, borderRadius: 16 }} />}>
              {(d) =>
                d.recordings.length === 0 ? (
                  <EmptyState icon={FileVideo} title={t("support.rec.emptyTitle")} text={t("support.rec.emptyText")} />
                ) : (
                  <ul className="row-list">
                    {d.recordings.map((r) => (
                      <RecordingRow key={r.name} r={r} onWatch={() => onChange({ source, name: r.name })} />
                    ))}
                  </ul>
                )
              }
            </QueryBoundary>
          </>
        )}
      </div>
    </Drawer>
  );
}

function RecordingRow({ r, onWatch }: { r: Rec; onWatch: () => void }) {
  return (
    <li className="flex flex-wrap items-center justify-between gap-3 py-3">
      <div className="min-w-0">
        <div className="text-[13px] font-medium">{new Date(r.at).toLocaleString(getLocale(), { day: "numeric", month: "long", hour: "2-digit", minute: "2-digit" })}</div>
        <div className="text-xs text-[var(--ink-500)]">
          {t("support.rec.meta", { grant: r.grant_id, size: bytes(r.size) })}
        </div>
      </div>
      <Button size="sm" onClick={onWatch}>
        <FileVideo size={16} aria-hidden /> {t("support.rec.watch")}
      </Button>
    </li>
  );
}

/** The file is fetched when it is opened; it is a text of at most a few megabytes. */
function RecordingView({ source, name }: { source: string; name: string }) {
  const copyText = useCopy();
  const file = useQuery({
    queryKey: supportKey.recording(source, name),
    queryFn: ({ signal }) => unwrap(api.GET("/api/v1/support/recordings/{name}", { params: { path: { name }, query: { source } }, parseAs: "text", signal })),
    staleTime: Infinity,
    gcTime: 60_000,
  });
  return (
    <QueryBoundary query={file} pending={<Skeleton style={{ height: 360, borderRadius: 12 }} />}>
      {(text) => (
        <>
          <CastPlayer text={text as unknown as string} name={name} />
          <div className="mt-3">
            <Button size="sm" variant="ghost" onClick={() => void copyText(name, t("support.rec.nameCopied"))}>
              <ClipboardCopy size={16} aria-hidden /> {t("support.rec.copyName")}
            </Button>
          </div>
        </>
      )}
    </QueryBoundary>
  );
}
