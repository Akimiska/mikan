import { useMutation, useQueryClient } from "@tanstack/react-query";
import { CircleCheck, Pencil, Plus, RotateCcw, Trash2, TriangleAlert } from "lucide-react";
import { lazy, Suspense, useEffect, useState, type FormEvent } from "react";
import { api, ApiError, errorText, unwrap, type Inbound, type Preset, type Schemas } from "../../api/client";
import { qk, useInbounds, useNodes, usePresets } from "../../api/hooks";
import { Confirm, Drawer } from "../../components/overlay";
import { useToast } from "../../components/toast";
import { Button, EmptyState, ErrorState, Field, PageHeader, Pill, Segmented, Skeleton, Switch } from "../../components/ui";
import { t, tMaybe } from "../../i18n";

import { nodeLabel } from "./nodes";

const ConfigEditor = lazy(() => import("../../components/config-editor"));

/** Protocol names are the same in every language; the one-line pitch is translated. */
function presetSummary(p: Preset): string {
  return tMaybe(`presets.${p.id}`) ?? p.summary;
}

function presetTitle(p: Preset): string {
  return p.id === "custom" ? t("inbounds.customTitle") : p.title;
}

function Editor(props: { value: string; onChange: (v: string) => void; invalid?: boolean }) {
  return (
    <Suspense fallback={<Skeleton style={{ height: 280, borderRadius: 12 }} />}>
      <ConfigEditor {...props} label={t("inbounds.configLabel")} />
    </Suspense>
  );
}

export function InboundsPage() {
  const all = useInbounds();
  const nodes = useNodes();
  const qc = useQueryClient();
  const toast = useToast();
  const [nodeId, setNodeId] = useState(1);
  const multi = (nodes.data?.length ?? 0) > 1;
  const node = nodes.data?.find((n) => n.id === nodeId);
  // One node: its inbounds are all there is; several: the chosen node's.
  const inbounds = { ...all, data: all.data?.filter((i) => !multi || i.node_id === nodeId) } as typeof all;
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
      toast.ok(t("inbounds.deleted"));
      setRemoving(null);
    },
    onSettled: () => void qc.invalidateQueries({ queryKey: qk.inbounds }),
    onError: (e) => toast.error(errorText(e)),
  });

  return (
    <>
      <PageHeader
        title={t("nav.inbounds")}
        sub={t("inbounds.subtitle")}
        actions={
          <Button variant="primary" onClick={() => setAdding(true)}>
            <Plus size={18} aria-hidden />
            <span className="max-[760px]:hidden">{t("common.add")}</span>
          </Button>
        }
      />
      <div className="banner warn">
        <TriangleAlert size={18} className="shrink-0" aria-hidden />
        <span>{t("inbounds.reconnectWarning")}</span>
      </div>
      {multi && nodes.data ? (
        <div className="mb-4">
          <Segmented
            value={String(nodeId)}
            label={t("inbounds.node")}
            options={nodes.data.map((n) => ({ value: String(n.id), label: nodeLabel(n) }))}
            onChange={(v) => setNodeId(Number(v))}
          />
        </div>
      ) : null}
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
          <EmptyState title={multi ? t("inbounds.nodeEmptyTitle") : t("inbounds.emptyTitle")} text={multi ? t("inbounds.nodeEmptyText") : t("inbounds.emptyText")}>
            <Button variant="primary" onClick={() => setAdding(true)}>
              <Plus size={18} aria-hidden /> {t("inbounds.add")}
            </Button>
          </EmptyState>
        </section>
      ) : (
        <div className="grid gap-4 lg:grid-cols-2">
          {inbounds.data.map((i, idx) => (
            <section key={i.id} className="card glass reveal" style={{ "--i": idx } as React.CSSProperties}>
              <div className="flex items-start justify-between gap-3">
                <div className="min-w-0">
                  <h2 className="font-display truncate text-lg font-medium tracking-tight">{i.sub_name}</h2>
                  <div className="mt-1 flex flex-wrap items-center gap-2 text-xs text-[var(--ink-500)]">
                    <span>{i.preset === "custom" ? t("inbounds.customOf", { type: i.type }) : i.title}</span>
                    <span>·</span>
                    <span>{t("inbounds.port", { port: i.port, network: i.network })}</span>
                    <span>·</span>
                    <span className="mono">{i.name}</span>
                  </div>
                </div>
                <Switch
                  checked={i.enabled}
                  label={t("inbounds.toggle", { name: i.sub_name })}
                  disabled={patch.isPending}
                  onChange={(v) => patch.mutate({ id: i.id, body: { enabled: v } }, { onSuccess: () => toast.ok(v ? t("inbounds.enabled") : t("inbounds.disabled")) })}
                />
              </div>
              <div className="mt-4 flex flex-wrap items-center gap-2">
                {!i.enabled ? (
                  <Pill tone="off">{t("inbounds.off")}</Pill>
                ) : i.status === "ok" ? (
                  <Pill tone="ok">{t("inbounds.working")}</Pill>
                ) : i.status === "error" ? (
                  <Pill tone="bad">{t("inbounds.error")}</Pill>
                ) : (
                  <Pill tone="off">{t("inbounds.checking")}</Pill>
                )}
                {i.dest ? <span className="text-xs text-[var(--ink-500)]">{t("inbounds.maskedAs", { dest: i.dest })}</span> : null}
              </div>
              {i.status === "error" && i.error ? (
                <p className="mt-3 text-[13px] text-[var(--berry-600)]" role="alert">
                  {listenerError(i.error)}
                </p>
              ) : null}
              <div className="mt-4 flex gap-2 border-t border-[var(--hairline)] pt-4">
                <Button size="sm" onClick={() => setEditing(i)}>
                  <Pencil size={16} aria-hidden /> {t("inbounds.configure")}
                </Button>
                <Button size="sm" variant="danger" onClick={() => setRemoving(i)}>
                  <Trash2 size={16} aria-hidden /> {t("common.delete")}
                </Button>
              </div>
            </section>
          ))}
        </div>
      )}
      <AddDrawer open={adding} onOpenChange={setAdding} nodeId={multi ? nodeId : 1} nodeName={multi && node ? nodeLabel(node) : undefined} />
      <EditDrawer inbound={editing} onClose={() => setEditing(null)} />
      <Confirm
        open={!!removing}
        onOpenChange={(v) => !v && setRemoving(null)}
        title={t("inbounds.deleteTitle", { name: removing?.sub_name ?? "" })}
        text={t("inbounds.deleteText")}
        confirm={t("common.delete")}
        danger
        loading={remove.isPending}
        onConfirm={() => removing && remove.mutate(removing.id)}
      />
    </>
  );
}

