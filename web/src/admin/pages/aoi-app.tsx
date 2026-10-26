import { keepPreviousData, useMutation, useQuery, useQueryClient } from "@tanstack/react-query";
import { useState, type FormEvent } from "react";
import { api, errorText, unwrap, type Schemas } from "../../api/client";
import { Card } from "../../components/layout";
import { Confirm, Drawer } from "../../components/overlay";
import { useToast } from "../../components/toast";
import { Button, EmptyState, ErrorState, Field, PageHeader, Pill, Segmented, Skeleton } from "../../components/ui";
import { t } from "../../i18n";
import { dateTime } from "../../lib/format";

// The AoiVPN fork's directives to the installed apps (aoiapp.Directives): the app asks for
// them at start and every few minutes, carries them out and reports back.

type Directive = Schemas["Directive"];
type Install = Schemas["Install"];
type Kind = Schemas["AoiDirectiveBody"]["kind"];
type Target = Schemas["AoiDirectiveBody"]["target"];

const KINDS: Kind[] = ["rewrite_url", "add_profile", "remove_profile", "refresh", "message"];
const DEFAULT_FROM = "https://sub.aoimusic.ru/sub/";

const qkDirectives = ["aoi", "directives"] as const;
const qkInstalls = (user: number, q: string) => ["aoi", "installs", user, q] as const;

const when = (unix: number) => dateTime(new Date(unix * 1000).toISOString());

function useDirectives() {
  return useQuery({
    queryKey: qkDirectives,
    queryFn: ({ signal }) => unwrap(api.GET("/api/v1/aoi/directives", { params: { query: { limit: 100 } }, signal })),
    refetchInterval: 15_000,
  });
}

function useInstalls(user: number, q: string) {
  return useQuery({
    queryKey: qkInstalls(user, q),
    queryFn: ({ signal }) => unwrap(api.GET("/api/v1/aoi/installs", { params: { query: { user_id: user || undefined, q: q || undefined, limit: 100 } }, signal })),
    placeholderData: keepPreviousData,
    refetchInterval: 30_000,
  });
}

export function AoiAppPage() {
  const [appTarget, setAppTarget] = useState("");
  return (
    <>
      <PageHeader title={t("aoiApp.title")} sub={t("aoiApp.sub")} />
      <div className="flex max-w-5xl flex-col gap-4">
        <SendCard appTarget={appTarget} />
        <HistoryCard />
        <InstallsCard onPick={setAppTarget} />
      </div>
    </>
  );
}

type Draft = {
  kind: Kind;
  target: Target;
  userID: string;
  appID: string;
  from: string;
  to: string;
  url: string;
  label: string;
  subUserID: string;
  title: string;
  text: string;
  hours: string;
  note: string;
};

const emptyDraft: Draft = { kind: "rewrite_url", target: "all", userID: "", appID: "", from: DEFAULT_FROM, to: "", url: "", label: "", subUserID: "", title: "", text: "", hours: "", note: "" };

const int = (s: string) => {
  const n = Number.parseInt(s.trim(), 10);
  return Number.isFinite(n) && n > 0 ? n : undefined;
};

