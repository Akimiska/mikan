import { useMutation, useQueryClient } from "@tanstack/react-query";
import { LoaderCircle, RefreshCw, ShieldCheck } from "lucide-react";
import { useState, type FormEvent } from "react";
import { api, errorText, unwrap, type Schemas } from "../../../api/client";
import { qk } from "../../../api/hooks";
import { AcmeAttempts, AcmeProblem, caName } from "../../../components/acme";
import { CertDrawer, certUntil, type CertInfo } from "../../../components/cert-drawer";
import { FormActions } from "../../../components/layout";
import { useToast } from "../../../components/toast";
import { Button, Field, Pill } from "../../../components/ui";
import { getLocale, t, tMaybe } from "../../../i18n";
import { useDraft } from "../../../lib/draft";
import { fieldErrors } from "../../../lib/fields";
import { useSaveSettings } from "./shared";

type Settings = Schemas["SettingsView"];

// A domain of its own for subscription links: the panel's address stays out of every link
// and the links survive a move of the panel. Requests to it reach the subscriptions alone.
export function SubDomainCard({ s }: { s: Settings }) {
  const save = useSaveSettings();
  const { draft, setDraft } = useDraft(s.sub_domain);
  const error = fieldErrors(save.error).sub_domain;
  const next = draft.trim().toLowerCase();
  const changed = next !== s.sub_domain;
  const submit = (e: FormEvent) => {
    e.preventDefault();
    if (changed) save.mutate({ sub_domain: next });
  };
  return (
    <section className="card glass reveal" style={{ "--i": 1 } as React.CSSProperties}>
      <form onSubmit={submit} noValidate>
        <div className="card-head">
          <div>
            <h2 className="card-title">{t("settings.subDomain.title")}</h2>
            <div className="card-sub">{t("settings.subDomain.sub")}</div>
          </div>
          {s.sub_domain ? <Pill tone="ok">{s.sub_domain}</Pill> : null}
        </div>
        <Field
          label={t("settings.subDomain.field")}
          htmlFor="s-sub-domain"
          hint={t("settings.subDomain.hint", { domain: s.domain || s.public_host || "—" })}
          error={error}
        >
          <input
            id="s-sub-domain"
            className="input mono max-w-[360px]"
            value={draft}
            onChange={(e) => setDraft(e.target.value)}
            placeholder="sub.example.com"
            aria-invalid={!!error}
            spellCheck={false}
            autoComplete="off"
          />
        </Field>
        <p className="text-xs text-[var(--ink-500)]">{t("settings.subDomain.note")}</p>
        <FormActions saving={save.isPending && save.variables?.sub_domain === next} disabled={!changed}>
          {s.sub_domain ? (
            <Button variant="ghost" loading={save.isPending && save.variables?.sub_domain === ""} onClick={() => save.mutate({ sub_domain: "" })}>
              {t("settings.subDomain.off")}
            </Button>
          ) : null}
        </FormActions>
      </form>
      {s.sub_domain && s.sub_certificate ? <SubCertificate s={s} c={s.sub_certificate} /> : null}
    </section>
  );
}

