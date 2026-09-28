import { useMutation, useQueryClient } from "@tanstack/react-query";
import { Pencil, Plus, Trash2, TriangleAlert } from "lucide-react";
import { useEffect, useState, type FormEvent } from "react";
import { api, ApiError, errorText, unwrap, type Inbound, type Schemas } from "../../api/client";
import { qk, useInbounds, usePresets } from "../../api/hooks";
import { Confirm, Drawer } from "../../components/overlay";
import { useToast } from "../../components/toast";
import { Button, EmptyState, ErrorState, Field, PageHeader, Pill, Skeleton, Switch } from "../../components/ui";

export function InboundsPage() {
  const inbounds = useInbounds();
  const qc = useQueryClient();
  const toast = useToast();
  const [adding, setAdding] = useState(false);
  const [editing, setEditing] = useState<Inbound | null>(null);
  const [removing, setRemoving] = useState<Inbound | null>(null);
  const patch = useMutation({
    mutationFn: ({ id, body }: { id: number; body: Schemas["PatchInboundInputBody"] }) => unwrap(api.PATCH("/api/v1/inbounds/{id}", { params: { path: { id } }, body })),
    onSettled: () => void qc.invalidateQueries({ queryKey: qk.inbounds }),
    onError: (e) => toast.error(errorText(e)),
  });
  const remove = useMutation({
    mutationFn: (id: number) => unwrap(api.DELETE("/api/v1/inbounds/{id}", { params: { path: { id } } })),
    onSuccess: () => {
      toast.ok("Подключение удалено");
      setRemoving(null);
    },
    onSettled: () => void qc.invalidateQueries({ queryKey: qk.inbounds }),
    onError: (e) => toast.error(errorText(e)),
  });

  return (
    <>
      <PageHeader
        title="Подключения"
        sub="Протоколы, по которым клиенты заходят на сервер. Ключи и пароли создаются автоматически."
        actions={
          <Button variant="primary" onClick={() => setAdding(true)}>
            <Plus size={18} aria-hidden />
            <span className="max-[760px]:hidden">Добавить</span>
          </Button>
        }
      />
      <div className="banner warn">
        <TriangleAlert size={18} className="shrink-0" aria-hidden />
        <span>Смена порта или маскировки переподключит клиентов этого протокола на несколько секунд. Hysteria2 и TUIC переподключаются дольше — до 30 секунд.</span>
      </div>
      {inbounds.isPending ? (
        <div className="grid gap-4 lg:grid-cols-2">
          {[0, 1, 2, 3].map((i) => (
            <Skeleton key={i} style={{ height: 150, borderRadius: 20 }} />
          ))}
        </div>
      ) : inbounds.isError ? (
        <section className="card glass">
          <ErrorState text={errorText(inbounds.error)} onRetry={() => void inbounds.refetch()} />
        </section>
      ) : inbounds.data.length === 0 ? (
        <section className="card glass">
          <EmptyState title="Нет ни одного подключения" text="Без них клиенты не смогут подключиться. Начните с VLESS REALITY — он лучше всего работает в РФ.">
            <Button variant="primary" onClick={() => setAdding(true)}>
              <Plus size={18} aria-hidden /> Добавить подключение
            </Button>
          </EmptyState>
        </section>
      ) : (
        <div className="grid gap-4 lg:grid-cols-2">
          {inbounds.data.map((i, idx) => (
            <section key={i.id} className="card glass reveal" style={{ "--i": idx } as React.CSSProperties}>
              <div className="flex items-start justify-between gap-3">
                <div className="min-w-0">
                  <h2 className="font-display text-lg font-medium tracking-tight">{i.title}</h2>
                  <div className="mt-1 flex flex-wrap items-center gap-2 text-xs text-[var(--ink-500)]">
                    <span className="mono">{i.name}</span>
                    <span>·</span>
                    <span>
                      порт {i.port}/{i.network}
                    </span>
                  </div>
                </div>
                <Switch checked={i.enabled} label={`Включить ${i.title}`} disabled={patch.isPending} onChange={(v) => patch.mutate({ id: i.id, body: { enabled: v } }, { onSuccess: () => toast.ok(v ? "Включено" : "Выключено") })} />
              </div>
              <div className="mt-4 flex flex-wrap items-center gap-2">
                {!i.enabled ? <Pill tone="off">выключено</Pill> : i.status === "ok" ? <Pill tone="ok">работает</Pill> : i.status === "error" ? <Pill tone="bad">ошибка</Pill> : <Pill tone="off">проверяем…</Pill>}
                {i.dest ? <span className="text-xs text-[var(--ink-500)]">маскировка под {i.dest}</span> : null}
              </div>
              {i.status === "error" && i.error ? (
                <p className="mt-3 text-[13px] text-[var(--berry-600)]" role="alert">
                  {humanListenerError(i.error)}
                </p>
              ) : null}
              <div className="mt-4 flex gap-2 border-t border-[var(--hairline)] pt-4">
                <Button size="sm" onClick={() => setEditing(i)}>
                  <Pencil size={16} aria-hidden /> Настроить
                </Button>
                <Button size="sm" variant="danger" onClick={() => setRemoving(i)}>
                  <Trash2 size={16} aria-hidden /> Удалить
                </Button>
              </div>
            </section>
          ))}
        </div>
      )}
      <AddDrawer open={adding} onOpenChange={setAdding} />
      <EditDrawer inbound={editing} onClose={() => setEditing(null)} />
      <Confirm
        open={!!removing}
        onOpenChange={(v) => !v && setRemoving(null)}
        title={`Удалить «${removing?.title ?? ""}»?`}
        text="Клиенты, которые подключаются этим протоколом, потеряют связь, пока не переключатся на другой. Подписки обновятся сами."
        confirm="Удалить"
        danger
        loading={remove.isPending}
        onConfirm={() => removing && remove.mutate(removing.id)}
      />
    </>
  );
}

