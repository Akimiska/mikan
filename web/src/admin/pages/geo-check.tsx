// Which country an address is in for the public geo bases: sites and apps pick a client's
// region by them, and they disagree.
import { useMutation } from "@tanstack/react-query";
import { MapPin } from "lucide-react";
import { useState, type FormEvent } from "react";
import { api, errorText, unwrap, type Schemas } from "../../api/client";
import { useNodes } from "../../api/hooks";
import { Button, EmptyState, Field, Skeleton } from "../../components/ui";
import { t } from "../../i18n";
import { ago } from "../../lib/format";
import { countryName, StatusMark } from "./node-check";

type Geo = Schemas["GeoCheck"];
type Node = Schemas["NodeInfo"];

/** 🇩🇪 for DE: two regional indicator letters. */
function flag(code: string): string {
  return String.fromCodePoint(...[...code.toUpperCase()].map((c) => 0x1f1e6 + c.charCodeAt(0) - 65));
}

/** The verdict, then each base's answer, a disagreeing one marked. */
export function GeoView({ res }: { res: Geo }) {
  const answered = res.answers.filter((a) => a.country);
  return (
    <div className="flex flex-col gap-3">
      {res.country ? (
        <div className={`banner ${res.agree ? "info" : "warn"}`} role="status">
          <span aria-hidden>{flag(res.country)}</span>
          <span>
            {res.agree
              ? t("geo.agree", { country: countryName(res.country), n: answered.length })
              : t("geo.disagree", { country: countryName(res.country), n: answered.filter((a) => a.country === res.country).length, total: answered.length })}
          </span>
        </div>
      ) : (
        <div className="banner err" role="status">
          {t("geo.none")}
        </div>
      )}
      <ul className="flex flex-col divide-y divide-[var(--hairline)] rounded-2xl border border-[var(--hairline)]">
        {res.answers.map((a) => (
          <li key={a.service} className="flex items-center gap-3 px-4 py-2 text-[13px]">
            <StatusMark status={!a.country ? "skip" : a.country === res.country ? "ok" : "warn"} />
            <span className="min-w-0 flex-1 truncate text-[var(--ink-700)]">{a.service}</span>
            {a.country ? (
              <span className="whitespace-nowrap">
                <span aria-hidden>{flag(a.country)} </span>
                {countryName(a.country)}
              </span>
            ) : (
              <span className="text-[var(--ink-500)]">{t(`geo.error.${a.error ?? "failed"}`)}</span>
            )}
          </li>
        ))}
      </ul>
      <p className="text-xs text-[var(--ink-500)]">
        {res.ip ? `${res.ip} · ` : ""}
        {t("geo.checkedAt", { ago: ago(res.at) })}
        {res.cached ? ` · ${t("geo.cached")}` : ""}
      </p>
    </div>
  );
}

/** The node's address in the geo bases, asked for on purpose: it goes to every base. */
export function NodeGeoCheck({ node }: { node: Node }) {
  const run = useMutation({
    mutationFn: () => unwrap(api.POST("/api/v1/nodes/{id}/geo", { params: { path: { id: node.id } } })),
  });
  return (
    <section className="mt-6 border-t border-[var(--hairline)] pt-5" aria-labelledby="geo-title">
      <h3 id="geo-title" className="text-[15px] font-semibold text-[var(--ink-900)]">
        {t("geo.title")}
      </h3>
      <p className="mt-1 text-[13px] text-[var(--ink-600)]">{t("geo.lead")}</p>
      <p className="mt-2 text-xs text-[var(--ink-500)]">{t("geo.caveat")}</p>
      <Button className="mt-3" size="sm" loading={run.isPending} onClick={() => run.mutate()}>
        <MapPin size={16} aria-hidden /> {t("geo.button")}
      </Button>
      <div className="mt-3" aria-live="polite" aria-busy={run.isPending}>
        {run.isPending ? (
          <>
            <p className="sr-only">{t("geo.running")}</p>
            <Skeleton style={{ height: 200, borderRadius: 16 }} />
          </>
        ) : run.isError ? (
          <div className="banner err" role="alert">
            {errorText(run.error)}
          </div>
        ) : run.data ? (
          <GeoView res={run.data} />
        ) : null}
      </div>
    </section>
  );
}

/** Any server's address in the geo bases, a node's or one to buy. */
export function GeoTab() {
  const nodes = useNodes();
  const [host, setHost] = useState("");
  const check = useMutation({
    mutationFn: (h: string) => unwrap(api.POST("/api/v1/system/geo", { body: { host: h } })),
  });
  const hosts = [...new Set((nodes.data ?? []).map((n) => n.host).filter(Boolean))];
  const submit = (e: FormEvent) => {
    e.preventDefault();
    if (host.trim()) check.mutate(host.trim());
  };
  return (
    <section className="card glass reveal" aria-labelledby="diag-geo-title">
      <div className="card-head">
        <div>
          <h2 className="card-title" id="diag-geo-title">
            {t("geo.title")}
          </h2>
          <div className="card-sub">{t("geo.lead")}</div>
        </div>
      </div>
      <form className="flex flex-wrap items-start gap-x-4 gap-y-1" onSubmit={submit} noValidate>
        <Field label={t("diag.reach.host")} htmlFor="geo-host">
          <input
            id="geo-host"
            className="input min-w-[240px]"
            value={host}
            onChange={(e) => setHost(e.target.value)}
            placeholder={t("diag.reach.hostPlaceholder")}
            list={hosts.length ? "geo-hosts" : undefined}
            autoComplete="off"
            spellCheck={false}
            inputMode="url"
            required
          />
        </Field>
        {hosts.length ? (
          <datalist id="geo-hosts">
            {hosts.map((h) => (
              <option key={h} value={h} />
            ))}
          </datalist>
        ) : null}
        <div className="mt-[26px] mb-4">
          <Button type="submit" variant="primary" loading={check.isPending} disabled={check.isPending || !host.trim()}>
            <MapPin size={16} aria-hidden /> {t("geo.button")}
          </Button>
        </div>
      </form>
      <p className="mb-4 text-xs text-[var(--ink-500)]">{t("geo.caveat")}</p>
      <div aria-live="polite" aria-busy={check.isPending}>
        {check.isPending ? (
          <>
            <p className="mb-3 text-[13px] text-[var(--ink-600)]">{t("geo.running")}</p>
            <Skeleton style={{ height: 240, borderRadius: 16 }} />
          </>
        ) : check.isError ? (
          <div className="banner err mb-3" role="alert">
            {errorText(check.error)}
          </div>
        ) : check.data ? (
          <GeoView res={check.data} />
        ) : (
          <EmptyState icon={MapPin} title={t("geo.emptyTitle")} text={t("geo.emptyText")} />
        )}
      </div>
    </section>
  );
}
