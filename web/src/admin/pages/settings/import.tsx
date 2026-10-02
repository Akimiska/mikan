import { useMutation, useQuery, useQueryClient } from "@tanstack/react-query";
import { useState, type FormEvent } from "react";
import { api, ApiError, errorText, unwrap, type Schemas } from "../../../api/client";
import { useTariffs } from "../../../api/hooks";
import { useToast } from "../../../components/toast";
import { Button, Field } from "../../../components/ui";
import { t } from "../../../i18n";
import { useDraft } from "../../../lib/draft";
import { fieldErrors } from "../../../lib/fields";

type Kind = "marzban" | "pasarguard" | "remnawave";
const KINDS: Kind[] = ["marzban", "pasarguard", "remnawave"];
// Where each panel keeps its subscription links, for the old-links path.
const OLD_PATH: Record<Kind, string> = { marzban: "sub", pasarguard: "sub", remnawave: "api/sub" };

/** Users from another panel: a preview first, then the import onto a chosen plan. */
export function ImportCard() {
  const qc = useQueryClient();
  const toast = useToast();
  const tariffs = useTariffs();
  const [src, setSrc] = useState({ kind: "marzban" as Kind, url: "", username: "", password: "", token: "" });
  const [tariff, setTariff] = useState<number | "">("");
  const [preview, setPreview] = useState<Schemas["Preview"] | null>(null);
  const [report, setReport] = useState<Schemas["Report"] | null>(null);
  const body = () => ({ kind: src.kind, url: src.url.trim(), username: src.username.trim(), password: src.password, token: src.token.trim() });
  const check = useMutation({
    mutationFn: () => unwrap(api.POST("/api/v1/import/preview", { body: body() })),
    onSuccess: (p) => {
      setPreview(p);
      setReport(null);
    },
  });
  const run = useMutation({
    mutationFn: () => unwrap(api.POST("/api/v1/import", { body: { ...body(), tariff_id: Number(tariff) } })),
    onSuccess: (r) => {
      setReport(r);
      setPreview(null);
      void qc.invalidateQueries();
      toast.ok(t("settings.import.done", { n: r.created }));
    },
    onError: (e) => toast.error(errorText(e)),
  });
  const errors = fieldErrors(check.error ?? run.error);
  const generic = (check.error ?? run.error) && Object.keys(errors).length === 0 ? errorText(check.error ?? run.error) : "";
  const set = (k: keyof typeof src) => (e: React.ChangeEvent<HTMLInputElement | HTMLSelectElement>) => {
    setSrc((s) => ({ ...s, [k]: e.target.value }));
    setPreview(null);
  };
  const submit = (e: FormEvent) => {
    e.preventDefault();
    check.mutate();
  };
  const usesPassword = src.kind !== "remnawave";
  return (
    <section className="card glass reveal" style={{ "--i": 1 } as React.CSSProperties}>
      <form onSubmit={submit} noValidate>
        <div className="card-head">
          <div>
            <h2 className="card-title">{t("settings.import.title")}</h2>
            <div className="card-sub">{t("settings.import.sub")}</div>
          </div>
        </div>
        <Field label={t("settings.import.kind")} htmlFor="imp-kind">
          <select id="imp-kind" className="input" value={src.kind} onChange={set("kind")}>
            {KINDS.map((k) => (
              <option key={k} value={k}>
                {t(`settings.import.kinds.${k}`)}
              </option>
            ))}
          </select>
        </Field>
        <Field label={t("settings.import.url")} htmlFor="imp-url" hint={t("settings.import.urlHint")} error={errors.url}>
          <input id="imp-url" className="input" value={src.url} onChange={set("url")} placeholder="https://panel.example.com" autoComplete="off" aria-invalid={!!errors.url} />
        </Field>
        {usesPassword ? (
          <div className="grid gap-x-3 sm:grid-cols-2">
            <Field label={t("settings.import.username")} htmlFor="imp-user">
              <input id="imp-user" className="input" value={src.username} onChange={set("username")} autoComplete="off" />
            </Field>
            <Field label={t("settings.import.password")} htmlFor="imp-pass" error={errors.password}>
              <input id="imp-pass" className="input" type="password" value={src.password} onChange={set("password")} autoComplete="new-password" aria-invalid={!!errors.password} />
            </Field>
          </div>
        ) : null}
        {src.kind !== "marzban" ? (
          <Field label={t(src.kind === "remnawave" ? "settings.import.token" : "settings.import.apiKey")} htmlFor="imp-token" hint={t(src.kind === "remnawave" ? "settings.import.tokenHint" : "settings.import.apiKeyHint")} error={errors.token}>
            <input id="imp-token" className="input mono" type="password" value={src.token} onChange={set("token")} autoComplete="off" aria-invalid={!!errors.token} />
          </Field>
        ) : null}
        {generic ? <div className="banner bad mb-3">{generic}</div> : null}
        {!preview ? (
          <Button type="submit" variant="primary" loading={check.isPending} disabled={!src.url.trim()}>
            {t("settings.import.check")}
          </Button>
        ) : (
          <div className="panel-soft mb-3 p-3 text-[13px]">
            <div className="font-medium">{t("settings.import.found", { total: preview.total, n: preview.new })}</div>
            {preview.taken.length ? <div className="mt-1 text-xs text-[var(--ink-500)]">{t("settings.import.taken", { list: preview.taken.slice(0, 20).join(", ") + (preview.taken.length > 20 ? "…" : "") })}</div> : null}
            {preview.on_hold ? <div className="mt-1 text-xs text-[var(--ink-500)]">{t("settings.import.onHold", { n: preview.on_hold })}</div> : null}
            <Field label={t("settings.import.tariff")} htmlFor="imp-tariff" hint={t("settings.import.tariffHint")} error={errors.tariff_id}>
              <select id="imp-tariff" className="input" value={tariff} onChange={(e) => setTariff(e.target.value ? Number(e.target.value) : "")}>
                <option value="">—</option>
                {(tariffs.data ?? []).map((tr) => (
                  <option key={tr.id} value={tr.id}>
                    {tr.name}
                  </option>
                ))}
              </select>
            </Field>
            <div className="flex gap-2">
              <Button type="button" variant="primary" loading={run.isPending} disabled={tariff === "" || preview.new === 0} onClick={() => run.mutate()}>
                {t("settings.import.run", { n: preview.new })}
              </Button>
              <Button type="button" variant="ghost" onClick={() => setPreview(null)}>
                {t("common.cancel")}
              </Button>
            </div>
          </div>
        )}
        {report ? (
          <div className="panel-soft mt-3 p-3 text-[13px]">
            <div className="font-medium">{t("settings.import.report", { created: report.created, links: report.links })}</div>
            {report.skipped.length ? <div className="mt-1 text-xs text-[var(--ink-500)]">{t("settings.import.skipped", { n: report.skipped.length })}</div> : null}
            {report.failed.length ? (
              <ul className="mt-1 text-xs text-[var(--berry-600)]">
                {report.failed.slice(0, 20).map((f) => (
                  <li key={f}>{f}</li>
                ))}
              </ul>
            ) : null}
          </div>
        ) : null}
      </form>
    </section>
  );
}

