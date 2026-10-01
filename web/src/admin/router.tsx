import type { QueryClient } from "@tanstack/react-query";
import { createRootRouteWithContext, createRoute, createRouter, Outlet, redirect } from "@tanstack/react-router";
import { ApiError, basePath, setCsrf } from "../api/client";
import { meQuery } from "../api/hooks";
import { Dashboard } from "./pages/dashboard";
import { InboundsPage } from "./pages/inbounds";
import { LoginPage } from "./pages/login";
import { NodesPage } from "./pages/nodes";
import { SettingsPage } from "./pages/settings";
import { TariffsPage } from "./pages/tariffs";
import { ApiPage } from "./pages/api";
import { TelegramPage } from "./pages/telegram";
import { UsersPage, type UsersSearch } from "./pages/users";
import { Shell } from "./shell";

const STATES = ["all", "active", "expiring", "limited", "expired", "disabled"] as const;

export function createAppRouter(queryClient: QueryClient) {
  const root = createRootRouteWithContext<{ queryClient: QueryClient }>()({ component: () => <Outlet /> });

  const login = createRoute({
    getParentRoute: () => root,
    path: "/login",
    component: LoginPage,
    validateSearch: (s: Record<string, unknown>): { next?: string } => ({ next: typeof s.next === "string" ? s.next : undefined }),
  });

  const app = createRoute({
    getParentRoute: () => root,
    id: "_app",
    component: Shell,
    beforeLoad: async ({ context, location }) => {
      try {
        const me = await context.queryClient.ensureQueryData(meQuery);
        setCsrf(me.csrf_token);
      } catch (e) {
        if (e instanceof ApiError && e.status === 401) {
          throw redirect({ to: "/login", search: { next: location.href } });
        }
        throw e;
      }
    },
  });

  const dashboard = createRoute({ getParentRoute: () => app, path: "/", component: Dashboard });
  const users = createRoute({
    getParentRoute: () => app,
    path: "/users",
    component: UsersPage,
    validateSearch: (s: Record<string, unknown>): UsersSearch => ({
      state: STATES.includes(s.state as (typeof STATES)[number]) ? (s.state as UsersSearch["state"]) : "all",
      q: typeof s.q === "string" ? s.q : "",
      user: typeof s.user === "number" ? s.user : Number(s.user) || undefined,
      create: s.create === true || s.create === "true" ? true : undefined,
    }),
  });
  const tariffs = createRoute({ getParentRoute: () => app, path: "/tariffs", component: TariffsPage });
  const inbounds = createRoute({ getParentRoute: () => app, path: "/inbounds", component: InboundsPage });
  const nodes = createRoute({ getParentRoute: () => app, path: "/nodes", component: NodesPage });
  const settings = createRoute({ getParentRoute: () => app, path: "/settings", component: SettingsPage });
  const telegram = createRoute({ getParentRoute: () => app, path: "/telegram", component: TelegramPage });
  const apiDocs = createRoute({ getParentRoute: () => app, path: "/api-docs", component: ApiPage });

  const routeTree = root.addChildren([login, app.addChildren([dashboard, users, tariffs, inbounds, nodes, telegram, apiDocs, settings])]);
  return createRouter({ routeTree, basepath: basePath || "/", context: { queryClient }, defaultPreload: "intent", scrollRestoration: true });
}

declare module "@tanstack/react-router" {
  interface Register {
    router: ReturnType<typeof createAppRouter>;
  }
}
