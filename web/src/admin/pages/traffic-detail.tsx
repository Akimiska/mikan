import { useMutation, useQuery, useQueryClient } from "@tanstack/react-query";
import type { FormEvent } from "react";
import { api, errorText, unwrap } from "../../api/client";
import { CardHead, FormActions } from "../../components/layout";
import { SwitchRow } from "../../components/switch";
import { useToast } from "../../components/toast";
import { Field, Skeleton } from "../../components/ui";
import { t } from "../../i18n";
import { useDraft } from "../../lib/draft";

const key = ["traffic-detail"] as const;

/** Where the clients go: the nodes sum each subscription's traffic by site, kept a few days. */
export function TrafficDetailCard() {
  const q = useQuery({ queryKey: key, queryFn: ({ signal }) => unwrap(api.GET("/api/v1/traffic-detail", { signal })) });
  if (!q.data) {
    return q.isError ? (
      <section className="card glass">
        <div className="banner err" role="alert">{errorText(q.error)}</div>
      </section>
    ) : (
      <Skeleton style={{ height: 160, borderRadius: 20 }} />
    );
  }
  return <DetailForm v={q.data} />;
}

function DetailForm({ v }: { v: { enabled: boolean; days: number } }) {
  const qc = useQueryClient();
  const toast = useToast();
  const { draft, setDraft, dirty, reset } = useDraft({ enabled: v.enabled, days: String(v.days) });
  const days = Number(draft.days);
  const bad = !Number.isInteger(days) || days < 1 || days > 90;
  const save = useMutation({
    mutationFn: () => unwrap(api.PUT("/api/v1/traffic-detail", { body: { enabled: draft.enabled, days } })),
    onSuccess: (nv) => {
      qc.setQueryData(key, nv);
      setDraft({ enabled: nv.enabled, days: String(nv.days) });
      toast.ok(t("filters.saved"));
    },
    onError: (e) => toast.error(errorText(e)),
  });
  const submit = (e: FormEvent) => {
    e.preventDefault();
    if (!bad) save.mutate();
  };
  return (
    <section className="card glass reveal">
      <form onSubmit={submit} noValidate>
        <CardHead title={t("settings.detail.title")} sub={t("settings.detail.sub")} />
        <SwitchRow label={t("settings.detail.on")} sub={t("settings.detail.onSub")} checked={draft.enabled} onChange={(on) => setDraft((d) => ({ ...d, enabled: on }))} />
        <Field label={t("settings.detail.keep")} htmlFor="detail-days" hint={t("settings.detail.keepHint")} error={bad ? t("settings.detail.keepHint") : undefined}>
          <input id="detail-days" className="input max-w-[120px]" inputMode="numeric" value={draft.days} onChange={(e) => setDraft((d) => ({ ...d, days: e.target.value }))} aria-invalid={bad} />
        </Field>
        <FormActions dirty={dirty} saving={save.isPending} onReset={reset} />
      </form>
    </section>
  );
}
