// Top-ups of the accounts' balances by hand: each request with its screenshot, approved or
// rejected by the admin; the balance itself is set up on Payments → Methods.
import { keepPreviousData, useInfiniteQuery, useMutation, useQueryClient } from "@tanstack/react-query";
import { Check, ImageOff, Wallet, X } from "lucide-react";
import { useState } from "react";
import { api, basePath, errorText, unwrap, type Schemas } from "../../api/client";
import { Confirm } from "../../components/overlay";
import { QueryBoundary } from "../../components/query";
import { useToast } from "../../components/toast";
import { Button, EmptyState, Field, Pill, Segmented, Skeleton } from "../../components/ui";
import { t } from "../../i18n";
import { ago, money } from "../../lib/format";
import { pagedItems } from "../../lib/paging";
import { CardHead } from "../../components/layout";

type Topup = Schemas["TopupView"];
type Filter = "pending" | "approved" | "rejected" | "";

const topupsKey = (status: Filter) => ["topups", status] as const;
const TONE = { pending: "warn", approved: "ok", rejected: "bad" } as const;
// A page of requests; the next one starts after the last id shown.
const PAGE = 50;

export function TopupsSection() {
  const [status, setStatus] = useState<Filter>("pending");
  const list = useInfiniteQuery({
    queryKey: topupsKey(status),
    initialPageParam: 0,
    queryFn: ({ pageParam, signal }) => unwrap(api.GET("/api/v1/topups", { params: { query: { status, before: pageParam || undefined, limit: PAGE } }, signal })),
    getNextPageParam: (last) => (last.items.length === PAGE ? last.items[last.items.length - 1]!.id : undefined),
    placeholderData: keepPreviousData,
    refetchInterval: status === "pending" ? 15_000 : false,
  });
  const pending = list.data?.pages[0]?.pending;
  const [rejecting, setRejecting] = useState<Topup | null>(null);
  return (
    <section className="card glass reveal" aria-labelledby="topups-title">
      <CardHead title={t("topups.title")} sub={t("topups.sub")} id="topups-title" />
      <div className="mb-4 max-w-full overflow-x-auto">
        <Segmented
          label={t("topups.filter")}
          value={status}
          onChange={(v) => setStatus(v as Filter)}
          options={[
            { value: "pending", label: pending ? `${t("topups.status.pending")} · ${pending}` : t("topups.status.pending") },
            { value: "approved", label: t("topups.status.approved") },
            { value: "rejected", label: t("topups.status.rejected") },
            { value: "", label: t("topups.all") },
          ]}
        />
      </div>
      <QueryBoundary
        query={list}
        pending={
          <div className="grid gap-3 md:grid-cols-2">
            {[0, 1].map((i) => (
              <Skeleton key={i} style={{ height: 240, borderRadius: 16 }} />
            ))}
          </div>
        }
      >
        {(v) => {
          const items = pagedItems(v.pages);
          return items.length === 0 ? (
            <EmptyState icon={Wallet} title={status === "pending" ? t("topups.emptyPending") : t("topups.empty")} text={t("topups.emptyText")} />
          ) : (
            <>
              <ul className="grid gap-3 md:grid-cols-2">
                {items.map((tp) => (
                  <TopupCard key={tp.id} tp={tp} onReject={() => setRejecting(tp)} />
                ))}
              </ul>
              {list.hasNextPage ? (
                <Button className="mt-3" size="sm" loading={list.isFetchingNextPage} onClick={() => void list.fetchNextPage()}>
                  {t("payments.more")}
                </Button>
              ) : null}
            </>
          );
        }}
      </QueryBoundary>
      <RejectDialog tp={rejecting} onClose={() => setRejecting(null)} />
    </section>
  );
}

