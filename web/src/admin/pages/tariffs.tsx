import { useMutation, useQueryClient } from "@tanstack/react-query";
import { Archive, Pencil, Plus } from "lucide-react";
import { useEffect, useState, type FormEvent } from "react";
import { api, ApiError, errorText, unwrap, type Schemas, type Tariff } from "../../api/client";
import { qk, useTariffs } from "../../api/hooks";
import { Confirm, Drawer } from "../../components/overlay";
import { useToast } from "../../components/toast";
import { Button, EmptyState, ErrorState, Field, PageHeader, Segmented, Skeleton } from "../../components/ui";
import { bytes, days, GiB } from "../../lib/format";

const RESET_LABEL: Record<Tariff["reset_strategy"], string> = {
  none: "без сброса",
  month_start: "сброс 1-го числа",
  period: "сброс каждые 30 дней",
};

export function tariffSummary(t: Tariff): string {
  const parts = [t.traffic_limit != null ? bytes(t.traffic_limit) : "без лимита", t.duration_days ? days(t.duration_days) : "бессрочно", t.device_limit != null ? `${t.device_limit} устр.` : "∞ устр."];
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
      toast.ok("Тариф убран в архив");
      setArchive(null);
    },
    onError: (e) => toast.error(errorText(e)),
  });

  return (
    <>
      <PageHeader
        title="Тарифы"
        sub="Шаблоны для выдачи доступа в один клик. Изменение тарифа не трогает уже выданные подписки."
        actions={
          <Button variant="primary" onClick={() => setEdit("new")}>
            <Plus size={18} aria-hidden />
            <span className="max-[760px]:hidden">Новый тариф</span>
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
          <EmptyState title="Тарифов пока нет" text="Создайте шаблон — например, «Стандарт: 150 ГБ на 30 дней, 3 устройства».">
            <Button variant="primary" onClick={() => setEdit("new")}>
              <Plus size={18} aria-hidden /> Новый тариф
            </Button>
          </EmptyState>
        </section>
      ) : (
        <div className="grid gap-4 sm:grid-cols-2 xl:grid-cols-3">
          {tariffs.data.map((t, i) => (
            <section key={t.id} className="card glass reveal flex flex-col" style={{ "--i": i } as React.CSSProperties}>
              <div className="flex items-start justify-between gap-3">
                <div>
                  <h2 className="font-display text-xl font-medium tracking-tight">{t.name}</h2>
                  {t.price_label ? <div className="mt-1 text-[13px] font-medium text-[var(--mikan-700)]">{t.price_label}</div> : null}
                </div>
                <div className="flex gap-1">
                  <button type="button" className="icon-btn" aria-label={`Изменить ${t.name}`} onClick={() => setEdit(t)}>
                    <Pencil size={16} />
                  </button>
                  <button type="button" className="icon-btn" aria-label={`В архив ${t.name}`} onClick={() => setArchive(t)}>
                    <Archive size={16} />
                  </button>
                </div>
              </div>
              <dl className="mt-5 grid grid-cols-2 gap-3 text-xs text-[var(--ink-500)]">
                <Item label="Трафик" value={t.traffic_limit != null ? bytes(t.traffic_limit) : "без лимита"} />
                <Item label="Срок" value={t.duration_days ? days(t.duration_days) : "бессрочно"} />
                <Item label="Устройства" value={t.device_limit != null ? String(t.device_limit) : "без лимита"} />
                <Item label="Сброс трафика" value={RESET_LABEL[t.reset_strategy]} />
              </dl>
            </section>
          ))}
        </div>
      )}
      <TariffDrawer tariff={edit} onClose={() => setEdit(null)} />
      <Confirm
        open={!!archive}
        onOpenChange={(v) => !v && setArchive(null)}
        title={`Убрать «${archive?.name ?? ""}» в архив?`}
        text="Новых пользователей по нему создать будет нельзя. У тех, кому он уже выдан, ничего не изменится."
        confirm="В архив"
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
  const [devices, setDevices] = useState("3");
  const [devicesUnlimited, setDevicesUnlimited] = useState(false);
  const [reset, setReset] = useState<Tariff["reset_strategy"]>("period");
  const [price, setPrice] = useState("");
  const [errors, setErrors] = useState<Record<string, string>>({});

  useEffect(() => {
    if (!tariff) return;
    const t = tariff === "new" ? null : tariff;
    setName(t?.name ?? "");
    setUnlimited(t ? t.traffic_limit == null : false);
    setGb(t?.traffic_limit != null ? String(Math.round(t.traffic_limit / GiB)) : "150");
    setDuration(String(t?.duration_days ?? 30));
    setDevicesUnlimited(t ? t.device_limit == null : false);
    setDevices(String(t?.device_limit ?? 3));
    setReset(t?.reset_strategy ?? "period");
    setPrice(t?.price_label ?? "");
    setErrors({});
  }, [tariff]);

  const save = useMutation({
    mutationFn: (body: Schemas["TariffBody"]) =>
      tariff === "new" || !tariff ? unwrap(api.POST("/api/v1/tariffs", { body })) : unwrap(api.PUT("/api/v1/tariffs/{id}", { params: { path: { id: tariff.id } }, body })),
    onSuccess: () => {
      void qc.invalidateQueries({ queryKey: qk.tariffs });
      toast.ok(tariff === "new" ? "Тариф создан" : "Тариф сохранён");
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
    const devN = Number(devices);
    if (!name.trim()) errs.name = "Назовите тариф";
    if (!unlimited && (!Number.isFinite(gbN) || gbN <= 0)) errs.traffic_limit = "Больше нуля или «без лимита»";
    if (!Number.isInteger(durN) || durN < 0 || durN > 3650) errs.duration_days = "От 0 (бессрочно) до 3650 дней";
    if (!devicesUnlimited && (!Number.isInteger(devN) || devN < 1 || devN > 100)) errs.device_limit = "От 1 до 100";
    setErrors(errs);
    if (Object.keys(errs).length) return;
    save.mutate({
      name: name.trim(),
      traffic_limit: unlimited ? undefined : Math.round(gbN * GiB),
      duration_days: durN,
      device_limit: devicesUnlimited ? undefined : devN,
      reset_strategy: unlimited ? "none" : reset,
      price_label: price.trim() || undefined,
    });
  };

  return (
    <Drawer
      open={!!tariff}
      onOpenChange={(v) => !v && onClose()}
      title={tariff === "new" ? "Новый тариф" : "Тариф"}
      meta="Изменения применятся к новым выдачам"
      footer={
        <>
          <Button variant="ghost" onClick={onClose}>
            Отмена
          </Button>
          <Button variant="primary" type="submit" form="tariff-form" loading={save.isPending}>
            Сохранить
          </Button>
        </>
      }
    >
      <form id="tariff-form" onSubmit={submit} className="pt-5" noValidate>
        <Field label="Название" htmlFor="t-name" error={errors.name}>
          <input id="t-name" className="input" value={name} onChange={(e) => setName(e.target.value)} maxLength={60} placeholder="Стандарт" autoFocus />
        </Field>
        <Field label="Трафик за период" htmlFor="t-gb" error={errors.traffic_limit}>
          <div className="flex items-center gap-2">
            <input id="t-gb" className="input max-w-[140px]" inputMode="decimal" value={unlimited ? "" : gb} disabled={unlimited} onChange={(e) => setGb(e.target.value)} aria-invalid={!!errors.traffic_limit} />
            <span className="text-[var(--ink-500)]">ГБ</span>
            <label className="ml-auto flex items-center gap-2 text-[13px]">
              <input type="checkbox" className="check" checked={unlimited} onChange={(e) => setUnlimited(e.target.checked)} /> без лимита
            </label>
          </div>
        </Field>
        <Field label="Срок" htmlFor="t-days" hint="0 — бессрочно" error={errors.duration_days}>
          <div className="flex flex-wrap items-center gap-2">
            <input id="t-days" className="input max-w-[100px]" inputMode="numeric" value={duration} onChange={(e) => setDuration(e.target.value)} />
            {[3, 7, 30, 90, 365].map((n) => (
              <button key={n} type="button" className="chip-btn" onClick={() => setDuration(String(n))}>
                {n === 365 ? "год" : days(n)}
              </button>
            ))}
          </div>
        </Field>
        <Field label="Устройств одновременно" htmlFor="t-dev" error={errors.device_limit}>
          <div className="flex items-center gap-2">
            <input id="t-dev" className="input max-w-[100px]" inputMode="numeric" value={devicesUnlimited ? "" : devices} disabled={devicesUnlimited} onChange={(e) => setDevices(e.target.value)} />
            <label className="ml-auto flex items-center gap-2 text-[13px]">
              <input type="checkbox" className="check" checked={devicesUnlimited} onChange={(e) => setDevicesUnlimited(e.target.checked)} /> без лимита
            </label>
          </div>
        </Field>
        {!unlimited ? (
          <Field label="Когда обнулять трафик" hint="Для тарифов на несколько месяцев: объём выдаётся заново каждый период.">
            <Segmented
              label="Сброс трафика"
              value={reset}
              onChange={setReset}
              options={[
                { value: "period", label: "каждые 30 дней" },
                { value: "month_start", label: "1-го числа" },
                { value: "none", label: "никогда" },
              ]}
            />
          </Field>
        ) : null}
        <Field label="Цена для себя" htmlFor="t-price" hint="Только подпись в панели — оплаты в панели пока нет.">
          <input id="t-price" className="input" value={price} onChange={(e) => setPrice(e.target.value)} maxLength={40} placeholder="490 ₽ / мес" />
        </Field>
      </form>
    </Drawer>
  );
}