function listenerError(e: string): string {
  if (e.includes("address already in use")) return t("inbounds.errPortBusy");
  if (e.includes("permission denied")) return t("inbounds.errPermission");
  return e;
}

/** Checks a template on the server (mikan's rules, then mihomo's parser on the node). */
function useValidate() {
  return useMutation({
    mutationFn: (body: Schemas["ValidateInboundInputBody"]) => unwrap(api.POST("/api/v1/inbounds/validate", { body })),
  });
}

function ValidateResult({ v }: { v: ReturnType<typeof useValidate> }) {
  if (v.isSuccess) {
    return (
      <span className="inline-flex items-center gap-1.5 text-[13px] text-[var(--leaf-700)]" role="status">
        <CircleCheck size={16} aria-hidden /> {t("inbounds.validOk", { type: v.data.type, network: v.data.network })}
      </span>
    );
  }
  if (v.isError) {
    const msg = v.error instanceof ApiError ? (v.error.fields.config ?? errorText(v.error)) : errorText(v.error);
    return (
      <span className="text-[13px] text-[var(--berry-600)]" role="alert">
        {msg}
      </span>
    );
  }
  return null;
}

function AddDrawer({ open, onOpenChange, nodeId, nodeName }: { open: boolean; onOpenChange: (v: boolean) => void; nodeId: number; nodeName?: string }) {
  const presets = usePresets();
  const qc = useQueryClient();
  const toast = useToast();
  const validate = useValidate();
  const [preset, setPreset] = useState<string>("vless_reality_xhttp");
  const [port, setPort] = useState("");
  const [config, setConfig] = useState("");
  const [errors, setErrors] = useState<Record<string, string>>({});
  useEffect(() => {
    if (open) {
      setPort("");
      setErrors({});
      setConfig(t("inbounds.customSkeleton"));
      validate.reset();
    }
    // `validate` changes identity on every render; reset only when the drawer opens.
  }, [open]);
  const editConfig = (v: string) => {
    setConfig(v);
    setErrors(({ config: _, ...rest }) => rest);
    validate.reset();
  };
  const create = useMutation({
    mutationFn: (body: Schemas["CreateInboundInputBody"]) => unwrap(api.POST("/api/v1/inbounds", { body })),
    onSuccess: (r) => {
      void qc.invalidateQueries({ queryKey: qk.inbounds });
      toast.ok(t("inbounds.addedOn", { name: r.sub_name, port: r.port }));
      onOpenChange(false);
    },
    onError: (e) => {
      validate.reset();
      if (e instanceof ApiError && Object.keys(e.fields).length) setErrors(e.fields);
      else toast.error(errorText(e));
    },
  });
  const custom = preset === "custom";
  const chosen = presets.data?.find((p) => p.id === preset);
  const submit = (e: FormEvent) => {
    e.preventDefault();
    if (custom && !port.trim()) {
      setErrors({ port: t("inbounds.portRequired") });
      return;
    }
    create.mutate({ preset: preset as Schemas["CreateInboundInputBody"]["preset"], node_id: nodeId, port: port.trim() || undefined, config: custom ? config : undefined });
  };
  return (
    <Drawer
      open={open}
      onOpenChange={onOpenChange}
      title={t("inbounds.newTitle")}
      meta={nodeName ? t("inbounds.newOn", { name: nodeName }) : custom ? t("inbounds.customMeta") : t("inbounds.newMeta")}
      footer={
        <>
          <Button variant="ghost" onClick={() => onOpenChange(false)}>
            {t("common.cancel")}
          </Button>
          <Button variant="primary" type="submit" form="add-inbound" loading={create.isPending}>
            {t("common.add")}
          </Button>
        </>
      }
    >
      <form id="add-inbound" onSubmit={submit} className="pt-5" noValidate>
        <Field label={t("inbounds.protocol")}>
          <div className="grid gap-2" role="radiogroup" aria-label={t("inbounds.protocol")}>
            {(presets.data ?? []).map((p) => (
              <button key={p.id} type="button" role="radio" aria-checked={preset === p.id} className="opt" onClick={() => setPreset(p.id)}>
                <span className="font-semibold">{presetTitle(p)}</span>
                <span className="text-xs text-[var(--ink-500)]">{presetSummary(p)}</span>
              </button>
            ))}
          </div>
        </Field>
        <Field
          label={t("inbounds.portLabel")}
          htmlFor="in-port"
          hint={custom ? t("inbounds.portHintCustom") : t("inbounds.portHint", { port: chosen?.default_port ?? "—", network: chosen?.network ?? "" })}
          error={errors.port}
        >
          <input id="in-port" className="input max-w-[200px]" inputMode="numeric" placeholder={chosen?.default_port} value={port} onChange={(e) => setPort(e.target.value)} aria-invalid={!!errors.port} />
        </Field>
        {custom ? (
          <Field label={t("inbounds.configLabel")} hint={t("inbounds.configHint")} error={errors.config}>
            <Editor value={config} onChange={editConfig} invalid={!!errors.config} />
            <div className="mt-2 flex flex-wrap items-center gap-3">
              <Button size="sm" loading={validate.isPending} onClick={() => validate.mutate({ config, node_id: nodeId, port: port.trim() || undefined })}>
                {t("inbounds.validate")}
              </Button>
              <ValidateResult v={validate} />
            </div>
          </Field>
        ) : null}
      </form>
    </Drawer>
  );
}

