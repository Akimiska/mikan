import type { FormEvent } from "react";
import type { Schemas } from "../../../api/client";
import { FormActions } from "../../../components/layout";
import { Field, Segmented } from "../../../components/ui";
import { SwitchRow } from "../../../components/switch";
import { t } from "../../../i18n";
import { useDraft } from "../../../lib/draft";
import { fieldErrors } from "../../../lib/fields";
import { useSaveSettings } from "./shared";

type Crypt = "off" | "api" | "local";
type RoutingMode = "off" | "auto" | "link";

/** What Happ gets beyond the profile: a routing profile, hidden server settings and a
 * crypt link that keeps the subscription address out of sight. */
export function HappCard({ s }: { s: Schemas["SettingsView"] }) {
  const save = useSaveSettings();
  const { draft: form, setDraft: setForm, dirty, reset } = useDraft({
    happ_routing: s.happ_routing === "auto" ? "" : s.happ_routing,
    // "auto": the panel makes the routing profile of Settings → Routing.
    mode: (s.happ_routing === "auto" ? "auto" : s.happ_routing ? "link" : "off") as RoutingMode,
    happ_provider_id: s.happ_provider_id,
    happ_crypt: (s.happ_crypt || "off") as Crypt,
  });
  const routingMode = form.mode;
  const errors = fieldErrors(save.error);
  const provider = form.happ_provider_id.trim();
  const submit = (e: FormEvent) => {
    e.preventDefault();
    const routing = form.mode === "auto" ? "auto" : form.mode === "off" ? "" : form.happ_routing.trim();
    save.mutate({ happ_routing: routing, happ_provider_id: provider, happ_crypt: form.happ_crypt });
  };
  // The switch saves at once, like the other switches; it needs the saved provider id.
  const canHide = !!s.happ_provider_id;
  return (
    <section className="card glass reveal" style={{ "--i": 3 } as React.CSSProperties}>
      <form onSubmit={submit} noValidate>
        <div className="card-head">
          <div>
            <h2 className="card-title">Happ</h2>
            <div className="card-sub">{t("settings.happSub")}</div>
          </div>
        </div>

        <Field label={t("settings.happCrypt")} hint={t(`settings.happCryptHint.${form.happ_crypt}`)} error={errors.happ_crypt}>
          <Segmented
            label={t("settings.happCrypt")}
            value={form.happ_crypt}
            onChange={(v) => setForm((f) => ({ ...f, happ_crypt: v }))}
            options={[
              { value: "off", label: t("settings.happCryptOff") },
              { value: "api", label: t("settings.happCryptApi") },
              { value: "local", label: t("settings.happCryptLocal") },
            ]}
          />
        </Field>

        <Field label={t("settings.happRouting")} hint={t(`settings.happRoutingModeHint.${routingMode}`)} error={routingMode === "link" ? undefined : errors.happ_routing}>
          <Segmented
            label={t("settings.happRouting")}
            value={routingMode}
            onChange={(v) => setForm((f) => ({ ...f, mode: v }))}
            options={[
              { value: "off", label: t("settings.happRoutingOff") },
              { value: "auto", label: t("settings.happRoutingAuto") },
              { value: "link", label: t("settings.happRoutingLink") },
            ]}
          />
        </Field>

        {routingMode === "link" ? (
        <Field label={t("settings.happRoutingLinkField")} htmlFor="s-happ-routing" hint={t("settings.happRoutingHint")} error={errors.happ_routing}>
          <textarea
            id="s-happ-routing"
            className="input mono min-h-[88px] text-xs"
            value={form.happ_routing}
            onChange={(e) => setForm((f) => ({ ...f, happ_routing: e.target.value }))}
            placeholder="happ://routing/onadd/eyJOYW1lIjoi…"
            spellCheck={false}
            autoComplete="off"
            aria-invalid={!!errors.happ_routing}
          />
        </Field>
        ) : null}

        <Field label={t("settings.happProvider")} htmlFor="s-happ-provider" hint={t("settings.happProviderHint")} error={errors.happ_provider_id}>
          <input
            id="s-happ-provider"
            className="input mono"
            value={form.happ_provider_id}
            onChange={(e) => setForm((f) => ({ ...f, happ_provider_id: e.target.value }))}
            maxLength={64}
            autoComplete="off"
            aria-invalid={!!errors.happ_provider_id}
          />
        </Field>

        <SwitchRow
          className="border-t border-[var(--hairline)]"
          label={t("settings.happHide")}
          sub={canHide ? t("settings.happHideSub") : t("settings.happHideNeedsProvider")}
          warn={!canHide}
          checked={canHide && s.happ_hide_settings}
          disabled={!canHide || save.isPending}
          onChange={(v) => save.mutate({ happ_hide_settings: v })}
        />
        {errors.happ_hide_settings ? (
          <p className="text-xs text-[var(--berry-600)]" role="alert">
            {errors.happ_hide_settings}
          </p>
        ) : null}

        <FormActions dirty={dirty} saving={save.isPending && save.variables?.happ_hide_settings === undefined} onReset={reset} />
      </form>
    </section>
  );
}
