// Ad campaigns: a link to the bot with a code of its own, and who came, bought and renewed
// by it over a period.
import { useMutation, useQuery, useQueryClient } from "@tanstack/react-query";
import { Archive, ArchiveRestore, Copy, Megaphone, Plus, Trash2 } from "lucide-react";
import { useState, type FormEvent } from "react";
import { api, errorText, unwrap, type Schemas } from "../../api/client";
import { Confirm, Drawer, DrawerFooter } from "../../components/overlay";
import { QueryBoundary } from "../../components/query";
import { useToast } from "../../components/toast";
import { Button, EmptyState, Field, Pill, Segmented, Skeleton } from "../../components/ui";
import { t } from "../../i18n";
import { useCopy } from "../../lib/copy";
import { fieldErrors, onFormError } from "../../lib/fields";
import { money, num } from "../../lib/format";

type Campaign = Schemas["CampaignView"];
const PERIODS = ["7", "30", "90", "0"] as const;
type Period = (typeof PERIODS)[number];

const campaignsKey = (days: Period) => ["campaigns", days] as const;

export function CampaignsSection() {
  const qc = useQueryClient();
  const toast = useToast();
  const copy = useCopy();
  const [period, setPeriod] = useState<Period>("30");
  const [creating, setCreating] = useState(false);
  const [removing, setRemoving] = useState<Campaign | null>(null);
  const list = useQuery({
    queryKey: campaignsKey(period),
    queryFn: () => unwrap(api.GET("/api/v1/campaigns", { params: { query: { days: Number(period) } } })),
  });
  const refresh = () => void qc.invalidateQueries({ queryKey: ["campaigns"] });
  const archive = useMutation({
    mutationFn: (c: Campaign) => unwrap(api.PATCH("/api/v1/campaigns/{id}", { params: { path: { id: c.id } }, body: { archived: !c.archived } })),
    onSuccess: refresh,
    onError: (e) => toast.error(errorText(e)),
  });
  const remove = useMutation({
    mutationFn: (id: number) => unwrap(api.DELETE("/api/v1/campaigns/{id}", { params: { path: { id } } })),
    onSuccess: () => {
      setRemoving(null);
      refresh();
    },
    onError: (e) => toast.error(errorText(e)),
  });
  return (
    <section className="card glass reveal">
      <div className="card-head">
        <div>
          <h2 className="card-title">{t("campaigns.title")}</h2>
          <div className="card-sub">{t("campaigns.sub")}</div>
        </div>
        <Button variant="primary" size="sm" onClick={() => setCreating(true)}>
          <Plus size={16} aria-hidden /> {t("campaigns.new")}
        </Button>
      </div>
      <div className="mb-4">
        <Segmented
          label={t("campaigns.period")}
          value={period}
          onChange={setPeriod}
          options={PERIODS.map((p) => ({ value: p, label: p === "0" ? t("campaigns.periodAll") : t("campaigns.periodDays", { n: Number(p) }) }))}
        />
      </div>
      <QueryBoundary query={list} pending={<Skeleton style={{ height: 160, borderRadius: 16 }} />}>
        {(rows) =>
          rows.length === 0 ? (
            <EmptyState icon={Megaphone} title={t("campaigns.emptyTitle")} text={t("campaigns.emptyText")}>
              <Button variant="primary" onClick={() => setCreating(true)}>
                <Plus size={16} aria-hidden /> {t("campaigns.new")}
              </Button>
            </EmptyState>
          ) : (
            <div className="row-list">
              {rows.map((c) => (
                <div key={c.id} className="py-3">
                  <div className="flex flex-wrap items-center gap-2">
                    <span className="font-semibold">{c.name}</span>
                    <span className="mono text-xs text-[var(--ink-500)]">{c.code}</span>
                    {c.archived ? <Pill tone="off">{t("campaigns.archived")}</Pill> : null}
                  </div>
                  <div className="mt-2 grid grid-cols-2 gap-2 sm:grid-cols-4" role="group" aria-label={t("campaigns.stats")}>
                    <Stat label={t("campaigns.came")} value={num(c.came)} />
                    <Stat label={t("campaigns.bought")} value={num(c.bought)} sub={c.came ? `${Math.round((c.bought / c.came) * 100)}%` : undefined} />
                    <Stat label={t("campaigns.renewed")} value={num(c.renewed)} />
                    <Stat
                      label={t("campaigns.revenue")}
                      value={[c.revenue_rub ? money(c.revenue_rub, "RUB") : "", c.revenue_stars ? money(c.revenue_stars, "XTR") : ""].filter(Boolean).join(" · ") || "—"}
                    />
                  </div>
                  <div className="mt-3 flex flex-wrap gap-2">
                    {c.link ? (
                      <Button size="sm" onClick={() => void copy(c.link, t("campaigns.copied"))}>
                        <Copy size={16} aria-hidden /> {t("campaigns.copy")}
                      </Button>
                    ) : (
                      <span className="text-xs text-[var(--honey-600)]">{t("campaigns.noBot")}</span>
                    )}
                    <Button size="sm" variant="ghost" loading={archive.isPending && archive.variables?.id === c.id} onClick={() => archive.mutate(c)}>
                      {c.archived ? <ArchiveRestore size={16} aria-hidden /> : <Archive size={16} aria-hidden />} {c.archived ? t("campaigns.restore") : t("campaigns.archive")}
                    </Button>
                    <Button size="sm" variant="danger" onClick={() => setRemoving(c)}>
                      <Trash2 size={16} aria-hidden /> {t("common.delete")}
                    </Button>
                  </div>
                  {c.link ? <div className="mono mt-2 truncate text-xs text-[var(--ink-500)]">{c.link}</div> : null}
                </div>
              ))}
            </div>
          )
        }
      </QueryBoundary>
      <CreateCampaign open={creating} onClose={() => setCreating(false)} onCreated={refresh} />
      <Confirm
        open={!!removing}
        onOpenChange={(v) => !v && setRemoving(null)}
        title={t("campaigns.deleteTitle", { name: removing?.name ?? "" })}
        text={t("campaigns.deleteText")}
        confirm={t("common.delete")}
        danger
        loading={remove.isPending}
        onConfirm={() => removing && remove.mutate(removing.id)}
      />
    </section>
  );
}

