import { keepPreviousData, useInfiniteQuery, useMutation, useQuery, useQueryClient } from "@tanstack/react-query";
import { Link } from "@tanstack/react-router";
import { Copy, Undo2 } from "lucide-react";
import { useEffect, useState, type FormEvent, type ReactNode } from "react";
import { api, ApiError, errorText, unwrap, type Schemas } from "../../api/client";
import { qk } from "../../api/hooks";
import { Confirm } from "../../components/overlay";
import { useToast } from "../../components/toast";
import { Button, EmptyState, ErrorState, Field, PageHeader, Pill, Skeleton, Switch } from "../../components/ui";
import { t } from "../../i18n";
import { dateShort, money, num, time } from "../../lib/format";

type Settings = Schemas["PaymentSettingsView"];
type Payment = Schemas["PaymentView"];
type Status = Payment["status"];
type Provider = Payment["provider"];

const STATUS_TONE: Record<Status, "ok" | "warn" | "bad" | "off"> = {
  applied: "ok",
  paid: "warn",
  pending: "off",
  expired: "off",
  failed: "bad",
  refunded: "bad",
};
const STATUSES: Status[] = ["applied", "paid", "pending", "failed", "expired", "refunded"];
const PROVIDERS: Provider[] = ["stars", "yookassa", "cryptobot"];

export function PaymentsPage() {
  const settings = useQuery({ queryKey: qk.paymentSettings, queryFn: () => unwrap(api.GET("/api/v1/payments/settings")) });
  return (
    <>
      <PageHeader title={t("payments.title")} sub={t("payments.subtitle")} />
      <div className="grid items-start gap-4 xl:grid-cols-[minmax(0,1fr)_420px]">
        <History />
        {settings.isPending ? (
          <Skeleton style={{ height: 420, borderRadius: 20 }} />
        ) : settings.isError ? (
          <section className="card glass">
            <ErrorState text={errorText(settings.error)} onRetry={() => void settings.refetch()} />
          </section>
        ) : (
          <SettingsCard s={settings.data} />
        )}
      </div>
    </>
  );
}

function History() {
  const qc = useQueryClient();
  const toast = useToast();
  const [status, setStatus] = useState<Status | "">("");
  const [provider, setProvider] = useState<Provider | "">("");
  const [refund, setRefund] = useState<Payment | null>(null);
  const list = useInfiniteQuery({
    queryKey: [...qk.payments, status, provider],
    initialPageParam: 0,
    queryFn: ({ pageParam }) =>
      unwrap(api.GET("/api/v1/payments", { params: { query: { status: status || undefined, provider: provider || undefined, before: pageParam || undefined, limit: 50 } } })),
    getNextPageParam: (last) => (last.items.length === 50 ? last.items[last.items.length - 1]!.id : undefined),
    placeholderData: keepPreviousData,
  });
  const doRefund = useMutation({
    mutationFn: (id: number) => unwrap(api.POST("/api/v1/payments/{id}/refund", { params: { path: { id } } })),
    onSuccess: () => {
      void qc.invalidateQueries({ queryKey: qk.payments });
      setRefund(null);
      toast.ok(t("payments.refunded"));
    },
    onError: (e) => toast.error(errorText(e)),
  });
  const items = list.data?.pages.flatMap((p) => p.items) ?? [];
  const totals = list.data?.pages[0]?.totals ?? [];
  return (
    <section className="card glass reveal min-w-0">
      <div className="card-head">
        <div>
          <h2 className="card-title">{t("payments.history")}</h2>
          <div className="card-sub">
            {totals.length ? totals.map((x) => t("payments.total", { n: num(x.count), sum: money(x.total, x.currency) })).join(" · ") : t("payments.noTotals")}
          </div>
        </div>
      </div>
      <div className="mb-4 flex flex-wrap gap-2">
        <select className="input max-w-[200px]" value={status} onChange={(e) => setStatus(e.target.value as Status | "")} aria-label={t("payments.status")}>
          <option value="">{t("payments.allStatuses")}</option>
          {STATUSES.map((s) => (
            <option key={s} value={s}>
              {t(`payments.statuses.${s}`)}
            </option>
          ))}
        </select>
        <select className="input max-w-[200px]" value={provider} onChange={(e) => setProvider(e.target.value as Provider | "")} aria-label={t("payments.provider")}>
          <option value="">{t("payments.allProviders")}</option>
          {PROVIDERS.map((p) => (
            <option key={p} value={p}>
              {t(`payments.providers.${p}`)}
            </option>
          ))}
        </select>
      </div>
      {list.isPending ? (
        <div className="flex flex-col gap-2">
          {[0, 1, 2, 3].map((i) => (
            <Skeleton key={i} style={{ height: 56 }} />
          ))}
        </div>
      ) : list.isError ? (
        <ErrorState text={errorText(list.error)} onRetry={() => void list.refetch()} />
      ) : items.length === 0 ? (
        <EmptyState title={t("payments.empty")} text={status || provider ? t("payments.emptyFiltered") : t("payments.emptyText")} search={!!(status || provider)} />
      ) : (
        <>
          <ul className="row-list" aria-busy={list.isFetching}>
            {items.map((p) => (
              <PaymentRow key={p.id} p={p} onRefund={() => setRefund(p)} />
            ))}
          </ul>
          {list.hasNextPage ? (
            <Button className="mt-3" size="sm" loading={list.isFetchingNextPage} onClick={() => void list.fetchNextPage()}>
              {t("payments.more")}
            </Button>
          ) : null}
        </>
      )}
      <Confirm
        open={!!refund}
        onOpenChange={(v) => !v && setRefund(null)}
        title={t("payments.refundTitle", { sum: refund ? money(refund.amount, refund.currency) : "" })}
        text={t("payments.refundText")}
        confirm={t("payments.refund")}
        danger
        loading={doRefund.isPending}
        onConfirm={() => refund && doRefund.mutate(refund.id)}
      />
    </section>
  );
}