function SendCard({ appTarget }: { appTarget: string }) {
  const qc = useQueryClient();
  const toast = useToast();
  const [d, setD] = useState<Draft>(emptyDraft);
  const [confirm, setConfirm] = useState(false);
  const [picked, setPicked] = useState("");
  // A device picked in the list below becomes the target.
  if (appTarget && appTarget !== picked) {
    setPicked(appTarget);
    setD((v) => ({ ...v, target: "app", appID: appTarget }));
  }
  const set = <K extends keyof Draft>(k: K, v: Draft[K]) => setD((x) => ({ ...x, [k]: v }));
  const send = useMutation({
    mutationFn: () => {
      const body: Schemas["AoiDirectiveBody"] = { kind: d.kind, target: d.target, note: d.note.trim() || undefined, expires_hours: int(d.hours) };
      if (d.target === "user") body.user_id = int(d.userID);
      if (d.target === "app") body.app_id = d.appID.trim();
      if (d.kind === "rewrite_url") Object.assign(body, { from: d.from.trim(), to: d.to.trim() });
      if (d.kind === "add_profile") Object.assign(body, { url: d.url.trim() || undefined, label: d.label.trim() || undefined });
      if (d.kind === "add_profile" || d.kind === "remove_profile" || d.kind === "refresh") body.sub_user_id = int(d.subUserID);
      if (d.kind === "message") Object.assign(body, { title: d.title.trim() || undefined, text: d.text.trim() });
      return unwrap(api.POST("/api/v1/aoi/directives", { body }));
    },
    onSuccess: () => {
      setConfirm(false);
      toast.ok(t("aoiApp.sent"));
      void qc.invalidateQueries({ queryKey: qkDirectives });
      setD((v) => ({ ...emptyDraft, kind: v.kind, target: v.target, userID: v.userID, appID: v.appID }));
    },
    onError: (e) => {
      setConfirm(false);
      toast.error(errorText(e));
    },
  });
  const submit = (ev: FormEvent) => {
    ev.preventDefault();
    if (d.target === "all") setConfirm(true);
    else send.mutate();
  };
  return (
    <Card title={t("aoiApp.send")} sub={t("aoiApp.sendSub")} i={1}>
      <form onSubmit={submit} noValidate className="flex flex-col gap-3">
        <Field label={t("aoiApp.kind")}>
          <Segmented label={t("aoiApp.kind")} value={d.kind} onChange={(k) => set("kind", k)} options={KINDS.map((k) => ({ value: k, label: t(`aoiApp.kinds.${k}`) }))} />
        </Field>
        <p className="text-xs text-[var(--ink-500)]">{t(`aoiApp.kindHints.${d.kind}`)}</p>

        {d.kind === "rewrite_url" ? (
          <div className="grid gap-3 sm:grid-cols-2">
            <Field label={t("aoiApp.from")} htmlFor="ad-from">
              <input id="ad-from" className="input mono" value={d.from} onChange={(e) => set("from", e.target.value)} spellCheck={false} />
            </Field>
            <Field label={t("aoiApp.to")} htmlFor="ad-to">
              <input id="ad-to" className="input mono" value={d.to} onChange={(e) => set("to", e.target.value)} spellCheck={false} placeholder="https://sub.example.com/sub/" />
            </Field>
          </div>
        ) : null}

        {d.kind === "add_profile" || d.kind === "remove_profile" || d.kind === "refresh" ? (
          <Field label={t("aoiApp.subUser")} htmlFor="ad-sub" hint={t(d.kind === "refresh" ? "aoiApp.subUserRefreshHint" : d.kind === "add_profile" ? "aoiApp.subUserAddHint" : "aoiApp.subUserHint")}>
            <input id="ad-sub" className="input mono" inputMode="numeric" value={d.subUserID} onChange={(e) => set("subUserID", e.target.value)} placeholder="42" />
          </Field>
        ) : null}

        {d.kind === "add_profile" ? (
          <div className="grid gap-3 sm:grid-cols-2">
            <Field label={t("aoiApp.url")} htmlFor="ad-url" hint={t("aoiApp.urlHint")}>
              <input id="ad-url" className="input mono" value={d.url} onChange={(e) => set("url", e.target.value)} spellCheck={false} placeholder="https://sub.aoimusic.ru/sub/…" />
            </Field>
            <Field label={t("aoiApp.label")} htmlFor="ad-label">
              <input id="ad-label" className="input" value={d.label} onChange={(e) => set("label", e.target.value)} placeholder="AoiVPN" />
            </Field>
          </div>
        ) : null}

        {d.kind === "message" ? (
          <>
            <Field label={t("aoiApp.msgTitle")} htmlFor="ad-title">
              <input id="ad-title" className="input" value={d.title} onChange={(e) => set("title", e.target.value)} maxLength={100} />
            </Field>
            <Field label={t("aoiApp.msgText")} htmlFor="ad-text">
              <textarea id="ad-text" className="input min-h-[96px]" value={d.text} onChange={(e) => set("text", e.target.value)} maxLength={2000} />
            </Field>
          </>
        ) : null}

        <Field label={t("aoiApp.target")}>
          <Segmented
            label={t("aoiApp.target")}
            value={d.target}
            onChange={(v) => set("target", v)}
            options={[
              { value: "all", label: t("aoiApp.targets.all") },
              { value: "user", label: t("aoiApp.targets.user") },
              { value: "app", label: t("aoiApp.targets.app") },
            ]}
          />
        </Field>
        {d.target === "user" ? (
          <Field label={t("aoiApp.userID")} htmlFor="ad-user" hint={t("aoiApp.userIDHint")}>
            <input id="ad-user" className="input mono" inputMode="numeric" value={d.userID} onChange={(e) => set("userID", e.target.value)} placeholder="42" />
          </Field>
        ) : null}
        {d.target === "app" ? (
          <Field label={t("aoiApp.appID")} htmlFor="ad-app" hint={t("aoiApp.appIDHint")}>
            <input id="ad-app" className="input mono" value={d.appID} onChange={(e) => set("appID", e.target.value)} spellCheck={false} placeholder="xxxxxxxx-xxxx-4xxx-xxxx-xxxxxxxxxxxx" />
          </Field>
        ) : null}

        <div className="grid gap-3 sm:grid-cols-2">
          <Field label={t("aoiApp.hours")} htmlFor="ad-hours" hint={t("aoiApp.hoursHint")}>
            <input id="ad-hours" className="input mono" inputMode="numeric" value={d.hours} onChange={(e) => set("hours", e.target.value)} placeholder="0" />
          </Field>
          <Field label={t("aoiApp.note")} htmlFor="ad-note">
            <input id="ad-note" className="input" value={d.note} onChange={(e) => set("note", e.target.value)} maxLength={300} />
          </Field>
        </div>
        <div>
          <Button variant="primary" type="submit" loading={send.isPending && !confirm}>
            {t("aoiApp.sendButton")}
          </Button>
        </div>
      </form>
      <Confirm open={confirm} onOpenChange={setConfirm} title={t("aoiApp.confirmAll")} text={t("aoiApp.confirmAllText")} confirm={t("aoiApp.sendButton")} loading={send.isPending} onConfirm={() => send.mutate()} />
    </Card>
  );
}

