import "../styles/app.css";
import "../styles/fonts-mono.css";
import { QueryClient, QueryClientProvider } from "@tanstack/react-query";
import { RouterProvider } from "@tanstack/react-router";
import { MotionConfig } from "motion/react";
import { StrictMode } from "react";
import { createRoot } from "react-dom/client";
import { ApiError } from "../api/client";
import { whoamiQuery } from "../api/support";
import { Atmosphere } from "../components/atmosphere";
import { getTheme, setTheme } from "../components/theme";
import { ErrorBoundary } from "../components/error-boundary";
import { ToastProvider } from "../components/toast";
import { initI18n } from "../i18n";
import { adminDicts } from "../i18n/admin";
import { registerWorker, watchInstall } from "../lib/pwa";
import { createAppRouter } from "./router";

// The panel as an installed app: the browser's install offer comes once per load, long before
// Settings is opened. The worker only in a build: the dev server is not the panel's path.
watchInstall();
if (import.meta.env.PROD) window.addEventListener("load", registerWorker, { once: true });

const queryClient = new QueryClient({
  defaultOptions: {
    queries: {
      staleTime: 5_000,
      retry: (count, e) => !(e instanceof ApiError && [401, 403, 404].includes(e.status)) && count < 2,
      refetchOnWindowFocus: true,
    },
  },
});

const router = createAppRouter(queryClient);

// Apply the saved theme before the first render so navigation never resets it.
setTheme(getTheme(), { keep: false });

window.addEventListener("mikan:unauthorized", () => {
  if (router.state.location.pathname === "/login" || router.state.location.pathname === "/support") return;
  // A support operator whose access ended (revoked, or by its time) is told so, not sent to a login that is not theirs.
  const operator = !!queryClient.getQueryData(whoamiQuery.queryKey);
  queryClient.clear();
  if (operator) {
    void router.navigate({ to: "/support", search: { ended: true } });
    return;
  }
  void router.navigate({ to: "/login", search: { next: router.state.location.href } });
});

// Dictionaries load before the first render: t() stays synchronous everywhere. Pages read
// their texts at render time and subscribe to the language themselves (see page() in
// router.tsx), so a switch redraws them without remounting anything.
void initI18n(adminDicts).then(() => {
  createRoot(document.getElementById("root")!).render(
    <StrictMode>
      <ErrorBoundary>
        <QueryClientProvider client={queryClient}>
          {/* Motion follows the system's "reduce motion" like the CSS animations do. */}
          <MotionConfig reducedMotion="user">
            <ToastProvider>
              <Atmosphere />
              <RouterProvider router={router} />
            </ToastProvider>
          </MotionConfig>
        </QueryClientProvider>
      </ErrorBoundary>
    </StrictMode>,
  );
});