function Stat({ label, value, sub }: { label: string; value: string; sub?: string }) {
  return (
    <div className="panel-soft p-2">
      <div className="text-xs text-[var(--ink-500)]">{label}</div>
      <div className="text-[15px] font-semibold">
        {value}
        {sub ? <span className="ml-1 text-xs font-normal text-[var(--ink-500)]">{sub}</span> : null}
      </div>
    </div>
  );
}

function CreateCampaign({ open, onClose, onCreated }: { open: boolean; onClose: () => void; onCreated: () => void }) {
  const toast = useToast();
  const [name, setName] = useState("");
  const [code, setCode] = useState("");
  const [errors, setErrors] = useState<Record<string, string>>({});
  const create = useMutation({
    mutationFn: () => unwrap(api.POST("/api/v1/campaigns", { body: { name: name.trim(), code: code.trim() || undefined } })),
    onSuccess: () => {
      toast.ok(t("campaigns.created"));
      setName("");
      setCode("");
      onCreated();
      onClose();
    },
    onError: (e) => {
      if (onFormError(e, toast, setErrors)) setErrors(fieldErrors(e));
    },
  });
  const submit = (e: FormEvent) => {
    e.preventDefault();
    if (!name.trim()) {
      setErrors({ name: t("campaigns.nameRequired") });
      return;
    }
    if (code.trim() && !/^[a-z0-9_-]{1,32}$/.test(code.trim())) {
      setErrors({ code: t("errors.api.campaign_code") });
      return;
    }
    create.mutate();
  };
  return (
    <Drawer
      open={open}
      onOpenChange={(v) => !v && onClose()}
      title={t("campaigns.new")}
      footer={
        <DrawerFooter form="new-campaign" onCancel={onClose} loading={create.isPending}>
          {t("campaigns.create")}
        </DrawerFooter>
      }
    >
      <form id="new-campaign" onSubmit={submit} className="pt-5" noValidate>
        <Field label={t("campaigns.name")} htmlFor="cm-name" error={errors.name}>
          <input id="cm-name" className="input" value={name} onChange={(e) => setName(e.target.value)} maxLength={80} placeholder={t("campaigns.namePlaceholder")} aria-invalid={!!errors.name} />
        </Field>
        <Field label={t("campaigns.code")} htmlFor="cm-code" hint={t("campaigns.codeHint")} error={errors.code}>
          <input id="cm-code" className="input mono max-w-[240px]" value={code} onChange={(e) => setCode(e.target.value.toLowerCase())} maxLength={32} placeholder="vk-oct" spellCheck={false} aria-invalid={!!errors.code} />
        </Field>
        <p className="text-xs text-[var(--ink-500)]">{t("campaigns.note")}</p>
      </form>
    </Drawer>
  );
}