/** The subscription domain's own certificate: how it is, get it now, one of your own. */
function SubCertificate({ s, c }: { s: Settings; c: Schemas["Status"] }) {
  const qc = useQueryClient();
  const toast = useToast();
  const [own, setOwn] = useState(false);
  const [outcome, setOutcome] = useState<"got" | "running" | null>(null);
  const renew = useMutation({
    mutationFn: () => unwrap(api.POST("/api/v1/settings/sub-certificate/renew")),
    onMutate: () => setOutcome(null),
    onSuccess: (st) => {
      qc.setQueryData<Settings>(qk.settings, (old) => (old ? { ...old, sub_certificate: st } : old));
      setOutcome(st.ordering ? "running" : st.error ? null : "got");
      if (st.ordering) window.setTimeout(() => void qc.invalidateQueries({ queryKey: qk.settings }), 15_000);
    },
    onError: (e) => toast.error(errorText(e)),
  });
  const custom = c.kind === "custom";
  const ok = c.kind === "acme" || custom;
  const until = new Date(c.not_after).toLocaleString(getLocale(), { day: "numeric", month: "long", hour: "2-digit", minute: "2-digit" });
  const line = custom
    ? t("settings.certCustom", { names: (c.names ?? []).join(", "), until: certUntil(c.not_after) })
    : ok
      ? t("certs.issued", { ca: caName(c.ca), id: c.identifier, until })
      : t("settings.subDomain.certNone");
  const current: CertInfo | null = custom ? { names: c.names, issuer: c.issuer, not_after: c.not_after, trusted: c.trusted } : c.error?.startsWith("custom_") ? { error: c.error } : null;
  const failed = !!c.error && !c.error.startsWith("custom_") && c.error !== "no_public_host";
  return (
    <div className="mt-5 border-t border-[var(--line)] pt-4">
      <div className="mb-2 flex flex-wrap items-center justify-between gap-2">
        <h3 className="text-[13px] font-semibold">{t("settings.subDomain.cert")}</h3>
        {custom ? <Pill tone="ok">{t("settings.certOwn")}</Pill> : ok ? <Pill tone="ok">{t("settings.certValid")}</Pill> : <Pill tone="warn">{t("settings.subDomain.certPending")}</Pill>}
      </div>
      <p className="mb-3 text-xs text-[var(--ink-600)]">{line}</p>
      {c.ordering || renew.isPending ? (
        <p className="mb-3 flex items-center gap-2 text-[13px] text-[var(--ink-600)]" role="status">
          <LoaderCircle size={16} className="spin" aria-hidden /> {t("certs.ordering")}
        </p>
      ) : c.error ? (
        c.error.startsWith("custom_") ? (
          <p className="mb-3 text-[13px] text-[var(--berry-600)]" role="alert">
            {tMaybe(`errors.acme.${c.error}`) ?? c.error}
          </p>
        ) : (
          <AcmeProblem code={c.error} detail={c.error_detail} holder={c.holder} retryAt={c.retry_at} host={c.identifier} className="mb-3" />
        )
      ) : outcome ? (
        <p className="mb-3 text-[13px] text-[var(--leaf-700)]" role="status">
          {outcome === "got" ? t("certs.got") : t("certs.stillRunning")}
        </p>
      ) : null}
      <AcmeAttempts attempts={c.attempts} className="mb-3" />
      <div className="flex flex-wrap gap-2">
        {!custom ? (
          <Button size="sm" loading={renew.isPending} disabled={renew.isPending || c.ordering} onClick={() => renew.mutate()}>
            <RefreshCw size={16} aria-hidden /> {failed ? t("certs.retry") : t("certs.getNow")}
          </Button>
        ) : null}
        <Button size="sm" onClick={() => setOwn(true)}>
          <ShieldCheck size={16} aria-hidden /> {custom ? t("settings.certReplace") : t("settings.certOwnButton")}
        </Button>
      </div>
      <CertDrawer
        open={own}
        onClose={() => setOwn(false)}
        title={t("settings.subDomain.drawerTitle")}
        meta={s.sub_domain}
        lead={t("settings.subDomain.drawerLead", { domain: s.sub_domain })}
        current={current}
        save={(cert, key) => unwrap(api.PUT("/api/v1/settings/sub-certificate", { body: { cert, key } })).then((v) => qc.setQueryData(qk.settings, v))}
        clear={() =>
          unwrap(api.DELETE("/api/v1/settings/sub-certificate")).then(() => {
            void qc.invalidateQueries({ queryKey: qk.settings });
            window.setTimeout(() => void qc.invalidateQueries({ queryKey: qk.settings }), 15_000);
          })
        }
        clearLabel={t("cert.panelClear")}
        clearText={t("settings.subDomain.clearText")}
      />
    </div>
  );
}
