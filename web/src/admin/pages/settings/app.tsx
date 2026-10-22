import { Link } from "@tanstack/react-router";
import { CircleCheck, EllipsisVertical, MonitorDown, Share, SquarePlus, TriangleAlert } from "lucide-react";
import { useId, useState, type FormEvent, type ReactNode } from "react";
import { type Schemas } from "../../../api/client";
import { CardHead, FormActions } from "../../../components/layout";
import { useToast } from "../../../components/toast";
import { Button, Field } from "../../../components/ui";
import { t } from "../../../i18n";
import { useDraft } from "../../../lib/draft";
import { fieldErrors } from "../../../lib/fields";
import { promptInstall, useInstallState, type InstallState } from "../../../lib/pwa";
import { useSaveSettings } from "./shared";

/** The longest name the server keeps (server.AppNameMax). */
const NAME_MAX = 30;

/** The brand the installed panel is named after; "" for a panel without one (its brand reads "VPN"). */
function brandName(s: Schemas["SettingsView"]): string {
  const b = s.brand.trim();
  return b && b !== "VPN" ? b : "";
}

/**
 * The panel as an app on this device: a shortcut on the desktop and its own window. The button
 * shows the browser's install dialog where the browser has one (Chromium); elsewhere the card
 * says how to install by hand. Below, the name the shortcut carries.
 */
export function AppCard({ s }: { s: Schemas["SettingsView"] }) {
  const st = useInstallState();
  return (
    <section className="card glass reveal" style={{ "--i": 5 } as React.CSSProperties}>
      <CardHead title={t("settings.app.title")} sub={t("settings.app.sub")} />
      <InstallBlock st={st} />
      <AppNameChoice s={s} />
    </section>
  );
}

function Note({ icon, tone, children }: { icon: ReactNode; tone?: "ok" | "warn"; children: ReactNode }) {
  return (
    <p className={tone === "warn" ? "banner warn" : "flex items-start gap-2 text-sm text-[var(--ink-700)]"} role={tone === "warn" ? "alert" : undefined}>
      <span className={tone === "ok" ? "mt-0.5 shrink-0 text-[var(--leaf-500)]" : "mt-0.5 shrink-0"}>{icon}</span>
      <span>{children}</span>
    </p>
  );
}

function Steps({ items }: { items: { icon: ReactNode; text: string }[] }) {
  return (
    <ol className="flex flex-col gap-2" aria-label={t("settings.app.steps")}>
      {items.map((s, i) => (
        <li key={i} className="flex items-center gap-3 text-sm text-[var(--ink-700)]">
          <span className="grid size-8 shrink-0 place-items-center rounded-full bg-[var(--surface-soft)] text-xs font-semibold text-[var(--ink-900)]" aria-hidden>
            {i + 1}
          </span>
          <span className="shrink-0 text-[var(--ink-500)]" aria-hidden>
            {s.icon}
          </span>
          <span>{s.text}</span>
        </li>
      ))}
    </ol>
  );
}

/** What this browser can do: the install button, how to install by hand, or that it is done. */
function InstallBlock({ st }: { st: InstallState }) {
  const toast = useToast();
  const [busy, setBusy] = useState(false);
  if (st.standalone) {
    return <Note icon={<CircleCheck size={16} aria-hidden />} tone="ok">{t("settings.app.standalone")}</Note>;
  }
  if (st.installed) {
    return <Note icon={<CircleCheck size={16} aria-hidden />} tone="ok">{t("settings.app.installed")}</Note>;
  }
  const chromium = st.platform === "chromium" || st.platform === "chromium-android";
  // No browser installs a site that is not HTTPS with a certificate it trusts.
  if (!st.secure || (st.workerFailed && chromium)) {
    return (
      <div className="flex flex-col gap-3">
        <Note icon={<TriangleAlert size={16} aria-hidden />} tone="warn">
          {t("settings.app.insecure")}
        </Note>
        <div>
          <Link to="/settings" search={{ tab: "security" }} className="btn btn-glass btn-sm">
            {t("settings.app.insecureLink")}
          </Link>
        </div>
      </div>
    );
  }
  const install = async () => {
    setBusy(true);
    try {
      const outcome = await promptInstall();
      if (outcome === "accepted") toast.ok(t("settings.app.done"));
    } finally {
      setBusy(false);
    }
  };
  if (chromium && st.canPrompt) {
    return (
      <div className="flex flex-wrap items-center gap-3">
        <Button variant="primary" loading={busy} onClick={() => void install()}>
          <MonitorDown size={16} aria-hidden />
          {t("settings.app.install")}
        </Button>
        <span className="text-xs text-[var(--ink-500)]">{t("settings.app.installHint")}</span>
      </div>
    );
  }
  switch (st.platform) {
    case "chromium":
      return <Note icon={<EllipsisVertical size={16} aria-hidden />}>{t("settings.app.chromiumMenu")}</Note>;
    case "chromium-android":
      return <Note icon={<EllipsisVertical size={16} aria-hidden />}>{t("settings.app.androidMenu")}</Note>;
    case "ios":
      return (
        <Steps
          items={[
            { icon: <Share size={16} />, text: t("settings.app.iosShare") },
            { icon: <SquarePlus size={16} />, text: t("settings.app.iosAdd") },
            { icon: <CircleCheck size={16} />, text: t("settings.app.iosConfirm") },
          ]}
        />
      );
    case "mac-safari":
      return <Note icon={<SquarePlus size={16} aria-hidden />}>{t("settings.app.macSafari")}</Note>;
    case "firefox-android":
      return <Note icon={<EllipsisVertical size={16} aria-hidden />}>{t("settings.app.firefoxAndroid")}</Note>;
    case "firefox":
      return <Note icon={<TriangleAlert size={16} aria-hidden />}>{t("settings.app.firefox")}</Note>;
    default:
      return <Note icon={<TriangleAlert size={16} aria-hidden />}>{t("settings.app.other")}</Note>;
  }
}