function PaymentRow({ p, onRefund }: { p: Payment; onRefund: () => void }) {
  const buyer = p.tg_username ? `@${p.tg_username}` : `tg ${p.tg_id}`;
  return (
    <li className="grid grid-cols-[minmax(0,1fr)_auto] items-center gap-3 py-3">
      <div className="min-w-0">
        <div className="flex flex-wrap items-center gap-2">
          <b className="num text-[13px]">{money(p.amount, p.currency)}</b>
          <span className="truncate text-[13px]">{p.tariff_name}</span>
          <Pill tone={STATUS_TONE[p.status]}>{t(`payments.statuses.${p.status}`)}</Pill>
        </div>
        <div className="mt-1 text-xs text-[var(--ink-500)]">
          {dateShort(p.created_at)} {time(p.created_at)} · {t(`payments.providers.${p.provider}`)} · {p.kind === "new" ? t("payments.kindNew") : t("payments.kindRenew")} · {buyer}
          {p.user_id != null ? (
            <>
              {" → "}
              <Link to="/users" search={{ state: "all", q: "", user: p.user_id }} className="link-btn">
                {p.user_name || `#${p.user_id}`}
              </Link>
            </>
          ) : null}
        </div>
        {p.error ? <div className="mt-1 text-xs text-[var(--berry-600)]">{t("payments.notApplied", { error: p.error })}</div> : null}
      </div>
      {p.provider === "stars" && p.status === "applied" ? (
        <Button size="sm" variant="ghost" onClick={onRefund}>
          <Undo2 size={16} aria-hidden /> {t("payments.refund")}
        </Button>
      ) : null}
    </li>
  );
}