function summary(v: Directive): string {
  const p = v.params;
  switch (v.kind) {
    case "rewrite_url":
      return `${p.from} → ${p.to}`;
    case "add_profile":
      return p.url ?? "";
    case "remove_profile":
    case "refresh":
      return p.tokens?.length ? p.tokens.map((x) => `${x.slice(0, 6)}…`).join(", ") : t("aoiApp.allProfiles");
    case "message":
      return [p.title, p.text].filter(Boolean).join(": ");
    default:
      return "";
  }
}

function targetText(v: Directive): string {
  if (v.target === "user") return t("aoiApp.targetUser", { id: v.user_id ?? 0 });
  if (v.target === "app") return `${(v.app_id ?? "").slice(0, 8)}…`;
  return t("aoiApp.targets.all");
}

function stateOf(v: Directive): { tone: "ok" | "warn" | "bad" | "off"; text: string } {
  if (v.canceled_at) return { tone: "off", text: t("aoiApp.state.canceled") };
  if (v.expires_at && v.expires_at * 1000 < Date.now()) return { tone: "off", text: t("aoiApp.state.expired") };
  return { tone: "ok", text: t("aoiApp.state.active") };
}

function HistoryCard() {
  const q = useDirectives();
  const qc = useQueryClient();
  const toast = useToast();
  const [acksOf, setAcksOf] = useState<Directive | null>(null);
  const [cancelOf, setCancelOf] = useState<Directive | null>(null);
  const cancel = useMutation({
    mutationFn: (id: number) => unwrap(api.POST("/api/v1/aoi/directives/{id}/cancel", { params: { path: { id } } })),
    onSuccess: () => {
      setCancelOf(null);
      toast.ok(t("aoiApp.canceled"));
      void qc.invalidateQueries({ queryKey: qkDirectives });
    },
    onError: (e) => toast.error(errorText(e)),
  });
  return (
    <Card title={t("aoiApp.history")} sub={t("aoiApp.historySub")} i={2}>
      {q.isPending ? (
        <Skeleton style={{ height: 160 }} />
      ) : q.isError ? (
        <ErrorState text={errorText(q.error)} />
      ) : q.data.items.length === 0 ? (
        <EmptyState title={t("aoiApp.historyEmpty")} text={t("aoiApp.historyEmptyText")} />
      ) : (
        <div className="overflow-x-auto rounded-2xl border border-[var(--hairline)]">
          <table className="w-full text-left text-[13px]">
            <thead>
              <tr className="text-xs text-[var(--ink-500)]">
                <th className="px-3 py-2 font-medium">#</th>
                <th className="px-3 py-2 font-medium">{t("aoiApp.kind")}</th>
                <th className="px-3 py-2 font-medium">{t("aoiApp.target")}</th>
                <th className="px-3 py-2 font-medium">{t("aoiApp.what")}</th>
                <th className="px-3 py-2 font-medium">{t("aoiApp.created")}</th>
                <th className="px-3 py-2 font-medium">{t("aoiApp.result")}</th>
                <th className="px-3 py-2 font-medium" />
              </tr>
            </thead>
            <tbody className="divide-y divide-[var(--hairline)]">
              {q.data.items.map((v) => {
                const st = stateOf(v);
                return (
                  <tr key={v.id}>
                    <td className="px-3 py-2 mono text-[var(--ink-500)]">{v.id}</td>
                    <td className="px-3 py-2">{t(`aoiApp.kinds.${v.kind as Kind}`)}</td>
                    <td className="px-3 py-2">{targetText(v)}</td>
                    <td className="max-w-[280px] truncate px-3 py-2 mono" title={summary(v)}>
                      {summary(v)}
                      {v.note ? <div className="truncate font-sans text-xs text-[var(--ink-500)]">{v.note}</div> : null}
                    </td>
                    <td className="whitespace-nowrap px-3 py-2">{when(v.created_at)}</td>
                    <td className="whitespace-nowrap px-3 py-2">
                      <Pill tone={st.tone}>{st.text}</Pill>{" "}
                      <span className="text-xs text-[var(--ink-500)]">{t("aoiApp.counts", { done: v.done, failed: v.failed, skipped: v.skipped })}</span>
                    </td>
                    <td className="whitespace-nowrap px-3 py-2 text-right">
                      <Button size="sm" variant="ghost" onClick={() => setAcksOf(v)}>
                        {t("aoiApp.who")}
                      </Button>
                      {st.tone === "ok" ? (
                        <Button size="sm" variant="danger" onClick={() => setCancelOf(v)}>
                          {t("aoiApp.cancel")}
                        </Button>
                      ) : null}
                    </td>
                  </tr>
                );
              })}
            </tbody>
          </table>
        </div>
      )}
      <AcksDrawer directive={acksOf} onClose={() => setAcksOf(null)} />
      <Confirm
        open={!!cancelOf}
        onOpenChange={(o) => !o && setCancelOf(null)}
        title={t("aoiApp.cancelTitle")}
        text={t("aoiApp.cancelText")}
        confirm={t("aoiApp.cancel")}
        danger
        loading={cancel.isPending}
        onConfirm={() => cancelOf && cancel.mutate(cancelOf.id)}
      />
    </Card>
  );
}

