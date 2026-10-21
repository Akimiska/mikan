// The bot as a subscriber sees it, beside the menu and the texts being edited.
import { TriangleAlert } from "lucide-react";
import { AnimatePresence, motion, useReducedMotion } from "motion/react";
import { useMemo } from "react";
import { useSettings } from "../../../api/hooks";
import { CardHead } from "../../../components/layout";
import { t, useLocale } from "../../../i18n";
import { TgHtml, useTgPreview } from "../telegram-text";
import { rise, slide, type Config, type MenuButton, type View } from "./shared";

/** Sample values of the texts' {variables}, for the previews. */
export function useSampleVars(): Record<string, string> {
  const settings = useSettings();
  const brand = settings.data?.brand || "VPN";
  const locale = useLocale();
  return useMemo(
    () => ({
      brand,
      name: t("telegram.sample.name"),
      state: t("telegram.sample.state"),
      term: t("telegram.sample.term"),
      traffic: t("telegram.sample.traffic"),
      devices: t("telegram.sample.devices"),
      until: t("telegram.sample.until"),
      days: t("telegram.sample.days"),
      used: t("telegram.sample.used"),
      left: t("telegram.sample.left"),
      limit: t("telegram.sample.limit"),
      reset: t("telegram.sample.reset"),
    }),
    // The sample texts are translated: they change with the language.
    // eslint-disable-next-line react-hooks/exhaustive-deps
    [brand, locale],
  );
}

/** The main menu as a subscriber sees it in Telegram, with sample data. */
export function Preview({ draft, v, bare }: { draft: Config; v: View; bare?: boolean }) {
  const settings = useSettings();
  const support = !!settings.data?.support_url;
  const sample = useSampleVars();
  const source = draft.texts.main || v.defaults.main;
  const html = useTgPreview(source, sample);
  // Until the panel answers, the text as typed with the samples in it.
  const text = source.replace(/\{(\w+)\}/g, (m, k: string) => sample[k] ?? m);
  const reduce = useReducedMotion();
  const rows: MenuButton[][] = [];
  for (const b of draft.buttons) {
    if (!b.on || (b.action === "support" && !support) || (b.action === "app" && !(draft.mini_app && v.mini_app_url))) continue;
    const last = rows[rows.length - 1];
    if (b.row && last && last.length < 3) last.push(b);
    else rows.push([b]);
  }
  // The bot adds «Промокоды» under the buttons itself while the Mini App is on.
  if (draft.promo_button && draft.mini_app && v.mini_app_url) {
    rows.push([{ id: "promo", action: "app", label: t("telegram.promoLabel"), on: true, row: false }]);
  }
  return (
    <section {...(bare ? {} : rise(1))} aria-label={t("telegram.preview")}>
      {bare ? (
        <p className="mb-3 text-[13px] text-[var(--ink-500)]">{t("telegram.previewSub")}</p>
      ) : (
        <CardHead title={t("telegram.preview")} sub={t("telegram.previewSub")} />
      )}
      <div className="tg-chat">
        <div className="tg-bubble">{html.data && !html.isError ? <TgHtml html={html.data.html} /> : text}</div>
        <motion.div className="tg-keyboard" layout={!reduce} transition={slide}>
          <AnimatePresence initial={false} mode="popLayout">
            {rows.map((r) => (
              <motion.div key={r[0]!.id} className="tg-row" layout={!reduce} transition={slide}>
                <AnimatePresence initial={false} mode="popLayout">
                  {r.map((b) => (
                    <motion.span
                      key={b.id}
                      className="tg-btn"
                      layout={!reduce}
                      initial={reduce ? false : { opacity: 0, scale: 0.85 }}
                      animate={{ opacity: 1, scale: 1 }}
                      exit={reduce ? { opacity: 0 } : { opacity: 0, scale: 0.85 }}
                      transition={slide}
                    >
                      {b.label}
                    </motion.span>
                  ))}
                </AnimatePresence>
              </motion.div>
            ))}
          </AnimatePresence>
        </motion.div>
      </div>
      {!v.mini_app_url && draft.buttons.some((b) => b.action === "app" && b.on) ? (
        <p className="mt-3 flex items-start gap-2 text-xs text-[var(--ink-500)]">
          <TriangleAlert size={14} className="mt-0.5 shrink-0 text-[var(--honey-600)]" aria-hidden /> {t("telegram.miniAppNoCert")}
        </p>
      ) : null}
    </section>
  );
}
