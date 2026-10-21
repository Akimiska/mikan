// The brand on the page: its mark and the line under the name.
import { Link } from "@tanstack/react-router";
import { SwitchRow } from "../../../../components/switch";
import { Field, Segmented } from "../../../../components/ui";
import { t } from "../../../../i18n";
import { ImageField } from "./look";
import { Card, imageURL, type Asset, type Config, type SectionProps } from "./parts";

export function BrandSection({ draft, set, errors, logo, brandName }: SectionProps & { logo?: Asset; brandName: string }) {
  const b = draft.config.brand;
  const og = draft.config.og;
  const setBrand = (patch: Partial<Config["brand"]>) => set((c) => ({ ...c, brand: { ...c.brand, ...patch } }));
  const setOG = (patch: Partial<Config["og"]>) => set((c) => ({ ...c, og: { ...c.og, ...patch } }));
  const name = brandName.trim() || "VPN";
  return (
    <>
      <Card title={t("settings.page.brand")} sub={t("settings.page.brandSub")}>
        <div className="grid gap-x-3 sm:grid-cols-2">
          <Field label={t("settings.brand")} hint={t("settings.page.brandNameHint")}>
            <div className="flex min-h-11 items-center justify-between gap-2 rounded-[var(--r-md)] border border-[var(--hairline)] bg-[var(--surface-soft)] py-1 pr-1 pl-3">
              <span className="truncate font-medium">{brandName.trim() || "VPN"}</span>
              <Link to="/settings" search={{ tab: "general" }} className="btn btn-ghost btn-sm">
                {t("settings.page.brandNameEdit")}
              </Link>
            </div>
          </Field>
          <Field label={t("settings.page.subtitle")} htmlFor="sp-subtitle" hint={t("settings.page.subtitleHint")} error={errors["config.brand.subtitle"]}>
            <input id="sp-subtitle" className="input" value={b.subtitle} maxLength={80} onChange={(e) => setBrand({ subtitle: e.target.value })} aria-invalid={!!errors["config.brand.subtitle"]} />
          </Field>
        </div>
        <Field label={t("settings.page.logo")} hint={t("settings.page.logoHint")} error={errors["config.brand.logo"]}>
          <Segmented
            label={t("settings.page.logo")}
            value={b.logo}
            onChange={(logo) => setBrand({ logo })}
            options={[
              { value: "letter", label: t("settings.page.logoLetter") },
              { value: "image", label: t("settings.page.logoImage") },
              { value: "emoji", label: t("settings.page.logoEmoji") },
              { value: "none", label: t("settings.page.logoNone") },
            ]}
          />
        </Field>
        {b.logo === "image" ? <ImageField name="logo" asset={logo} label={t("settings.page.logoFile")} hint={t("settings.page.logoFileHint")} /> : null}
        {b.logo === "emoji" ? (
          <Field label={t("settings.page.emoji")} htmlFor="sp-emoji" hint={t("settings.page.emojiHint")} error={errors["config.brand.emoji"]}>
            <input id="sp-emoji" className="input max-w-[120px] text-center text-lg" value={b.emoji} maxLength={16} autoComplete="off" onChange={(e) => setBrand({ emoji: e.target.value.trim() })} aria-invalid={!!errors["config.brand.emoji"]} />
          </Field>
        ) : null}
      </Card>

      <Card title={t("settings.page.og")} sub={t("settings.page.ogSub")}>
        <Field label={t("settings.page.ogTitle")} htmlFor="sp-og-title" error={errors["config.og.title"]}>
          <input id="sp-og-title" className="input" value={og.title} maxLength={80} placeholder={name} onChange={(e) => setOG({ title: e.target.value })} aria-invalid={!!errors["config.og.title"]} />
        </Field>
        <Field label={t("settings.page.ogDescription")} htmlFor="sp-og-desc" error={errors["config.og.description"]}>
          <textarea id="sp-og-desc" className="input" rows={2} value={og.description} maxLength={200} onChange={(e) => setOG({ description: e.target.value.replace(/\n/g, " ") })} aria-invalid={!!errors["config.og.description"]} />
        </Field>
        <SwitchRow label={t("settings.page.ogImage")} sub={logo ? t("settings.page.ogImageSub") : t("settings.page.ogImageNoLogo")} checked={og.image} disabled={!logo && !og.image} onChange={(image) => setOG({ image })} />
        {errors["config.og.image"] ? (
          <p className="mb-3 text-xs text-[var(--berry-600)]" role="alert">
            {errors["config.og.image"]}
          </p>
        ) : null}
        <div className="text-xs text-[var(--ink-500)]">{t("settings.page.ogLook")}</div>
        {og.title || og.description || (og.image && logo) ? (
          <div className="panel-soft mt-2 flex items-start gap-3 p-3" aria-hidden>
            <span className="w-0.5 self-stretch rounded-full bg-[var(--mikan-500)]" />
            <div className="min-w-0 flex-1">
              <div className="text-xs font-semibold text-[var(--mikan-700)]">{name}</div>
              <div className="truncate text-[13px] font-semibold">{og.title || name}</div>
              {og.description ? <div className="line-clamp-2 text-xs text-[var(--ink-600)]">{og.description}</div> : null}
            </div>
            {og.image && logo ? <img src={imageURL(logo)} alt="" className="h-12 w-12 shrink-0 rounded-[10px] object-cover" /> : null}
          </div>
        ) : (
          <p className="panel-soft mt-2 p-3 text-xs text-[var(--ink-500)]">{t("settings.page.ogNone")}</p>
        )}
      </Card>
    </>
  );
}
