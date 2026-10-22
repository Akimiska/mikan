// The admin panel's service worker: the panel installs as an app (manifest.webmanifest), and
// an app that cannot reach its server shows a page that says so instead of the browser's own
// error. Nothing is cached: the panel's pages, files and data always come from the server, so
// an update of the panel is never hidden behind an old copy. A plain file, not a module: the
// panel's CSP allows scripts from itself only, and this one is served as it is.
"use strict";

self.addEventListener("install", () => self.skipWaiting());
self.addEventListener("activate", (event) => event.waitUntil(self.clients.claim()));

self.addEventListener("fetch", (event) => {
  const req = event.request;
  // Only opening a page: the API, the files and everything else go to the network untouched.
  if (req.mode !== "navigate" || req.method !== "GET") return;
  event.respondWith(fetch(req).catch(() => offlinePage()));
});

const TEXTS = {
  ru: {
    lang: "ru",
    title: "Нет связи с панелью",
    text: "Сервер панели не отвечает. Проверьте интернет или VPN; если панель обновлялась, через минуту она вернётся.",
    retry: "Повторить",
  },
  en: {
    lang: "en",
    title: "The panel is out of reach",
    text: "The panel's server does not answer. Check the internet or the VPN; if the panel was updating, it is back in a minute.",
    retry: "Try again",
  },
};

function offlinePage() {
  const t = String(self.navigator.language || "").toLowerCase().startsWith("ru") ? TEXTS.ru : TEXTS.en;
  const html = `<!doctype html>
<html lang="${t.lang}">
<head>
<meta charset="utf-8">
<meta name="viewport" content="width=device-width, initial-scale=1, viewport-fit=cover">
<meta name="color-scheme" content="light dark">
<title>${t.title}</title>
<style>
  /* The panel's own themes (src/styles/themes.css): mikan, and midnight for a dark system. */
  :root { --bg: #fbf1e8; --card: #ffffff; --ink: #161a24; --muted: #5c6474; --icon: #f07a2e; --fill: #161a24; --on-fill: #ffffff; --line: rgba(22, 26, 36, 0.08); }
  @media (prefers-color-scheme: dark) { :root { --bg: #0d1118; --card: #161d27; --ink: #f4f7fb; --muted: #8b97a8; --icon: #ff843d; --fill: #ff843d; --on-fill: #161a24; --line: rgba(244, 247, 251, 0.1); } }
  * { box-sizing: border-box; }
  body { margin: 0; min-height: 100vh; display: grid; place-items: center; padding: 24px; background: var(--bg); color: var(--ink);
    font: 15px/1.5 system-ui, -apple-system, "Segoe UI", Roboto, sans-serif; }
  main { max-width: 400px; width: 100%; padding: 32px 24px; border-radius: 24px; background: var(--card); border: 1px solid var(--line); text-align: center; }
  svg { width: 40px; height: 40px; color: var(--icon); }
  h1 { margin: 16px 0 8px; font-size: 20px; line-height: 1.3; }
  p { margin: 0 0 24px; color: var(--muted); }
  a { display: inline-flex; align-items: center; justify-content: center; min-height: 44px; padding: 0 24px; border-radius: 999px;
    background: var(--fill); color: var(--on-fill); font-weight: 600; text-decoration: none; }
  a:hover { opacity: 0.9; }
  a:focus-visible { outline: 3px solid var(--icon); outline-offset: 3px; }
</style>
</head>
<body>
<main>
  <svg viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2" stroke-linecap="round" stroke-linejoin="round" aria-hidden="true">
    <path d="M12 20h.01"/><path d="M8.5 16.43a5 5 0 0 1 7 0"/><path d="M5 12.86a10 10 0 0 1 5.17-2.69"/><path d="M19 12.86a10 10 0 0 0-2-1.43"/><path d="M2 8.82a15 15 0 0 1 4.17-2.65"/><path d="M10.66 5c4.01-.36 8.14.9 11.34 3.76"/><path d="m2 2 20 20"/>
  </svg>
  <h1>${t.title}</h1>
  <p>${t.text}</p>
  <a href="">${t.retry}</a>
</main>
</body>
</html>`;
  return new Response(html, {
    status: 503,
    headers: {
      "Content-Type": "text/html; charset=utf-8",
      "Cache-Control": "no-store",
      "Content-Security-Policy": "default-src 'none'; style-src 'unsafe-inline'; base-uri 'none'; form-action 'none'",
    },
  });
}
