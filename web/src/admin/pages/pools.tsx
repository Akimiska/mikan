// Traffic pools (GitHub issue #6): chosen protocols count to a pool with its own limit,
// apart from the main traffic. Pools are made here; protocols join one in their own
// settings, and tariffs and users give each pool its limit.
import { useMutation, useQueryClient } from "@tanstack/react-query";
import { Pencil, Plus, Trash2 } from "lucide-react";
import { useState, type FormEvent } from "react";
import { api, ApiError, errorText, unwrap, type Schemas } from "../../api/client";
import { qk, usePools } from "../../api/hooks";
import { Confirm } from "../../components/overlay";
import { useToast } from "../../components/toast";
import { Button, ErrorState, Skeleton } from "../../components/ui";
import { t } from "../../i18n";

type Pool = Schemas["PoolView"];

export function PoolsCard() {
  const pools = usePools();
  const qc = useQueryClient();
  const toast = useToast();
  const [name, setName] = useState("");
  const [renaming, setRenaming] = useState<{ id: number; name: string } | null>(null);
  const [removing, setRemoving] = useState<Pool | null>(null);
  const done = () => {
    void qc.invalidateQueries({ queryKey: qk.pools });
    void qc.invalidateQueries({ queryKey: qk.tariffs });
    void qc.invalidateQueries({ queryKey: qk.inbounds });
  };
  const create = useMutation({
    mutationFn: () => unwrap(api.POST("/api/v1/pools", { body: { name: name.trim() } })),
    onSuccess: () => {
      setName("");
      done();
      toast.ok(t("pools.created"));
    },
  });
  const rename = useMutation({
    mutationFn: (p: { id: number; name: string }) => unwrap(api.PATCH("/api/v1/pools/{id}", { params: { path: { id: p.id } }, body: { name: p.name.trim() } })),
    onSuccess: () => {
      setRenaming(null);
      done();
    },
    onError: (e) => toast.error(errorText(e)),
  });
  const remove = useMutation({
    mutationFn: (id: number) => unwrap(api.DELETE("/api/v1/pools/{id}", { params: { path: { id } } })),
    onSuccess: () => {
      setRemoving(null);
      done();
      toast.ok(t("pools.deleted"));
    },
    onError: (e) => toast.error(errorText(e)),
  });
  const createError = create.error instanceof ApiError ? (create.error.fields.name ?? errorText(create.error)) : create.error ? errorText(create.error) : "";
  const submit = (e: FormEvent) => {
    e.preventDefault();
    if (name.trim()) create.mutate();
  };

  return (
    <section className="card glass reveal mb-4">
      <div className="card-head">
        <div>
          <h2 className="card-title">{t("pools.title")}</h2>
          <div className="card-sub">{t("pools.sub")}</div>
        </div>
      </div>
      {pools.isPending ? (
        <Skeleton style={{ height: 64 }} />
      ) : pools.isError ? (
        <ErrorState text={errorText(pools.error)} onRetry={() => void pools.refetch()} />
      ) : pools.data.length === 0 ? (
        <p className="mb-3 text-xs text-[var(--ink-500)]">{t("pools.none")}</p>
      ) : (
        <ul className="row-list mb-3">
          {pools.data.map((p) => (
            <li key={p.id} className="flex flex-wrap items-center justify-between gap-3 py-3">
              {renaming?.id === p.id ? (
                <form
                  className="flex flex-1 items-center gap-2"
                  onSubmit={(e) => {
                    e.preventDefault();
                    rename.mutate(renaming);
                  }}
                >
                  <input className="input max-w-[240px]" value={renaming.name} onChange={(e) => setRenaming({ id: p.id, name: e.target.value })} maxLength={40} aria-label={t("pools.name")} autoFocus />
                  <Button size="sm" type="submit" variant="primary" loading={rename.isPending} disabled={!renaming.name.trim()}>
                    {t("common.save")}
                  </Button>
                  <Button size="sm" variant="ghost" onClick={() => setRenaming(null)}>
                    {t("common.cancel")}
                  </Button>
                </form>
              ) : (
                <>
                  <div className="min-w-0">
                    <div className="text-[13px] font-semibold">{p.name}</div>
                    <div className="text-xs text-[var(--ink-500)]">{p.inbounds.length ? p.inbounds.join(", ") : t("pools.empty")}</div>
                  </div>
                  <div className="flex gap-1">
                    <button type="button" className="icon-btn" aria-label={t("pools.rename", { name: p.name })} onClick={() => setRenaming({ id: p.id, name: p.name })}>
                      <Pencil size={16} />
                    </button>
                    <button type="button" className="icon-btn" aria-label={t("pools.delete", { name: p.name })} onClick={() => setRemoving(p)}>
                      <Trash2 size={16} />
                    </button>
                  </div>
                </>
              )}
            </li>
          ))}
        </ul>
      )}
      <form className="flex flex-wrap items-start gap-2" onSubmit={submit}>
        <div>
          <input className="input max-w-[240px]" value={name} onChange={(e) => setName(e.target.value)} maxLength={40} placeholder={t("pools.placeholder")} aria-label={t("pools.name")} aria-invalid={!!createError} />
          {createError ? (
            <p className="mt-1 text-xs text-[var(--berry-600)]" role="alert">
              {createError}
            </p>
          ) : null}
        </div>
        <Button type="submit" loading={create.isPending} disabled={!name.trim()}>
          <Plus size={16} aria-hidden /> {t("pools.add")}
        </Button>
      </form>
      <p className="mt-3 text-xs text-[var(--ink-500)]">{t("pools.hint")}</p>
      <Confirm
        open={!!removing}
        onOpenChange={(v) => !v && setRemoving(null)}
        title={t("pools.deleteTitle", { name: removing?.name ?? "" })}
        text={t("pools.deleteText")}
        confirm={t("common.delete")}
        danger
        loading={remove.isPending}
        onConfirm={() => removing && remove.mutate(removing.id)}
      />
    </section>
  );
}

/** Pool limits of a tariff or a user: GB per pool, empty = unlimited. */
export function PoolLimitsField({
  pools,
  value,
  onChange,
}: {
  pools: Pool[];
  value: Record<number, string>;
  onChange: (v: Record<number, string>) => void;
}) {
  if (pools.length === 0) return null;
  return (
    <div className="grid gap-2">
      {pools.map((p) => (
        <label key={p.id} className="flex items-center gap-2 text-[13px]">
          <span className="w-32 shrink-0 truncate font-medium">{p.name}</span>
          <input
            className="input max-w-[120px]"
            inputMode="decimal"
            value={value[p.id] ?? ""}
            onChange={(e) => onChange({ ...value, [p.id]: e.target.value })}
            placeholder={t("pools.unlimited")}
            aria-label={t("pools.limitOf", { name: p.name })}
          />
          <span className="text-[var(--ink-500)]">{t("units.gb")}</span>
        </label>
      ))}
    </div>
  );
}