type NameMode = "brand" | "mikan" | "own";

/** The shortcut's name: the service's, "mikan", or one of the admin's own. */
function AppNameChoice({ s }: { s: Schemas["SettingsView"] }) {
  const save = useSaveSettings();
  const inputId = useId();
  const brand = brandName(s);
  const stored = s.panel_app_name.trim();
  const saved: NameMode = stored === "" ? (brand ? "brand" : "mikan") : stored === "mikan" ? "mikan" : "own";
  const [ownOpen, setOwnOpen] = useState(false);
  const mode: NameMode = ownOpen ? "own" : saved;
  const { draft, setDraft, dirty, reset } = useDraft(saved === "own" ? stored : "");
  const errors = fieldErrors(save.error);
  const options: { id: NameMode; label: string; sub?: string }[] = [
    ...(brand ? [{ id: "brand" as const, label: brand, sub: t("settings.app.nameBrand") }] : []),
    { id: "mikan", label: "mikan" },
    { id: "own", label: t("settings.app.nameOwn") },
  ];
  const pick = (m: NameMode) => {
    if (m === "own") {
      setOwnOpen(true);
      requestAnimationFrame(() => document.getElementById(inputId)?.focus());
      return;
    }
    setOwnOpen(false);
    reset();
    const value = m === "brand" ? "" : "mikan";
    if (value !== s.panel_app_name) save.mutate({ panel_app_name: value });
  };
  const submit = (e: FormEvent) => {
    e.preventDefault();
    const name = draft.trim();
    if (!name) return;
    save.mutate({ panel_app_name: name }, { onSuccess: () => setOwnOpen(false) });
  };
  const empty = mode === "own" && !draft.trim();
  return (
    <div className="mt-5 border-t border-[var(--hairline)] pt-4">
      <div className="mb-2 text-sm font-semibold text-[var(--ink-900)]" id={`${inputId}-label`}>
        {t("settings.app.name")}
      </div>
      <div className={options.length > 2 ? "grid gap-2 sm:grid-cols-3" : "grid gap-2 sm:grid-cols-2"} role="radiogroup" aria-labelledby={`${inputId}-label`} aria-busy={save.isPending}>
        {options.map((o) => (
          <button key={o.id} type="button" role="radio" aria-checked={mode === o.id} className="opt" disabled={save.isPending} onClick={() => pick(o.id)}>
            <span className="truncate font-semibold">{o.label}</span>
            {o.sub ? <span className="text-xs text-[var(--ink-500)]">{o.sub}</span> : null}
          </button>
        ))}
      </div>
      {mode === "own" ? (
        <form onSubmit={submit} noValidate className="mt-3">
          <Field label={t("settings.app.nameOwn")} htmlFor={inputId} error={errors.panel_app_name ?? (empty && dirty ? t("settings.app.nameEmpty") : undefined)}>
            <input
              id={inputId}
              className="input"
              value={draft}
              maxLength={NAME_MAX}
              placeholder={t("settings.app.namePlaceholder")}
              onChange={(e) => setDraft(e.target.value)}
              aria-invalid={!!errors.panel_app_name || (empty && dirty)}
            />
          </Field>
          <FormActions
            dirty={dirty || saved !== "own"}
            saving={save.isPending}
            disabled={empty || (!dirty && saved === "own")}
            onReset={() => {
              reset();
              setOwnOpen(false);
            }}
          />
        </form>
      ) : null}
      <p className="mt-3 text-xs text-[var(--ink-500)]">{t("settings.app.nameHint")}</p>
    </div>
  );
}