function AcksDrawer({ directive, onClose }: { directive: Directive | null; onClose: () => void }) {
  const id = directive?.id ?? 0;
  const q = useQuery({
    queryKey: ["aoi", "acks", id],
    queryFn: ({ signal }) => unwrap(api.GET("/api/v1/aoi/directives/{id}/acks", { params: { path: { id } }, signal })),
    enabled: id > 0,
    refetchInterval: 15_000,
  });
  return (
    <Drawer open={!!directive} onOpenChange={(o) => !o && onClose()} title={t("aoiApp.whoTitle", { id })} meta={directive ? summary(directive) : undefined}>
      {q.isPending ? (
        <Skeleton style={{ height: 120 }} />
      ) : q.isError ? (
        <ErrorState text={errorText(q.error)} />
      ) : q.data.items.length === 0 ? (
        <p className="text-sm text-[var(--ink-500)]">{t("aoiApp.nobodyYet")}</p>
      ) : (
        <ul className="flex flex-col divide-y divide-[var(--hairline)] text-[13px]">
          {q.data.items.map((a) => (
            <li key={a.app_id} className="flex flex-col gap-1 py-2">
              <div className="flex items-center gap-2">
                <Pill tone={a.status === "done" ? "ok" : a.status === "failed" ? "bad" : "off"}>{t(`aoiApp.ack.${a.status as "done" | "failed" | "skipped"}`)}</Pill>
                <span className="mono">{a.app_id}</span>
              </div>
              <div className="text-xs text-[var(--ink-500)]">
                {when(a.at)}
                {a.detail ? ` · ${a.detail}` : ""}
              </div>
            </li>
          ))}
        </ul>
      )}
    </Drawer>
  );
}