type Target = Schemas["Result"];

function TargetBadges({ r }: { r: Target }) {
  if (r.error) {
    return <span className="text-xs text-[var(--berry-600)]">{tMaybe(`inbounds.targetErr.${r.error}`) ?? r.error}</span>;
  }
  const items: [string, boolean][] = [
    ["TLS 1.3", r.tls13],
    ["HTTP/2", r.h2],
    ["X25519", r.x25519],
    [t("inbounds.targetCert"), r.cert_valid],
  ];
  return (
    <span className="flex flex-wrap items-center gap-1.5">
      {items.map(([label, ok]) => (
        <Pill key={label} tone={ok ? "ok" : "bad"}>
          {label}
        </Pill>
      ))}
      <span className="num text-xs text-[var(--ink-500)]">{t("inbounds.targetRtt", { ms: r.rtt_ms })}</span>
    </span>
  );
}

/** Check the camouflage site or pick one next to the server (and the self-steal option). */
function TargetPicker({ dest, nodeId, onPick }: { dest: string; nodeId: number; onPick: (dest: string, sni: string) => void }) {
  const check = useMutation({ mutationFn: (d: string) => unwrap(api.POST("/api/v1/inbounds/check-target", { body: { dest: d } })) });
  const scan = useMutation({ mutationFn: () => unwrap(api.POST("/api/v1/inbounds/scan-targets", { params: { query: { node_id: nodeId } } })) });
  const candidates = scan.data ? [...(scan.data.self_steal?.ok ? [scan.data.self_steal] : []), ...scan.data.results] : [];
  return (
    <div className="-mt-2 mb-4">
      <div className="flex flex-wrap gap-2">
        <Button size="sm" loading={check.isPending} disabled={!dest.trim()} onClick={() => check.mutate(dest.trim())}>
          {t("inbounds.targetCheck")}
        </Button>
        <Button size="sm" loading={scan.isPending} onClick={() => scan.mutate()}>
          {t("inbounds.targetScan")}
        </Button>
      </div>
      {check.data ? (
        <div className="panel-soft mt-2 flex flex-col gap-1.5 p-3" role="status">
          <span className="text-[13px] font-medium">{check.data.ok ? t("inbounds.targetOk") : t("inbounds.targetBad")}</span>
          <TargetBadges r={check.data} />
        </div>
      ) : check.isError ? (
        <p className="mt-2 text-[13px] text-[var(--berry-600)]" role="alert">
          {errorText(check.error)}
        </p>
      ) : null}
      {scan.isPending ? (
        <div className="mt-2 flex flex-col gap-2" aria-busy>
          <span className="text-xs text-[var(--ink-500)]">{t("inbounds.targetScanning")}</span>
          <Skeleton style={{ height: 52, borderRadius: 12 }} />
          <Skeleton style={{ height: 52, borderRadius: 12 }} />
        </div>
      ) : scan.isError ? (
        <p className="mt-2 text-[13px] text-[var(--berry-600)]" role="alert">
          {errorText(scan.error)}
        </p>
      ) : scan.data ? (
        <div className="mt-2 flex flex-col gap-2">
          <span className="text-xs text-[var(--ink-500)]">
            {candidates.length ? t("inbounds.targetFound", { n: candidates.length, scanned: scan.data.scanned }) : t("inbounds.targetNone", { scanned: scan.data.scanned })}
          </span>
          {candidates.map((c) => {
            const self = c === scan.data?.self_steal;
            return (
              <div key={c.dest + c.sni} className="panel-soft grid grid-cols-[minmax(0,1fr)_auto] items-center gap-3 p-3">
                <div className="min-w-0">
                  <div className="truncate text-[13px] font-medium">{self ? t("inbounds.targetSelfSteal", { sni: c.sni }) : c.sni}</div>
                  <div className="mono truncate text-xs text-[var(--ink-500)]">{self ? t("inbounds.targetSelfStealHint") : c.dest}</div>
                  <div className="mt-1.5">
                    <TargetBadges r={c} />
                  </div>
                </div>
                <Button size="sm" variant="primary" onClick={() => onPick(c.dest, c.sni)}>
                  {t("inbounds.targetPick")}
                </Button>
              </div>
            );
          })}
        </div>
      ) : null}
    </div>
  );
}

