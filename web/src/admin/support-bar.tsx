// The strip across the top of every page while support is connected: for the admin, that
// someone can work in the panel until a time (with the way to end it); for the operator,
// the mode they are in. One strip, two readings, so neither side can forget the access is live.
import { useMutation, useQueryClient } from "@tanstack/react-query";
import { Link, useNavigate } from "@tanstack/react-router";
import { EyeOff, Headset, LifeBuoy, LogOut, Pencil, TerminalSquare } from "lucide-react";
import { useEffect, useState } from "react";
import { api, errorText, unwrap } from "../api/client";
import { isLive, supportKey, useSupportGrants, useSupportMode, type SupportGrant } from "../api/support";
import { Confirm } from "../components/overlay";
import { useToast } from "../components/toast";
import { Button } from "../components/ui";
import { getLocale, t, useLocale } from "../i18n";
import { ago, time, timeLeft } from "../lib/format";
import { useNow } from "../lib/now";

const LEVEL_ICON = { view: Headset, change: Pencil, terminal: TerminalSquare } as const;

/** The strip is this tall; the sidebar and the sticky bars below it make room (--bar-h in app.css). */
function useBarHeight() {
  useEffect(() => {
    document.documentElement.style.setProperty("--bar-h", "44px");
    return () => {
      document.documentElement.style.removeProperty("--bar-h");
    };
  }, []);
}

export function SupportBar() {
  useLocale();
  const operator = useSupportMode(true);
  return operator ? <OperatorBar level={operator.level} until={operator.expires_at} showPeople={operator.show_people} /> : <AdminBanner />;
}

/** The admin's side: the live grant, how long it has, whether anyone came in, and "Revoke". */
function AdminBanner() {
  const grants = useSupportGrants();
  const g = grants.data?.grant;
  // A grant that ran out between two refreshes is not shown as live.
  return isLive(g) ? <LiveGrant g={g} /> : null;
}

function LiveGrant({ g }: { g: SupportGrant }) {
  useBarHeight();
  const now = useNow(15_000);
  const qc = useQueryClient();
  const toast = useToast();
  const revoke = useMutation({
    mutationFn: () => unwrap(api.DELETE("/api/v1/support/grants/{id}", { params: { path: { id: g.id } } })),
    onSuccess: () => {
      void qc.invalidateQueries({ queryKey: supportKey.grants });
      toast.ok(t("support.revoked"));
    },
    onError: (e) => toast.error(errorText(e)),
  });
  const Icon = LEVEL_ICON[g.level];
  const seen = g.last_used_at ?? g.entered_at;
  return (
    <div className="support-bar warn" role="region" aria-label={t("support.bannerLabel")}>
      <Icon size={16} className="shrink-0" aria-hidden />
      <p className="min-w-0 flex-1 truncate">
        <b>{t("support.bannerTitle", { time: time(g.expires_at) })}</b>
        <span className="support-bar-more"> · {t(`support.level.${g.level}`)} · {t("support.left", { left: timeLeft(g.expires_at, now) })}</span>
        <span className="support-bar-more"> · {seen ? t("support.operatorSeen", { ago: ago(seen, now) }) : t("support.operatorNot")}</span>
      </p>
      <Link to="/settings" search={{ tab: "support" }} className="support-bar-link">
        {t("support.details")}
      </Link>
      <Button size="sm" variant="danger-solid" loading={revoke.isPending} onClick={() => revoke.mutate()}>
        {t("support.revoke")}
      </Button>
    </div>
  );
}

/** The operator's side: what they may do here, until when, and the way out when the work is done. */
function OperatorBar({ level, until, showPeople }: { level: "view" | "change" | "terminal"; until: string; showPeople: boolean }) {
  useBarHeight();
  const now = useNow(15_000);
  const qc = useQueryClient();
  const toast = useToast();
  const navigate = useNavigate();
  const [confirm, setConfirm] = useState(false);
  const leave = useMutation({
    mutationFn: () => unwrap(api.POST("/api/v1/support/leave")),
    onSuccess: () => {
      setConfirm(false);
      qc.clear();
      void navigate({ to: "/support", search: { ended: true } });
    },
    onError: (e) => {
      setConfirm(false);
      toast.error(errorText(e));
    },
  });
  const Icon = LEVEL_ICON[level];
  return (
    <div className="support-bar info" role="region" aria-label={t("support.modeLabel")}>
      <LifeBuoy size={16} className="shrink-0" aria-hidden />
      <p className="min-w-0 flex-1 truncate">
        <b>{t("support.mode", { level: t(`support.level.${level}`).toLocaleLowerCase(getLocale()) })}</b>
        <span className="support-bar-more"> · {t("support.modeUntil", { time: time(until), left: timeLeft(until, now) })}</span>
      </p>
      {level === "view" ? (
        <span className="support-bar-tag" title={t("support.readOnlyHint")}>
          <Icon size={14} aria-hidden /> {t("support.readOnly")}
        </span>
      ) : null}
      {!showPeople ? (
        <span className="support-bar-tag" title={t("support.peopleHiddenHint")}>
          <EyeOff size={14} aria-hidden /> {t("support.peopleHidden")}
        </span>
      ) : null}
      <Button size="sm" variant="danger-solid" onClick={() => setConfirm(true)}>
        <LogOut size={16} aria-hidden /> {t("support.leave.button")}
      </Button>
      <Confirm
        open={confirm}
        onOpenChange={setConfirm}
        title={t("support.leave.title")}
        text={t("support.leave.text")}
        confirm={t("support.leave.confirm")}
        danger
        loading={leave.isPending}
        onConfirm={() => leave.mutate()}
      />
    </div>
  );
}
