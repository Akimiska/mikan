import { useNavigate, useSearch } from "@tanstack/react-router";
import { Globe, Link2, ListFilter, ShieldCheck } from "lucide-react";
import { useRef, type KeyboardEvent, type ReactNode } from "react";
import { errorText } from "../../../api/client";
import { useSettings } from "../../../api/hooks";
import { LangSwitch } from "../../../components/lang";
import { ErrorState, PageHeader, Skeleton } from "../../../components/ui";
import { t } from "../../../i18n";
import { AutoCard, LanguageCard, SalesCard, ServerCard, UpdatesCard } from "./general";
import { ClashRulesCard } from "./rules";
import { AccessCard, ApiCard, CertificateCard, PasswordCard, SessionsCard, TwoFactorCard } from "./security";
import { DevicesCard, SubPortCard, SubscriptionCard } from "./subscription";

export const SETTINGS_TABS = ["general", "subscription", "rules", "security"] as const;
export type SettingsTab = (typeof SETTINGS_TABS)[number];
export type SettingsSearch = { tab: SettingsTab };

const ICONS: Record<SettingsTab, typeof Globe> = { general: Globe, subscription: Link2, rules: ListFilter, security: ShieldCheck };

/** Settings in four sections, one at a time; the section is in the URL, so a link opens it. */
export function SettingsPage() {
  const settings = useSettings();
  const { tab } = useSearch({ from: "/_app/settings" });
  const navigate = useNavigate({ from: "/settings" });
  const refs = useRef<Partial<Record<SettingsTab, HTMLButtonElement | null>>>({});
  const open = (next: SettingsTab, focus = false) => {
    void navigate({ search: { tab: next }, replace: true });
    if (focus) refs.current[next]?.focus();
  };
  // Arrows move between the tabs, as in any tab list.
  const onKey = (e: KeyboardEvent) => {
    const i = SETTINGS_TABS.indexOf(tab);
    const step = e.key === "ArrowRight" ? 1 : e.key === "ArrowLeft" ? -1 : 0;
    if (e.key === "Home") open(SETTINGS_TABS[0], true);
    else if (e.key === "End") open(SETTINGS_TABS[SETTINGS_TABS.length - 1]!, true);
    else if (step) open(SETTINGS_TABS[(i + step + SETTINGS_TABS.length) % SETTINGS_TABS.length]!, true);
    else return;
    e.preventDefault();
  };
  return (
    <>
      <PageHeader title={t("nav.settings")} sub={t("settings.subtitle")} actions={<LangSwitch />} />
      <div className="tabs mb-4" role="tablist" aria-label={t("settings.sections")} onKeyDown={onKey}>
        {SETTINGS_TABS.map((id) => {
          const Icon = ICONS[id];
          return (
            <button
              key={id}
              ref={(el) => {
                refs.current[id] = el;
              }}
              type="button"
              role="tab"
              id={`settings-tab-${id}`}
              aria-selected={tab === id}
              aria-controls="settings-panel"
              tabIndex={tab === id ? 0 : -1}
              onClick={() => open(id)}
            >
              <Icon size={16} aria-hidden /> {t(`settings.tabs.${id}`)}
            </button>
          );
        })}
      </div>
      <div id="settings-panel" role="tabpanel" aria-labelledby={`settings-tab-${tab}`}>
        {settings.isPending ? (
          <Skeleton style={{ height: 320, borderRadius: 20 }} />
        ) : settings.isError ? (
          <section className="card glass">
            <ErrorState text={errorText(settings.error)} onRetry={() => void settings.refetch()} />
          </section>
        ) : tab === "general" ? (
          <Columns left={[<ServerCard key="s" s={settings.data} />, <LanguageCard key="l" s={settings.data} />, <AutoCard key="a" s={settings.data} />]} right={[<UpdatesCard key="u" />, <SalesCard key="p" />]} />
        ) : tab === "subscription" ? (
          <Columns left={[<SubscriptionCard key="s" s={settings.data} />, <DevicesCard key="d" s={settings.data} />]} right={[<SubPortCard key="p" s={settings.data} />]} />
        ) : tab === "rules" ? (
          <div className="max-w-4xl">
            <ClashRulesCard s={settings.data} />
          </div>
        ) : (
          <Columns
            left={[<AccessCard key="a" s={settings.data} />, <CertificateCard key="c" s={settings.data} />, <ApiCard key="k" />]}
            right={[<PasswordCard key="p" />, <TwoFactorCard key="t" />, <SessionsCard key="s" />]}
          />
        )}
      </div>
    </>
  );
}

function Columns({ left, right }: { left: ReactNode[]; right: ReactNode[] }) {
  return (
    <div className="grid items-start gap-4 xl:grid-cols-2">
      <div className="flex min-w-0 flex-col gap-4">{left}</div>
      <div className="flex min-w-0 flex-col gap-4">{right}</div>
    </div>
  );
}
