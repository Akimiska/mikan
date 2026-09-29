import { useMutation, useQueryClient } from "@tanstack/react-query";
import { Archive, Pencil, Plus } from "lucide-react";
import { useEffect, useState, type FormEvent } from "react";
import { api, ApiError, errorText, unwrap, type Schemas, type Tariff } from "../../api/client";
import { qk, useTariffs } from "../../api/hooks";
import { Confirm, Drawer } from "../../components/overlay";
import { useToast } from "../../components/toast";
import { Button, EmptyState, ErrorState, Field, PageHeader, Segmented, Skeleton } from "../../components/ui";
import { t } from "../../i18n";
import { bytes, days, GiB, months, termMonths } from "../../lib/format";

/** How long a term on the tariff runs: days, or months up to the billing day. */
export function tariffTerm(tr: Tariff): string {
  if (!tr.duration_days) return t("time.forever");
  if (tr.billing_day != null) return t("tariffs.termToDay", { months: months(termMonths(tr.duration_days)), d: tr.billing_day });
  return days(tr.duration_days);
}

function resetLabel(tr: Tariff): string {
  if (tr.reset_strategy === "month_start" && tr.billing_day != null) return t("tariffs.resetOnDay", { d: tr.billing_day });
  return t(`tariffs.resetLabel.${tr.reset_strategy}`);
}

export function tariffSummary(tr: Tariff): string {
  const parts = [
    tr.traffic_limit != null ? bytes(tr.traffic_limit) : t("users.unlimited"),
    tariffTerm(tr),
    tr.device_limit != null ? t("userDrawer.devicesShort", { n: tr.device_limit }) : t("tariffs.devicesUnlimitedShort"),
  ];
  return parts.join(" · ");
}

export function TariffsPage() {
  const tariffs = useTariffs();
  const [edit, setEdit] = useState<Tariff | "new" | null>(null);
  const [archive, setArchive] = useState<Tariff | null>(null);
  const qc = useQueryClient();
  const toast = useToast();
  const remove = useMutation({
    mutationFn: (id: number) => unwrap(api.DELETE("/api/v1/tariffs/{id}", { params: { path: { id } } })),
    onSuccess: () => {
      void qc.invalidateQueries({ queryKey: qk.tariffs });
      toast.ok(t("tariffs.archived"));
      setArchive(null);
    },
    onError: (e) => toast.error(errorText(e)),
  });

  return (
    <>
      <PageHeader
        title={t("nav.tariffs")}
        sub={t("tariffs.subtitle")}
        actions={
          <Button variant="primary" onClick={() => setEdit("new")}>
            <Plus size={18} aria-hidden />
            <span className="max-[760px]:hidden">{t("tariffs.new")}</span>
          </Button>
        }
      />
      {tariffs.isPending ? (
        <div className="grid gap-4 sm:grid-cols-2 xl:grid-cols-3">
          {[0, 1, 2].map((i) => (
            <Skeleton key={i} style={{ height: 180, borderRadius: 20 }} />
          ))}
        </div>
      ) : tariffs.isError ? (
        <section className="card glass">
          <ErrorState text={errorText(tariffs.error)} onRetry={() => void tariffs.refetch()} />
        </section>
      ) : tariffs.data.length === 0 ? (
        <section className="card glass">
          <EmptyState title={t("tariffs.emptyTitle")} text={t("tariffs.emptyText")}>
            <Button variant="primary" onClick={() => setEdit("new")}>
              <Plus size={18} aria-hidden /> {t("tariffs.new")}
            </Button>
          </EmptyState>
        </section>
      ) : (
        <div className="grid gap-4 sm:grid-cols-2 xl:grid-cols-3">
          {tariffs.data.map((tr, i) => (
            <section key={tr.id} className="card glass reveal flex flex-col" style={{ "--i": i } as React.CSSProperties}>
              <div className="flex items-start justify-between gap-3">
                <div>
                  <h2 className="font-display text-xl font-medium tracking-tight">{tr.name}</h2>
                  {tr.price_label ? <div className="mt-1 text-[13px] font-medium text-[var(--mikan-700)]">{tr.price_label}</div> : null}
                </div>
                <div className="flex gap-1">
                  <button type="button" className="icon-btn" aria-label={t("tariffs.editLabel", { name: tr.name })} onClick={() => setEdit(tr)}>
                    <Pencil size={16} />
                  </button>
                  <button type="button" className="icon-btn" aria-label={t("tariffs.archiveLabel", { name: tr.name })} onClick={() => setArchive(tr)}>
                    <Archive size={16} />
                  </button>
                </div>
              </div>
              <dl className="mt-5 grid grid-cols-2 gap-3 text-xs text-[var(--ink-500)]">
                <Item label={t("users.colTraffic")} value={tr.traffic_limit != null ? bytes(tr.traffic_limit) : t("users.unlimited")} />
                <Item label={t("users.colExpiry")} value={tariffTerm(tr)} />
                <Item label={t("users.colDevices")} value={tr.device_limit != null ? String(tr.device_limit) : t("users.unlimited")} />
                <Item label={t("tariffs.reset")} value={resetLabel(tr)} />
              </dl>
            </section>
          ))}
        </div>
      )}
      <TariffDrawer tariff={edit} onClose={() => setEdit(null)} />
      <Confirm
        open={!!archive}
        onOpenChange={(v) => !v && setArchive(null)}
        title={t("tariffs.archiveTitle", { name: archive?.name ?? "" })}
        text={t("tariffs.archiveText")}
        confirm={t("tariffs.archiveConfirm")}
        loading={remove.isPending}
        onConfirm={() => archive && remove.mutate(archive.id)}
      />
    </>
  );
}

