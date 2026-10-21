import { useBlocker, useNavigate, useSearch } from "@tanstack/react-router";
import { Bell, Bot, LayoutList, Link2, Megaphone, Network, PlugZap } from "lucide-react";
import { useState } from "react";
import { ApiError, errorText } from "../../api/client";
import { useTelegram } from "../../api/hooks";
import { SaveBar, SectionNav, WithPreview } from "../../components/layout";
import { Confirm } from "../../components/overlay";
import { QueryBoundary } from "../../components/query";
import { Switch } from "../../components/switch";
import { useToast } from "../../components/toast";
import { Button, PageHeader, Pill, Skeleton } from "../../components/ui";
import { t, tMaybe, type Key } from "../../i18n";
import { useDraft } from "../../lib/draft";
import { TELEGRAM_TABS } from "../search";
import { BackToAddons } from "./addon-row";
import { botStatus, type BotPhase } from "./market-common";
import { BroadcastCard } from "./telegram/broadcast";
import { ConnectCard, RouteCard } from "./telegram/connect";
import { BackupCard, InfrastructureCard } from "./telegram/infra";
import { ChannelCard, channelErrorText, MenuCard, OptionsCard, TextsCard } from "./telegram/menu";
import { Preview } from "./telegram/preview";
import { usePatchTelegram, type View } from "./telegram/shared";

const TAB_ICONS = { connect: PlugZap, menu: LayoutList, notify: Bell, infra: Network, broadcast: Megaphone } as const;

/**
 * The bot in four sections: connecting it, its menu and texts, notifications and options,
 * broadcasts. Menu, texts and options are one draft saved together from the bar below,
 * whichever section they were changed in.
 */
export function TelegramPage() {
  const tg = useTelegram();
  return (
    <>
      <PageHeader title={t("nav.telegram")} sub={t("telegram.subtitle")} actions={<BackToAddons />} />
      <QueryBoundary
        query={tg}
        pending={
          <div className="flex flex-col gap-4" role="status" aria-busy aria-label={t("common.loading")}>
            <Skeleton style={{ height: 72, borderRadius: 20 }} />
            <Skeleton style={{ height: 420, borderRadius: 20 }} />
          </div>
        }
        wrap={(state) => <section className="card glass">{state}</section>}
      >
        {(v) => <TelegramBody v={v} />}
      </QueryBoundary>
    </>
  );
}

function TelegramBody({ v }: { v: View }) {
  const { tab } = useSearch({ from: "/_app/addons/telegram" });
  const navigate = useNavigate({ from: "/addons/telegram" });
  const go = (next: (typeof TELEGRAM_TABS)[number]) => void navigate({ search: { tab: next }, replace: true });
  const patch = usePatchTelegram();
  const toast = useToast();
  // The drafts follow the server's copy while untouched and keep the edits when the copy
  // changes under them (a poll, the bot saved from another session). Menu, texts and options
  // are one draft, the admin's alerts another; the bar below saves whatever was changed.
  const { draft, setDraft, dirty, reset } = useDraft(v.config);
  const infrastructure = useDraft(v.infrastructure);
  const [infraError, setInfraError] = useState("");
  const changed = dirty || infrastructure.dirty;
  const save = () =>
    patch.mutate(
      { ...(dirty ? { config: draft } : {}), ...(infrastructure.dirty ? { infrastructure: infrastructure.draft } : {}) },
      {
        onSuccess: (r) => {
          setDraft(r.config);
          infrastructure.setDraft(r.infrastructure);
          setInfraError("");
          toast.ok(t("telegram.saved"));
        },
        onError: (e) => {
          if (infrastructure.dirty && e instanceof ApiError && Object.keys(e.fields).length) {
            setInfraError(Object.values(e.fields)[0] ?? errorText(e));
            go("infra");
          } else toast.error(errorText(e));
        },
      },
    );
  const discard = () => {
    reset();
    infrastructure.reset();
    setInfraError("");
  };
  // Leaving the page drops the drafts: ask first. Switching the section stays on the page.
  const leave = useBlocker({ shouldBlockFn: ({ current, next }) => changed && current.pathname !== next.pathname, enableBeforeUnload: () => changed, withResolver: true });
  const preview = (bare: boolean) => <Preview draft={draft} v={v} bare={bare} />;

  return (
    <>
      <BotSummary v={v} onConnect={() => go("connect")} />
      <SectionNav
        label={t("telegram.sections")}
        sections={TELEGRAM_TABS.map((id) => ({ id, label: t(`telegram.tabs.${id}`), icon: TAB_ICONS[id] }))}
        value={tab}
        onChange={go}
        narrow={tab === "infra" || tab === "broadcast"}
      >
        {tab === "connect" ? (
          <WithPreview title={t("telegram.preview")} preview={preview}>
            <ConnectCard v={v} />
            <RouteCard v={v} />
          </WithPreview>
        ) : tab === "menu" ? (
          <WithPreview title={t("telegram.preview")} preview={preview}>
            <MenuCard draft={draft} setDraft={setDraft} />
            <ChannelCard draft={draft} setDraft={setDraft} v={v} />
            <TextsCard draft={draft} setDraft={setDraft} defaults={v.defaults} />
          </WithPreview>
        ) : tab === "notify" ? (
          <WithPreview title={t("telegram.preview")} preview={preview}>
            <OptionsCard draft={draft} setDraft={setDraft} v={v} />
          </WithPreview>
        ) : tab === "infra" ? (
          <>
            <InfrastructureCard v={v} draft={infrastructure.draft} setDraft={infrastructure.setDraft} error={infraError} />
            <BackupCard />
          </>
        ) : (
          <BroadcastCard v={v} />
        )}
        <SaveBar dirty={changed} saving={patch.isPending} onSave={save} onReset={discard} />
      </SectionNav>
      <Confirm
        open={leave.status === "blocked"}
        onOpenChange={(open) => !open && leave.reset?.()}
        title={t("telegram.leaveTitle")}
        text={t("telegram.leaveText")}
        confirm={t("telegram.leaveConfirm")}
        danger
        onConfirm={() => leave.proceed?.()}
      />
    </>
  );
}