function SettingsCard({ s }: { s: Settings }) {
  const qc = useQueryClient();
  const toast = useToast();
  const init = () => ({ stars: s.stars, yookassa: s.yookassa, shop: s.yookassa_shop_id, cryptobot: s.cryptobot, testnet: s.cryptobot_testnet, allowNew: s.allow_new, resetTraffic: s.renew_resets_traffic });
  const [form, setForm] = useState(init);
  const [ykSecret, setYkSecret] = useState("");
  const [cbToken, setCbToken] = useState("");
  useEffect(() => setForm(init()), [s]);
  const save = useMutation({
    mutationFn: (body: Schemas["PatchPaymentSettingsInputBody"]) => unwrap(api.PATCH("/api/v1/payments/settings", { body })),
    onSuccess: (v) => {
      qc.setQueryData(qk.paymentSettings, v);
      setYkSecret("");
      setCbToken("");
      toast.ok(t("payments.saved"));
    },
  });
  const errors = save.error instanceof ApiError ? save.error.fields : {};
  const submit = (e: FormEvent) => {
    e.preventDefault();
    save.mutate({
      stars: form.stars,
      yookassa: form.yookassa,
      yookassa_shop_id: form.shop.trim(),
      cryptobot: form.cryptobot,
      cryptobot_testnet: form.testnet,
      allow_new: form.allowNew,
      renew_resets_traffic: form.resetTraffic,
      ...(ykSecret.trim() ? { yookassa_secret: ykSecret.trim() } : {}),
      ...(cbToken.trim() ? { cryptobot_token: cbToken.trim() } : {}),
    });
  };
  const set = (k: keyof ReturnType<typeof init>) => (v: boolean) => setForm((f) => ({ ...f, [k]: v }));
  return (
    <section className="card glass reveal" style={{ "--i": 1 } as React.CSSProperties}>
      <form onSubmit={submit} noValidate>
        <div className="card-head">
          <div>
            <h2 className="card-title">{t("payments.settings")}</h2>
            <div className="card-sub">{t("payments.settingsSub")}</div>
          </div>
        </div>
        {save.error && !Object.keys(errors).length ? <div className="banner err mb-4">{errorText(save.error)}</div> : null}

        <Provider title={t("payments.providers.stars")} sub={t("payments.starsSub")} on={form.stars} onChange={set("stars")} live={s.available.stars} offline={form.stars && !s.available.stars ? t("payments.starsBotOff") : ""} />

        <Provider title={t("payments.providers.yookassa")} sub={t("payments.yookassaSub")} on={form.yookassa} onChange={set("yookassa")} live={s.available.yookassa} error={errors.yookassa}>
          <div className="grid gap-x-3 sm:grid-cols-2">
            <Field label={t("payments.shopId")} htmlFor="p-shop" error={errors.yookassa_shop_id}>
              <input id="p-shop" className="input mono" inputMode="numeric" value={form.shop} onChange={(e) => setForm((f) => ({ ...f, shop: e.target.value }))} autoComplete="off" aria-invalid={!!errors.yookassa_shop_id} />
            </Field>
            <Field label={t("payments.secretKey")} htmlFor="p-yk" error={errors.yookassa_secret}>
              <input
                id="p-yk"
                className="input mono"
                type="password"
                value={ykSecret}
                onChange={(e) => setYkSecret(e.target.value)}
                placeholder={s.yookassa_secret_set ? t("payments.keySaved") : "live_…"}
                autoComplete="new-password"
                aria-invalid={!!errors.yookassa_secret}
              />
            </Field>
          </div>
          <Webhook label={t("payments.webhookYooKassa")} url={s.webhook_yookassa} />
        </Provider>

        <Provider title={t("payments.providers.cryptobot")} sub={t("payments.cryptobotSub")} on={form.cryptobot} onChange={set("cryptobot")} live={s.available.cryptobot} error={errors.cryptobot}>
          <Field label={t("payments.cryptoToken")} htmlFor="p-cb" error={errors.cryptobot_token}>
            <input
              id="p-cb"
              className="input mono"
              type="password"
              value={cbToken}
              onChange={(e) => setCbToken(e.target.value)}
              placeholder={s.cryptobot_token_set ? t("payments.keySaved") : "12345:AA…"}
              autoComplete="new-password"
              aria-invalid={!!errors.cryptobot_token}
            />
          </Field>
          <label className="mb-3 flex items-center gap-2 text-[13px]">
            <input type="checkbox" className="check" checked={form.testnet} onChange={(e) => setForm((f) => ({ ...f, testnet: e.target.checked }))} /> {t("payments.testnet")}
          </label>
          <Webhook label={t("payments.webhookCryptoBot")} url={s.webhook_cryptobot} />
        </Provider>

        <div className="mb-4 flex items-start justify-between gap-3 border-t border-[var(--hairline)] pt-4">
          <div>
            <div className="text-[13px] font-semibold">{t("payments.allowNew")}</div>
            <div className="text-xs text-[var(--ink-500)]">{t("payments.allowNewSub")}</div>
          </div>
          <Switch checked={form.allowNew} onChange={set("allowNew")} label={t("payments.allowNew")} />
        </div>
        <div className="mb-4 flex items-start justify-between gap-3">
          <div>
            <div className="text-[13px] font-semibold">{t("payments.resetTraffic")}</div>
            <div className="text-xs text-[var(--ink-500)]">{form.resetTraffic ? t("payments.resetTrafficOn") : t("payments.resetTrafficOff")}</div>
          </div>
          <Switch checked={form.resetTraffic} onChange={set("resetTraffic")} label={t("payments.resetTraffic")} />
        </div>
        <Button type="submit" variant="primary" loading={save.isPending}>
          {t("common.save")}
        </Button>
      </form>
    </section>
  );
}

function Provider({ title, sub, on, onChange, live, offline, error, children }: { title: string; sub: string; on: boolean; onChange: (v: boolean) => void; live: boolean; offline?: string; error?: string; children?: ReactNode }) {
  return (
    <div className="mb-4 border-t border-[var(--hairline)] pt-4 first-of-type:border-t-0 first-of-type:pt-0" role="group" aria-label={title}>
      <div className="mb-3 flex items-start justify-between gap-3">
        <div className="min-w-0">
          <div className="flex flex-wrap items-center gap-2 text-[13px] font-semibold">
            {title}
            {on ? <Pill tone={live ? "ok" : "warn"}>{live ? t("payments.live") : t("payments.notReady")}</Pill> : null}
          </div>
          <div className="text-xs text-[var(--ink-500)]">{sub}</div>
          {offline ? <div className="mt-1 text-xs text-[var(--honey-600)]">{offline}</div> : null}
          {error ? (
            <div className="mt-1 text-xs text-[var(--berry-600)]" role="alert">
              {error}
            </div>
          ) : null}
        </div>
        <Switch checked={on} onChange={onChange} label={title} />
      </div>
      {on ? children : null}
    </div>
  );
}

function Webhook({ label, url }: { label: string; url: string }) {
  const toast = useToast();
  if (!url) return <p className="text-xs text-[var(--ink-500)]">{t("payments.webhookNoHost")}</p>;
  const copy = async () => {
    try {
      await navigator.clipboard.writeText(url);
      toast.ok(t("payments.webhookCopied"));
    } catch {
      toast.error(t("common.copyFailed"));
    }
  };
  return (
    <div>
      <div className="mb-1 text-xs text-[var(--ink-500)]">{label}</div>
      <div className="link-field">
        <span className="mono">{url}</span>
        <button type="button" className="icon-btn" onClick={() => void copy()} aria-label={t("payments.copyWebhook")}>
          <Copy size={18} />
        </button>
      </div>
    </div>
  );
}
