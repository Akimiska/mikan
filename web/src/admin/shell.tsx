import { useMutation, useQuery, useQueryClient } from "@tanstack/react-query";
import { Link, Outlet, useNavigate } from "@tanstack/react-router";
import { LayoutDashboard, LogOut, Server, SlidersHorizontal, Tag, Users } from "lucide-react";
import { api, unwrap } from "../api/client";
import { meQuery, useNode, useOverview } from "../api/hooks";
import { Logo } from "../components/atmosphere";
import { LangSwitch } from "../components/lang";
import { Avatar, Bar, Pill } from "../components/ui";
import { t } from "../i18n";
import { num, uptime } from "../lib/format";

const NAV = [
  { to: "/", key: "overview", icon: LayoutDashboard },
  { to: "/users", key: "users", icon: Users },
  { to: "/tariffs", key: "tariffs", icon: Tag },
  { to: "/inbounds", key: "inbounds", icon: Server },
  { to: "/settings", key: "settings", icon: SlidersHorizontal },
] as const;

export function Shell() {
  const overview = useOverview();
  return (
    <>
      <div className="app">
        <aside className="sidebar glass" aria-label={t("shell.sidebar")}>
          <div className="brand">
            <Logo />
            <span className="brand-name">mikan</span>
          </div>
          <nav className="nav" aria-label={t("shell.sections")}>
            {NAV.map((n) => (
              <Link key={n.to} to={n.to} className="nav-item" activeProps={{ className: "active", "aria-current": "page" }} activeOptions={{ exact: n.to === "/" }} title={t(`nav.${n.key}`)}>
                <n.icon size={18} aria-hidden />
                <span className="nav-label">{t(`nav.${n.key}`)}</span>
                {n.to === "/users" && overview.data ? <span className="nav-count num">{num(overview.data.users_total)}</span> : null}
              </Link>
            ))}
          </nav>
          <div className="side-foot">
            <NodeCard />
            <AdminRow />
            <LangSwitch className="self-start" />
          </div>
        </aside>
        <main className="main">
          <Outlet />
        </main>
      </div>
      <nav className="mnav glass" aria-label={t("shell.sections")}>
        {NAV.map((n) => (
          <Link key={n.to} to={n.to} activeProps={{ className: "active", "aria-current": "page" }} activeOptions={{ exact: n.to === "/" }}>
            <n.icon size={20} aria-hidden />
            <span>{t(`navShort.${n.key}`)}</span>
          </Link>
        ))}
      </nav>
    </>
  );
}

function NodeCard() {
  const node = useNode();
  const n = node.data;
  if (!n) {
    return (
      <div className="node-card" aria-busy>
        <span className="sk" style={{ width: "60%" }} />
        <span className="sk mt-3" style={{ width: "90%" }} />
      </div>
    );
  }
  const memPct = n.system.mem_total ? (n.system.mem_used / n.system.mem_total) * 100 : 0;
  return (
    <div className="node-card">
      <div className="node-top">
        <span className="node-name">{t("shell.server")}</span>
        {n.ok ? <Pill tone="ok">{t("shell.running")}</Pill> : <Pill tone="bad">{t("shell.offline")}</Pill>}
      </div>
      <div className="node-sub">{n.ok ? `${n.core}${n.started_at ? ` · ${uptime(n.started_at)}` : ""}` : t("shell.nodeDown")}</div>
      {n.ok ? (
        <div className="node-bars">
          <span>CPU</span>
          <Bar pct={n.system.cpu_percent} />
          <span className="num">{Math.round(n.system.cpu_percent)}%</span>
          <span>RAM</span>
          <Bar pct={memPct} />
          <span className="num">{Math.round(memPct)}%</span>
        </div>
      ) : null}
    </div>
  );
}

function AdminRow() {
  const me = useQuery(meQuery);
  const qc = useQueryClient();
  const navigate = useNavigate();
  const logout = useMutation({
    mutationFn: () => unwrap(api.POST("/api/v1/auth/logout")),
    onSettled: () => {
      qc.clear();
      void navigate({ to: "/login", search: {} });
    },
  });
  const name = me.data?.admin.username ?? "…";
  return (
    <div className="admin-row">
      <Avatar name={name} seed={4} size="sm" />
      <div className="who-wrap min-w-0">
        <div className="truncate text-[13px] font-medium">{name}</div>
        <div className="text-xs text-[var(--ink-500)]">{me.data?.admin.totp_enabled ? t("shell.twoFactorOn") : t("shell.owner")}</div>
      </div>
      <button type="button" className="icon-btn logout ml-auto" aria-label={t("shell.logout")} onClick={() => logout.mutate()} disabled={logout.isPending}>
        <LogOut size={18} />
      </button>
    </div>
  );
}