/** The bot at a glance above every section: who it is, whether it runs, and its switch. */
function BotSummary({ v, onConnect }: { v: View; onConnect: () => void }) {
  const patch = usePatchTelegram();
  const toast = useToast();
  const st = botStatus(v, STATUS_WORDS);
  return (
    <section className="card glass reveal" aria-label={t("telegram.summary")}>
      <div className="flex flex-wrap items-center gap-3">
        <span className="grid h-10 w-10 shrink-0 place-items-center rounded-xl bg-[var(--hover)] text-[var(--ink-700)]" aria-hidden>
          <Bot size={20} />
        </span>
        <div className="min-w-0 flex-1">
          {v.token_set && v.bot ? (
            <a className="font-semibold text-[var(--ink-900)] hover:underline" href={`https://t.me/${encodeURIComponent(v.bot.username)}`} target="_blank" rel="noreferrer noopener">
              @{v.bot.username}
            </a>
          ) : (
            <div className="font-semibold">{t("telegram.notConnected")}</div>
          )}
          <div className="truncate text-xs text-[var(--ink-500)]">
            {v.token_set ? (
              <>
                {t("telegram.stats", { linked: v.linked, accounts: v.accounts })}
                {" · "}
                {v.mini_app_url ? t("telegram.miniAppOn") : t("telegram.miniAppNoCert")}
              </>
            ) : (
              t("telegram.connectSub")
            )}
          </div>
        </div>
        {v.token_set ? (
          <div className="flex items-center gap-3">
            <Pill tone={st.tone}>{st.text}</Pill>
            <Switch checked={v.enabled} label={t("telegram.enabled")} disabled={patch.isPending} onChange={(on) => patch.mutate({ enabled: on }, { onError: (e) => toast.error(errorText(e)) })} />
          </div>
        ) : (
          <Button variant="primary" onClick={onConnect}>
            <Link2 size={16} aria-hidden /> {t("telegram.connectButton")}
          </Button>
        )}
      </div>
      {v.error && v.enabled ? (
        <p className="mt-3 text-[13px] text-[var(--berry-600)]" role="alert">
          {tMaybe(`telegram.err.${v.error}`) ?? v.error}
        </p>
      ) : null}
      {v.channel_error && v.config.channel.required ? (
        <p className="mt-3 text-[13px] text-[var(--berry-600)]" role="alert">
          {channelErrorText(v.channel_error)}
        </p>
      ) : null}
    </section>
  );
}

const STATUS_WORDS: Record<BotPhase, Key> = { off: "telegram.off", ok: "telegram.running", reconnecting: "telegram.reconnecting", starting: "telegram.starting", down: "telegram.stopped" };