/** The old panel's links: its path here, and for Marzban and PasarGuard its signing secret. */
export function LegacyLinksCard() {
  const qc = useQueryClient();
  const toast = useToast();
  const q = useQuery({ queryKey: ["legacy-links"], queryFn: () => unwrap(api.GET("/api/v1/import/legacy", {})) });
  const v = q.data;
  const { draft, setDraft } = useDraft({ path: v?.path ?? "", kind: (v?.kind || "") as Kind | "" });
  const [secret, setSecret] = useState("");
  const save = useMutation({
    mutationFn: (body: { path?: string; kind?: Kind | ""; secret?: string }) => unwrap(api.PATCH("/api/v1/import/legacy", { body })),
    onSuccess: (r) => {
      qc.setQueryData(["legacy-links"], r);
      setSecret("");
      toast.ok(t("settings.import.legacySaved"));
    },
    onError: (e) => {
      if (!(e instanceof ApiError && Object.keys(e.fields).length)) toast.error(errorText(e));
    },
  });
  const errors = fieldErrors(save.error);
  if (!v) return null;
  const signed = draft.kind === "marzban" || draft.kind === "pasarguard";
  return (
    <section className="card glass reveal" style={{ "--i": 2 } as React.CSSProperties}>
      <div className="card-head">
        <div>
          <h2 className="card-title">{t("settings.import.legacy")}</h2>
          <div className="card-sub">{t("settings.import.legacySub")}</div>
        </div>
      </div>
      <Field label={t("settings.import.legacyKind")} htmlFor="leg-kind">
        <select id="leg-kind" className="input" value={draft.kind} onChange={(e) => setDraft((d) => ({ ...d, kind: e.target.value as Kind | "", path: d.path || (e.target.value ? OLD_PATH[e.target.value as Kind] : "") }))}>
          <option value="">—</option>
          {KINDS.map((k) => (
            <option key={k} value={k}>
              {t(`settings.import.kinds.${k}`)}
            </option>
          ))}
        </select>
      </Field>
      <Field label={t("settings.import.legacyPath")} htmlFor="leg-path" hint={t("settings.import.legacyPathHint")} error={errors.path}>
        <input id="leg-path" className="input mono" value={draft.path} onChange={(e) => setDraft((d) => ({ ...d, path: e.target.value }))} placeholder="sub" autoComplete="off" aria-invalid={!!errors.path} />
      </Field>
      {signed ? (
        <Field label={t("settings.import.secret")} htmlFor="leg-secret" hint={v.secret_set ? t("settings.import.secretSet") : t("settings.import.secretHint")}>
          <input id="leg-secret" className="input mono" type="password" value={secret} onChange={(e) => setSecret(e.target.value)} autoComplete="off" />
        </Field>
      ) : null}
      <div className="flex flex-wrap items-center justify-between gap-3">
        <span className="text-xs text-[var(--ink-500)]">{t("settings.import.legacyCount", { n: v.links })}</span>
        <Button
          variant="primary"
          loading={save.isPending}
          onClick={() =>
            save.mutate({
              path: draft.path.trim(),
              kind: draft.kind,
              ...(signed && secret ? { secret } : {}),
            })
          }
        >
          {t("common.save")}
        </Button>
      </div>
    </section>
  );
}