function humanListenerError(e: string): string {
  if (e.includes("address already in use")) return "Порт уже занят другой программой на сервере. Выберите другой порт или освободите этот.";
  if (e.includes("permission denied")) return "Нет прав открыть этот порт. Проверьте настройки контейнера.";
  return e;
}

function AddDrawer({ open, onOpenChange }: { open: boolean; onOpenChange: (v: boolean) => void }) {
  const presets = usePresets();
  const qc = useQueryClient();
  const toast = useToast();
  const [preset, setPreset] = useState<string>("vless_reality_vision");
  const [port, setPort] = useState("");
  const [error, setError] = useState("");
  useEffect(() => {
    if (open) {
      setPort("");
      setError("");
    }
  }, [open]);
  const create = useMutation({
    mutationFn: (body: Schemas["CreateInboundInputBody"]) => unwrap(api.POST("/api/v1/inbounds", { body })),
    onSuccess: (r) => {
      void qc.invalidateQueries({ queryKey: qk.inbounds });
      toast.ok(`«${r.title}» добавлено на порт ${r.port}`);
      onOpenChange(false);
    },
    onError: (e) => setError(e instanceof ApiError ? (e.fields.port ?? errorText(e)) : errorText(e)),
  });
  const chosen = presets.data?.find((p) => p.id === preset);
  const submit = (e: FormEvent) => {
    e.preventDefault();
    create.mutate({ preset: preset as Schemas["CreateInboundInputBody"]["preset"], port: port.trim() || undefined });
  };
  return (
    <Drawer
      open={open}
      onOpenChange={onOpenChange}
      title="Новое подключение"
      meta="Ключи, пароли и пути сгенерируются сами"
      footer={
        <>
          <Button variant="ghost" onClick={() => onOpenChange(false)}>
            Отмена
          </Button>
          <Button variant="primary" type="submit" form="add-inbound" loading={create.isPending}>
            Добавить
          </Button>
        </>
      }
    >
      <form id="add-inbound" onSubmit={submit} className="pt-5" noValidate>
        <Field label="Протокол">
          <div className="grid gap-2" role="radiogroup" aria-label="Протокол">
            {(presets.data ?? []).map((p) => (
              <button key={p.id} type="button" role="radio" aria-checked={preset === p.id} className="opt" onClick={() => setPreset(p.id)}>
                <span className="font-semibold">{p.title}</span>
                <span className="text-xs text-[var(--ink-500)]">{p.summary}</span>
              </button>
            ))}
          </div>
        </Field>
        <Field label="Порт" htmlFor="in-port" hint={`По умолчанию ${chosen?.default_port ?? "—"}/${chosen?.network ?? ""}. Для Hysteria2 можно диапазон, например 20000-20100.`} error={error}>
          <input id="in-port" className="input max-w-[200px]" inputMode="numeric" placeholder={chosen?.default_port} value={port} onChange={(e) => setPort(e.target.value)} aria-invalid={!!error} />
        </Field>
      </form>
    </Drawer>
  );
}