function InstallsCard({ onPick }: { onPick: (appID: string) => void }) {
  const [user, setUser] = useState("");
  const [q, setQ] = useState("");
  const list = useInstalls(int(user) ?? 0, q.trim());
  return (
    <Card title={t("aoiApp.installs")} sub={t("aoiApp.installsSub")} i={3}>
      <div className="mb-3 grid gap-3 sm:grid-cols-2">
        <Field label={t("aoiApp.byUser")} htmlFor="ai-user">
          <input id="ai-user" className="input mono" inputMode="numeric" value={user} onChange={(e) => setUser(e.target.value)} placeholder="42" />
        </Field>
        <Field label={t("aoiApp.byApp")} htmlFor="ai-q">
          <input id="ai-q" className="input mono" value={q} onChange={(e) => setQ(e.target.value)} spellCheck={false} />
        </Field>
      </div>
      {list.isPending ? (
        <Skeleton style={{ height: 120 }} />
      ) : list.isError ? (
        <ErrorState text={errorText(list.error)} />
      ) : list.data.items.length === 0 ? (
        <p className="text-sm text-[var(--ink-500)]">{t("aoiApp.installsEmpty")}</p>
      ) : (
        <div className="overflow-x-auto rounded-2xl border border-[var(--hairline)]">
          <table className="w-full text-left text-[13px]">
            <thead>
              <tr className="text-xs text-[var(--ink-500)]">
                <th className="px-3 py-2 font-medium">app_id</th>
                <th className="px-3 py-2 font-medium">{t("aoiApp.version")}</th>
                <th className="px-3 py-2 font-medium">{t("aoiApp.profiles")}</th>
                <th className="px-3 py-2 font-medium">{t("aoiApp.users")}</th>
                <th className="px-3 py-2 font-medium">{t("aoiApp.lastSeen")}</th>
                <th className="px-3 py-2 font-medium" />
              </tr>
            </thead>
            <tbody className="divide-y divide-[var(--hairline)]">
              {list.data.items.map((i: Install) => (
                <tr key={i.app_id}>
                  <td className="px-3 py-2 mono" title={i.app_id}>
                    {i.app_id.slice(0, 13)}…
                  </td>
                  <td className="whitespace-nowrap px-3 py-2">
                    {i.app_version || "—"}
                    {i.platform ? <span className="text-xs text-[var(--ink-500)]"> · {i.platform}</span> : null}
                  </td>
                  <td className="px-3 py-2">{i.profiles}</td>
                  <td className="px-3 py-2 mono">{i.user_ids?.length ? i.user_ids.join(", ") : "—"}</td>
                  <td className="whitespace-nowrap px-3 py-2">{when(i.last_seen)}</td>
                  <td className="px-3 py-2 text-right">
                    <Button size="sm" variant="ghost" onClick={() => onPick(i.app_id)}>
                      {t("aoiApp.pick")}
                    </Button>
                  </td>
                </tr>
              ))}
            </tbody>
          </table>
        </div>
      )}
    </Card>
  );
}