function useDecide() {
  const qc = useQueryClient();
  const toast = useToast();
  return useMutation({
    mutationFn: ({ id, approve, reason }: { id: number; approve: boolean; reason?: string }) =>
      unwrap(api.POST("/api/v1/topups/{id}/decide", { params: { path: { id } }, body: { approve, reason } })),
    onSuccess: (tp) => toast.ok(tp.status === "approved" ? t("topups.approved", { amount: money(tp.amount, tp.currency) }) : t("topups.rejected")),
    onError: (e) => toast.error(errorText(e)),
    onSettled: () => void qc.invalidateQueries({ queryKey: ["topups"] }),
  });
}

function TopupCard({ tp, onReject }: { tp: Topup; onReject: () => void }) {
  const decide = useDecide();
  const [broken, setBroken] = useState(false);
  const who = tp.tg_username ? `@${tp.tg_username}` : `tg ${tp.tg_id}`;
  return (
    <li className="flex flex-col overflow-hidden rounded-2xl border border-[var(--hairline)] bg-[var(--surface-soft)]">
      {tp.photo && !broken ? (
        <a href={`${basePath}/api/v1/topups/${tp.id}/photo`} target="_blank" rel="noreferrer" className="block bg-[var(--surface)]" aria-label={t("topups.openPhoto")}>
          <img src={`${basePath}/api/v1/topups/${tp.id}/photo`} alt={t("topups.photoAlt", { who })} loading="lazy" className="h-48 w-full object-contain" onError={() => setBroken(true)} />
        </a>
      ) : (
        <div className="grid h-48 place-items-center bg-[var(--surface)] text-[var(--ink-500)]">
          <span className="flex items-center gap-2 text-[13px]">
            <ImageOff size={16} aria-hidden /> {t("topups.noPhoto")}
          </span>
        </div>
      )}
      <div className="flex flex-1 flex-col gap-2 p-4">
        <div className="flex items-start justify-between gap-3">
          <div className="min-w-0">
            <div className="num text-lg font-semibold">{money(tp.amount, tp.currency)}</div>
            <div className="truncate text-[13px] text-[var(--ink-600)]">
              {who} · {ago(tp.created_at)}
            </div>
          </div>
          <Pill tone={TONE[tp.status]}>{t(`topups.status.${tp.status}`)}</Pill>
        </div>
        <div className="text-xs text-[var(--ink-500)]">{t("topups.balanceNow", { amount: money(tp.balance, tp.currency) })}</div>
        {tp.reason ? <p className="text-[13px] text-[var(--ink-700)]">{tp.reason}</p> : null}
        {tp.status === "pending" ? (
          <div className="mt-auto flex gap-2 pt-2">
            <Button size="sm" variant="primary" loading={decide.isPending && decide.variables?.approve} disabled={decide.isPending} onClick={() => decide.mutate({ id: tp.id, approve: true })}>
              <Check size={16} aria-hidden /> {t("topups.approve")}
            </Button>
            <Button size="sm" variant="danger" disabled={decide.isPending} onClick={onReject}>
              <X size={16} aria-hidden /> {t("topups.reject")}
            </Button>
          </div>
        ) : null}
      </div>
    </li>
  );
}

function RejectDialog({ tp, onClose }: { tp: Topup | null; onClose: () => void }) {
  const decide = useDecide();
  const [reason, setReason] = useState("");
  const close = () => {
    onClose();
    setReason("");
  };
  return (
    <Confirm
      open={!!tp}
      onOpenChange={(v) => !v && close()}
      title={t("topups.rejectTitle", { amount: tp ? money(tp.amount, tp.currency) : "" })}
      text={t("topups.rejectText")}
      confirm={t("topups.reject")}
      danger
      loading={decide.isPending}
      onConfirm={() => tp && decide.mutate({ id: tp.id, approve: false, reason: reason.trim() || undefined }, { onSuccess: close })}
    >
      <Field label={t("topups.reason")} htmlFor="topup-reason" hint={t("topups.reasonHint")}>
        <input id="topup-reason" className="input" value={reason} onChange={(e) => setReason(e.target.value)} maxLength={200} placeholder={t("topups.reasonPlaceholder")} />
      </Field>
    </Confirm>
  );
}