function EditDrawer({ inbound, onClose }: { inbound: Inbound | null; onClose: () => void }) {
  const qc = useQueryClient();
  const toast = useToast();
  const [port, setPort] = useState("");
  const [dest, setDest] = useState("");
  const [errors, setErrors] = useState<Record<string, string>>({});
  useEffect(() => {
    if (!inbound) return;
    setPort(inbound.port);
    setDest(inbound.dest ?? "");
    setErrors({});
  }, [inbound]);
  const save = useMutation({
    mutationFn: (body: Schemas["PatchInboundInputBody"]) => unwrap(api.PATCH("/api/v1/inbounds/{id}", { params: { path: { id: inbound!.id } }, body })),
    onSuccess: () => {
      void qc.invalidateQueries({ queryKey: qk.inbounds });
      toast.ok("Сохранено — клиенты получат изменения при обновлении подписки");
      onClose();
    },
    onError: (e) => {
      if (e instanceof ApiError && Object.keys(e.fields).length) setErrors(e.fields);
      else toast.error(errorText(e));
    },
  });
  const submit = (e: FormEvent) => {
    e.preventDefault();
    const body: Schemas["PatchInboundInputBody"] = {};
    if (port !== inbound?.port) body.port = port.trim();
    if (inbound?.dest !== undefined && dest !== inbound.dest) body.dest = dest.trim();
    if (Object.keys(body).length === 0) {
      onClose();
      return;
    }
    save.mutate(body);
  };
  return (
    <Drawer
      open={!!inbound}
      onOpenChange={(v) => !v && onClose()}
      title={inbound?.title ?? ""}
      meta={inbound ? <span className="mono">{inbound.name}</span> : undefined}
      footer={
        <>
          <Button variant="ghost" onClick={onClose}>
            Отмена
          </Button>
          <Button variant="primary" type="submit" form="edit-inbound" loading={save.isPending}>
            Сохранить
          </Button>
        </>
      }
    >
      <form id="edit-inbound" onSubmit={submit} className="pt-5" noValidate>
        <Field label="Порт" htmlFor="ed-port" error={errors.port}>
          <input id="ed-port" className="input max-w-[200px]" inputMode="numeric" value={port} onChange={(e) => setPort(e.target.value)} aria-invalid={!!errors.port} />
        </Field>
        {inbound?.dest !== undefined ? (
          <Field
            label="Сайт для маскировки (REALITY)"
            htmlFor="ed-dest"
            error={errors.dest}
            hint="Сайт с TLS 1.3 и HTTP/2, лучше в той же сети, что и сервер. Трафик клиентов снаружи выглядит как заход на этот сайт."
          >
            <input id="ed-dest" className="input mono" value={dest} onChange={(e) => setDest(e.target.value)} placeholder="www.microsoft.com:443" aria-invalid={!!errors.dest} spellCheck={false} />
          </Field>
        ) : null}
      </form>
    </Drawer>
  );
}
