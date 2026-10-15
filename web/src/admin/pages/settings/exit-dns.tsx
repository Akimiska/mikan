// The DNS at the exit: the nodes look a site up themselves, so a site sees the node's
// country whatever DNS the user's device asked. On from the start; here it is tuned.
import { useMutation, useQueryClient } from "@tanstack/react-query";
import type { FormEvent } from "react";
import { api, unwrap, type Schemas } from "../../../api/client";
import { qk, useExitDns } from "../../../api/hooks";
import { FormActions } from "../../../components/layout";
import { QueryBoundary } from "../../../components/query";
import { SwitchRow } from "../../../components/switch";
import { useToast } from "../../../components/toast";
import { Field, Skeleton } from "../../../components/ui";
import { t } from "../../../i18n";
import { useDraft } from "../../../lib/draft";
import { fieldErrors, onFormError } from "../../../lib/fields";

type Info = Schemas["ExitDNSInfo"];
type Resolver = Info["resolver"];

const RESOLVERS: Resolver[] = ["host", "cloudflare", "google", "quad9", "custom"];

// One server a line (commas also split); the panel puts them in their canonical form.
const lines = (s: string) =>
  s
    .split(/[\n,]+/)
    .map((x) => x.trim())
    .filter(Boolean);

const draftOf = (v: Info) => ({ sniff: v.sniff, resolver: v.resolver, servers: v.servers.join("\n") });

export function ExitDnsCard() {
  const q = useExitDns();
  return (
    <QueryBoundary query={q} pending={<Skeleton style={{ height: 240, borderRadius: 20 }} />} wrap={(state) => <section className="card glass">{state}</section>}>
      {(v) => <ExitDnsForm v={v} />}
    </QueryBoundary>
  );
}

function ExitDnsForm({ v }: { v: Info }) {
  const qc = useQueryClient();
  const toast = useToast();
  const { draft, setDraft, dirty, reset } = useDraft(draftOf(v));
  const save = useMutation({
    mutationFn: (body: Schemas["ExitDNSView"]) => unwrap(api.PUT("/api/v1/exit-dns", { body })),
    onSuccess: (nv) => {
      qc.setQueryData(qk.exitDns, nv);
      setDraft(draftOf(nv));
      toast.ok(t("settings.exitDns.saved"));
    },
    // A wrong server is shown under the list; anything else is a toast.
    onError: (e) => onFormError(e, toast),
  });
  const errors = fieldErrors(save.error);
  const serversError = Object.entries(errors).find(([k]) => k === "servers" || k.startsWith("servers["))?.[1];
  const submit = (ev: FormEvent) => {
    ev.preventDefault();
    save.mutate({ sniff: draft.sniff, resolver: draft.resolver, servers: lines(draft.servers) });
  };
  const preset = v.presets[draft.resolver];
  const off = !draft.sniff && draft.resolver === "host";
  return (
    <section className="card glass reveal" style={{ "--i": 3 } as React.CSSProperties} aria-labelledby="exit-dns-title">
      <form onSubmit={submit} noValidate>
        <div className="card-head">
          <div>
            <h2 className="card-title" id="exit-dns-title">
              {t("settings.exitDns.title")}
            </h2>
            <div className="card-sub">{t("settings.exitDns.lead")}</div>
          </div>
        </div>
        <SwitchRow label={t("settings.exitDns.sniff")} sub={t("settings.exitDns.sniffSub")} checked={draft.sniff} onChange={(sniff) => setDraft((d) => ({ ...d, sniff }))} />
        <Field label={t("settings.exitDns.resolver")} htmlFor="exit-dns-resolver" hint={preset ? preset.join(", ") : draft.resolver === "host" ? t("settings.exitDns.resolverHint") : undefined}>
          <select id="exit-dns-resolver" className="input max-w-[320px]" value={draft.resolver} onChange={(e) => setDraft((d) => ({ ...d, resolver: e.target.value as Resolver }))}>
            {RESOLVERS.map((r) => (
              <option key={r} value={r}>
                {t(`settings.exitDns.resolvers.${r}`)}
              </option>
            ))}
          </select>
        </Field>
        {draft.resolver === "custom" ? (
          <Field label={t("settings.exitDns.servers")} htmlFor="exit-dns-servers" hint={t("settings.exitDns.serversHint")} error={serversError}>
            <textarea
              id="exit-dns-servers"
              className="input mono min-h-[96px]"
              value={draft.servers}
              onChange={(e) => setDraft((d) => ({ ...d, servers: e.target.value }))}
              spellCheck={false}
              placeholder={"https://1.1.1.1/dns-query\ntls://dns.google"}
            />
          </Field>
        ) : null}
        {off ? (
          <p className="mb-3 text-[13px] text-[var(--ink-600)]" role="status">
            {t("settings.exitDns.off")}
          </p>
        ) : null}
        <FormActions dirty={dirty} saving={save.isPending} onReset={reset} />
      </form>
    </section>
  );
}