function EditDrawer({ inbound, onClose }: { inbound: Inbound | null; onClose: () => void }) {
  const qc = useQueryClient();
  const toast = useToast();
  const presets = usePresets();
  const validate = useValidate();
  const [tab, setTab] = useState<"main" | "config">("main");
  const [port, setPort] = useState("");
  const [dest, setDest] = useState("");
  const [sni, setSni] = useState(""); // set when a picked target has an IP dest
  const [name, setName] = useState("");
  const [config, setConfig] = useState("");
  const [errors, setErrors] = useState<Record<string, string>>({});
  useEffect(() => {
    if (!inbound) return;
    setTab("main");
    setPort(inbound.port);
    setDest(inbound.dest ?? "");
    setSni("");
    setName(inbound.display_name);
    setConfig(inbound.config);
    setErrors({});
    validate.reset();
    // `validate` changes identity on every render; reset only for another inbound.
  }, [inbound]);
  const defaultName = presets.data?.find((p) => p.id === inbound?.preset)?.sub_name ?? "";
  const editConfig = (v: string) => {
    setConfig(v);
    setErrors(({ config: _, ...rest }) => rest);
    validate.reset();
  };
  const save = useMutation({
    mutationFn: (body: Schemas["PatchInboundInputBody"]) => unwrap(api.PATCH("/api/v1/inbounds/{id}", { params: { path: { id: inbound!.id } }, body })),
    onSuccess: () => {
      void qc.invalidateQueries({ queryKey: qk.inbounds });
      toast.ok(t("inbounds.saved"));
      onClose();
    },
    onError: (e) => {
      validate.reset();
      if (e instanceof ApiError && Object.keys(e.fields).length) {
        setErrors(e.fields);
        if (e.fields.config) setTab("config");
      } else toast.error(errorText(e));
    },
  });
  const configChanged = !!inbound && config !== inbound.config;
  const submit = (e: FormEvent) => {
    e.preventDefault();
    const body: Schemas["PatchInboundInputBody"] = {};
    if (port !== inbound?.port) body.port = port.trim();
    if (name.trim() !== inbound?.display_name) body.display_name = name.trim();
    // The config tab rewrites the whole template; the dest field is a shortcut into it.
    if (configChanged) body.config = config;
    else if (inbound?.dest !== undefined && (dest !== inbound.dest || sni)) {
      body.dest = dest.trim();
      if (sni) body.server_name = sni;
    }
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
      title={inbound?.sub_name ?? ""}
      meta={
        inbound ? (
          <span>
            {inbound.preset === "custom" ? t("inbounds.customOf", { type: inbound.type }) : inbound.title} · <span className="mono">{inbound.name}</span>
          </span>
        ) : undefined
      }
      footer={
        <>
          <Button variant="ghost" onClick={onClose}>
            {t("common.cancel")}
          </Button>
          <Button variant="primary" type="submit" form="edit-inbound" loading={save.isPending}>
            {t("common.save")}
          </Button>
        </>
      }
    >
      <form id="edit-inbound" onSubmit={submit} className="pt-5" noValidate>
        <Segmented
          label={t("inbounds.tabs")}
          value={tab}
          onChange={setTab}
          options={[
            { value: "main", label: t("inbounds.tabMain") },
            { value: "config", label: t("inbounds.tabConfig") },
          ]}
        />
        <div className="mt-4">
          {tab === "main" ? (
            <>
              <Field label={t("inbounds.subName")} htmlFor="ed-name" hint={t("inbounds.subNameHint")} error={errors.display_name}>
                <input id="ed-name" className="input" value={name} onChange={(e) => setName(e.target.value)} placeholder={defaultName} maxLength={48} aria-invalid={!!errors.display_name} autoComplete="off" />
              </Field>
              <Field label={t("inbounds.portLabel")} htmlFor="ed-port" error={errors.port}>
                <input id="ed-port" className="input max-w-[200px]" inputMode="numeric" value={port} onChange={(e) => setPort(e.target.value)} aria-invalid={!!errors.port} />
              </Field>
              {inbound?.dest !== undefined ? (
                <>
                  <Field
                    label={t("inbounds.dest")}
                    htmlFor="ed-dest"
                    error={errors.dest}
                    hint={configChanged ? t("inbounds.destLocked") : sni ? t("inbounds.targetSni", { sni }) : t("inbounds.destHint")}
                  >
                    <input
                      id="ed-dest"
                      className="input mono"
                      value={dest}
                      onChange={(e) => {
                        setDest(e.target.value);
                        setSni("");
                      }}
                      placeholder="www.microsoft.com:443"
                      aria-invalid={!!errors.dest}
                      spellCheck={false}
                      disabled={configChanged}
                    />
                  </Field>
                  {!configChanged ? (
                    <TargetPicker
                      dest={dest}
                      nodeId={inbound.node_id}
                      onPick={(d, s) => {
                        setDest(d);
                        setSni(s);
                      }}
                    />
                  ) : null}
                </>
              ) : null}
            </>
          ) : (
            <Field label={t("inbounds.configLabel")} hint={t("inbounds.configHint")} error={errors.config}>
              <Editor value={config} onChange={editConfig} invalid={!!errors.config} />
              <div className="mt-2 flex flex-wrap items-center gap-3">
                <Button size="sm" loading={validate.isPending} onClick={() => validate.mutate({ config, node_id: inbound?.node_id, port: port.trim() || undefined })}>
                  {t("inbounds.validate")}
                </Button>
                <Button size="sm" variant="ghost" disabled={!configChanged} onClick={() => inbound && editConfig(inbound.config)}>
                  <RotateCcw size={14} aria-hidden /> {t("inbounds.revert")}
                </Button>
                <ValidateResult v={validate} />
              </div>
            </Field>
          )}
        </div>
      </form>
    </Drawer>
  );
}