function Item({ label, value }: { label: string; value: string }) {
  return (
    <div>
      <dt>{label}</dt>
      <dd className="mt-0.5 text-sm font-medium text-[var(--ink-900)]">{value}</dd>
    </div>
  );
}

function TariffDrawer({ tariff, onClose }: { tariff: Tariff | "new" | null; onClose: () => void }) {
  const qc = useQueryClient();
  const toast = useToast();
  const [name, setName] = useState("");
  const [gb, setGb] = useState("150");
  const [unlimited, setUnlimited] = useState(false);
  const [duration, setDuration] = useState("30");
  const [term, setTerm] = useState<"days" | "day">("days");
  const [monthsN, setMonthsN] = useState("1");
  const [billingDay, setBillingDay] = useState("1");
  const [devices, setDevices] = useState("3");
  const [devicesUnlimited, setDevicesUnlimited] = useState(false);
  const [reset, setReset] = useState<Tariff["reset_strategy"]>("period");
  const [price, setPrice] = useState("");
  const [errors, setErrors] = useState<Record<string, string>>({});

  useEffect(() => {
    if (!tariff) return;
    const tr = tariff === "new" ? null : tariff;
    setName(tr?.name ?? "");
    setUnlimited(tr ? tr.traffic_limit == null : false);
    setGb(tr?.traffic_limit != null ? String(Math.round(tr.traffic_limit / GiB)) : "150");
    setDuration(String(tr?.duration_days ?? 30));
    setTerm(tr?.billing_day != null ? "day" : "days");
    setMonthsN(tr?.billing_day != null && !tr.duration_days ? "0" : String(termMonths(tr?.duration_days ?? 30)));
    setBillingDay(String(tr?.billing_day ?? new Date().getDate()));
    setDevicesUnlimited(tr ? tr.device_limit == null : false);
    setDevices(String(tr?.device_limit ?? 3));
    setReset(tr?.reset_strategy ?? "period");
    setPrice(tr?.price_label ?? "");
    setErrors({});
  }, [tariff]);

  const save = useMutation({
    mutationFn: (body: Schemas["TariffBody"]) =>
      tariff === "new" || !tariff ? unwrap(api.POST("/api/v1/tariffs", { body })) : unwrap(api.PUT("/api/v1/tariffs/{id}", { params: { path: { id: tariff.id } }, body })),
    onSuccess: () => {
      void qc.invalidateQueries({ queryKey: qk.tariffs });
      toast.ok(tariff === "new" ? t("tariffs.created") : t("tariffs.saved"));
      onClose();
    },
    onError: (e) => {
      if (e instanceof ApiError && Object.keys(e.fields).length) setErrors(e.fields);
      else toast.error(errorText(e));
    },
  });

  const submit = (e: FormEvent) => {
    e.preventDefault();
    const errs: Record<string, string> = {};
    const gbN = Number(gb);
    const durN = Number(duration);
    const monN = Number(monthsN);
    const dayN = Number(billingDay);
    const devN = Number(devices);
    const toDay = term === "day";
    if (!name.trim()) errs.name = t("tariffs.errName");
    if (!unlimited && (!Number.isFinite(gbN) || gbN <= 0)) errs.traffic_limit = t("tariffs.errTraffic");
    if (!toDay && (!Number.isInteger(durN) || durN < 0 || durN > 3650)) errs.duration_days = t("tariffs.errDuration");
    if (toDay && (!Number.isInteger(monN) || monN < 0 || monN > 120)) errs.duration_days = t("tariffs.errMonths");
    if (toDay && (!Number.isInteger(dayN) || dayN < 1 || dayN > 31)) errs.billing_day = t("tariffs.errBillingDay");
    if (!devicesUnlimited && (!Number.isInteger(devN) || devN < 1 || devN > 100)) errs.device_limit = t("tariffs.errDevices");
    setErrors(errs);
    if (Object.keys(errs).length) return;
    save.mutate({
      name: name.trim(),
      traffic_limit: unlimited ? undefined : Math.round(gbN * GiB),
      // The server turns 30 days into one month up to the billing day.
      duration_days: toDay ? monN * 30 : durN,
      billing_day: toDay ? dayN : undefined,
      device_limit: devicesUnlimited ? undefined : devN,
      reset_strategy: unlimited ? "none" : reset,
      price_label: price.trim() || undefined,
      // PUT replaces the tariff: keep its place in the list.
      sort: tariff && tariff !== "new" ? tariff.sort : undefined,
    });
  };

  return (
    <Drawer
      open={!!tariff}
      onOpenChange={(v) => !v && onClose()}
      title={tariff === "new" ? t("tariffs.new") : t("users.colTariff")}
      meta={t("tariffs.drawerMeta")}
      footer={
        <>
          <Button variant="ghost" onClick={onClose}>
            {t("common.cancel")}
          </Button>
          <Button variant="primary" type="submit" form="tariff-form" loading={save.isPending}>
            {t("common.save")}
          </Button>
        </>
      }
    >
      <form id="tariff-form" onSubmit={submit} className="pt-5" noValidate>
        <Field label={t("tariffs.name")} htmlFor="t-name" error={errors.name}>
          <input id="t-name" className="input" value={name} onChange={(e) => setName(e.target.value)} maxLength={60} placeholder={t("tariffs.namePlaceholder")} autoFocus />
        </Field>
        <Field label={t("tariffs.trafficPerPeriod")} htmlFor="t-gb" error={errors.traffic_limit}>
          <div className="flex items-center gap-2">
            <input id="t-gb" className="input max-w-[140px]" inputMode="decimal" value={unlimited ? "" : gb} disabled={unlimited} onChange={(e) => setGb(e.target.value)} aria-invalid={!!errors.traffic_limit} />
            <span className="text-[var(--ink-500)]">{t("units.gb")}</span>
            <label className="ml-auto flex items-center gap-2 text-[13px]">
              <input type="checkbox" className="check" checked={unlimited} onChange={(e) => setUnlimited(e.target.checked)} /> {t("users.unlimited")}
            </label>
          </div>
        </Field>
        <Field label={t("users.colExpiry")} htmlFor={term === "day" ? "t-months" : "t-days"} hint={term === "day" ? t("tariffs.monthsHint") : t("tariffs.durationHint")} error={errors.duration_days}>
          <Segmented
            label={t("tariffs.termKind")}
            value={term}
            onChange={setTerm}
            options={[
              { value: "days", label: t("tariffs.termDays") },
              { value: "day", label: t("tariffs.termToDate") },
            ]}
          />
          {term === "day" ? (
            <div className="mt-2 flex flex-wrap items-center gap-2">
              <input id="t-months" className="input max-w-[80px]" inputMode="numeric" value={monthsN} onChange={(e) => setMonthsN(e.target.value)} aria-invalid={!!errors.duration_days} />
              {[1, 3, 6, 12].map((n) => (
                <button key={n} type="button" className="chip-btn" onClick={() => setMonthsN(String(n))}>
                  {n === 12 ? t("userDrawer.year") : months(n)}
                </button>
              ))}
            </div>
          ) : (
            <div className="mt-2 flex flex-wrap items-center gap-2">
              <input id="t-days" className="input max-w-[100px]" inputMode="numeric" value={duration} onChange={(e) => setDuration(e.target.value)} aria-invalid={!!errors.duration_days} />
              {[3, 7, 30, 90, 365].map((n) => (
                <button key={n} type="button" className="chip-btn" onClick={() => setDuration(String(n))}>
                  {n === 365 ? t("userDrawer.year") : days(n)}
                </button>
              ))}
            </div>
          )}
        </Field>
        {term === "day" ? (
          <Field label={t("tariffs.billingDay")} htmlFor="t-bday" hint={t("tariffs.billingDayHint")} error={errors.billing_day}>
            <div className="flex items-center gap-2">
              <input id="t-bday" className="input max-w-[100px]" inputMode="numeric" value={billingDay} onChange={(e) => setBillingDay(e.target.value)} aria-invalid={!!errors.billing_day} />
              <span className="text-[var(--ink-500)]">{t("tariffs.dayOfEveryMonth")}</span>
            </div>
          </Field>
        ) : null}
        <Field label={t("tariffs.devicesAtOnce")} htmlFor="t-dev" error={errors.device_limit}>
          <div className="flex items-center gap-2">
            <input id="t-dev" className="input max-w-[100px]" inputMode="numeric" value={devicesUnlimited ? "" : devices} disabled={devicesUnlimited} onChange={(e) => setDevices(e.target.value)} />
            <label className="ml-auto flex items-center gap-2 text-[13px]">
              <input type="checkbox" className="check" checked={devicesUnlimited} onChange={(e) => setDevicesUnlimited(e.target.checked)} /> {t("users.unlimited")}
            </label>
          </div>
        </Field>
        {!unlimited ? (
          <Field label={t("tariffs.whenReset")} hint={t("tariffs.whenResetHint")}>
            <Segmented
              label={t("tariffs.reset")}
              value={reset}
              onChange={setReset}
              options={[
                { value: "period", label: t("tariffs.resetEvery30") },
                { value: "month_start", label: term === "day" && Number(billingDay) >= 1 && Number(billingDay) <= 31 ? t("tariffs.resetOnDayShort", { d: Number(billingDay) }) : t("tariffs.resetMonthStart") },
                { value: "none", label: t("tariffs.resetNever") },
              ]}
            />
          </Field>
        ) : null}
        <Field label={t("tariffs.price")} htmlFor="t-price" hint={t("tariffs.priceHint")}>
          <input id="t-price" className="input" value={price} onChange={(e) => setPrice(e.target.value)} maxLength={40} placeholder={t("tariffs.pricePlaceholder")} />
        </Field>
      </form>
    </Drawer>
  );
}
