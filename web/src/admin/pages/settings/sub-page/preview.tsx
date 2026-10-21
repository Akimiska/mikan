// The page as a subscriber sees it, from the draft being edited.
import { Monitor, Moon, Smartphone, Sun } from "lucide-react";
import { useMemo, useState } from "react";
import { useNodes, useSettings } from "../../../../api/hooks";
import { Segmented } from "../../../../components/ui";
import { t, useLocale } from "../../../../i18n";
import type { Platform } from "../../../../sub/apps";
import type { ShopData } from "../../../../sub/shop";
import type { Info } from "../../../../sub/types";
import { firstPlatform } from "../../../../sub/view";
import { useSubDocs } from "../sub-docs";
import { PagePreview, type PreviewDevice } from "../sub-page-preview";
import { COLOR, imageURL, type Form, type View } from "./parts";

const SAMPLE_GB = 1024 ** 3;

export function Preview({ v, draft, platform: editing, bare }: { v: View; draft: Form; platform?: Platform; bare?: boolean }) {
  const settings = useSettings();
  const nodes = useNodes();
  const docs = useSubDocs();
  const locale = useLocale();
  const [device, setDevice] = useState<PreviewDevice>("phone");
  const [scheme, setScheme] = useState<"light" | "dark">(() => (window.matchMedia?.("(prefers-color-scheme: dark)").matches ? "dark" : "light"));
  const [tg, setTg] = useState(false);
  const look = draft.config.look;
  const s = settings.data;
  const brand = v.brand.trim() || "VPN";
  const info = useMemo<Info>(() => {
    const now = Date.now();
    const fill = (text: string) => text.replace(/\{(\w+)\}/g, (m, k: string) => ({ brand, name: t("settings.page.sample.name"), days: "23", devices: "1/3" })[k] ?? m);
    const names = (nodes.data ?? []).map((n) => n.name).filter(Boolean);
    return {
      name: t("settings.page.sample.name"),
      brand,
      support_url: s?.support_url || "https://t.me/",
      state: "active",
      used_up: 1.6 * SAMPLE_GB,
      used_down: 10.8 * SAMPLE_GB,
      limit: 50 * SAMPLE_GB,
      expires_at: new Date(now + 23 * 86_400_000).toISOString(),
      device_limit: 3,
      protocols: [],
      binding: !!s?.device_binding,
      devices: s?.device_binding
        ? [{ id: 1, name: "", os: "iOS", os_version: "18.4", model: "iPhone 15", app: "Happ", shared: false, created_at: new Date(now - 9 * 86_400_000).toISOString(), last_seen: new Date(now - 600_000).toISOString() }]
        : [],
      telegram: "https://t.me/",
      locations: names.length ? names : [t("settings.page.sample.location1"), t("settings.page.sample.location2")],
      announce: fill(s?.sub_announce || t("settings.page.sample.announce")),
      announce_url: s?.sub_announce_url || undefined,
    };
    // The sample texts are translated: they change with the language.
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [brand, s, nodes.data, locale]);
  const shop = useMemo<ShopData>(
    () => ({
      allow_new: true,
      providers: { stars: true },
      offers: [
        { id: 1, name: t("settings.page.sample.month"), description: t("settings.page.sample.monthText"), rub: 19900, stars: 130 },
        { id: 2, name: t("settings.page.sample.year"), description: t("settings.page.sample.yearText"), rub: 179000, stars: 1150 },
      ],
    }),
    // The same: the sample plans are in the page's language.
    // eslint-disable-next-line react-hooks/exhaustive-deps
    [locale],
  );
  const accent = look.accent === "brand" && COLOR.test(draft.brand_accent.trim()) ? draft.brand_accent.trim().toUpperCase() : undefined;
  const published = (docs.data ?? []).filter((d) => d.published).map((d) => ({ id: d.id, title: d.title, emoji: d.emoji, platform: d.platform, body: d.body }));
  const platform = editing ?? firstPlatform(draft.config, device === "phone" ? "ios" : "windows");
  const shownScheme = look.mode === "system" ? scheme : look.mode;
  return (
    <section className={bare ? "flex flex-col gap-3" : "card glass flex flex-col gap-3"} aria-label={t("settings.page.preview")}>
      <div className="flex flex-wrap items-center justify-between gap-2">
        {bare ? <span /> : <h2 className="card-title">{t("settings.page.preview")}</h2>}
        <div className="flex flex-wrap items-center gap-2">
          <div className="seg" role="group" aria-label={t("settings.page.previewDevice")}>
            <button type="button" aria-pressed={device === "phone"} onClick={() => setDevice("phone")} aria-label={t("settings.page.phone")} title={t("settings.page.phone")}>
              <Smartphone size={14} aria-hidden />
            </button>
            <button type="button" aria-pressed={device === "desktop"} onClick={() => setDevice("desktop")} aria-label={t("settings.page.desktop")} title={t("settings.page.desktop")}>
              <Monitor size={14} aria-hidden />
            </button>
          </div>
          {look.mode === "system" ? (
            <div className="seg" role="group" aria-label={t("settings.page.previewScheme")}>
              <button type="button" aria-pressed={scheme === "light"} onClick={() => setScheme("light")} aria-label={t("settings.page.modeLight")} title={t("settings.page.modeLight")}>
                <Sun size={14} aria-hidden />
              </button>
              <button type="button" aria-pressed={scheme === "dark"} onClick={() => setScheme("dark")} aria-label={t("settings.page.modeDark")} title={t("settings.page.modeDark")}>
                <Moon size={14} aria-hidden />
              </button>
            </div>
          ) : null}
          <Segmented
            label={t("settings.page.previewWhere")}
            value={tg ? "tg" : "web"}
            onChange={(w) => setTg(w === "tg")}
            options={[
              { value: "web", label: t("settings.page.previewWeb") },
              { value: "tg", label: "Mini App" },
            ]}
          />
        </div>
      </div>
      <PagePreview
        config={draft.config}
        brand={brand}
        accent={accent}
        logo={imageURL(v.logo)}
        background={look.background.kind === "image" ? imageURL(v.background) : undefined}
        docs={published}
        info={info}
        subURL="https://vpn.example.com/sub/k3J9fQ2mXa7d"
        device={device}
        scheme={shownScheme}
        tg={tg}
        platform={platform}
        shop={shop}
      />
      <p className="text-xs text-[var(--ink-500)]">{t("settings.page.previewNote")}</p>
    </section>
  );
}
