import { useSyncExternalStore } from "react";

/*
 * The panel as an installed app (manifest.webmanifest, public/sw.js). Chromium browsers say a
 * page can be installed with `beforeinstallprompt`, once it has a manifest, a service worker and
 * a certificate they trust: the event is kept here for the button in Settings, which shows the
 * browser's own install dialog. Safari and Firefox have no such event: the card says how to
 * install by hand.
 */

/** How this browser installs a site. */
export type InstallPlatform =
  | "ios" // iPhone and iPad, any browser: Share → Add to Home Screen
  | "mac-safari" // Safari on a Mac: File → Add to Dock
  | "firefox-android" // Firefox on Android: menu → Install
  | "firefox" // Firefox on a computer: no installing of sites
  | "chromium" // Chrome, Edge, Yandex, Opera on a computer: the button, or the browser's menu
  | "chromium-android" // the same on Android, Samsung Internet too: the button, or the ⋮ menu
  | "other";

export type InstallState = {
  platform: InstallPlatform;
  /** The panel runs as the installed app right now. */
  standalone: boolean;
  /** The browser offered its install dialog: the button shows it. */
  canPrompt: boolean;
  /** Installed during this visit. */
  installed: boolean;
  /** HTTPS (or localhost): no browser installs a site without it. */
  secure: boolean;
  /** The service worker did not register; in a Chromium browser, a certificate it does not trust. */
  workerFailed: boolean;
};

/** The way a browser of user agent ua installs sites; touchPoints tells an iPad from a Mac. */
export function installPlatform(ua: string, touchPoints: number): InstallPlatform {
  // iPadOS presents itself as a Mac; only the touch screen gives it away.
  if (/iPhone|iPad|iPod/.test(ua) || (/Macintosh/.test(ua) && touchPoints > 1)) return "ios";
  if (/Firefox|FxiOS/.test(ua)) return /Android/.test(ua) ? "firefox-android" : "firefox";
  if (/Chrome|Chromium|Edg\/|OPR\/|YaBrowser|SamsungBrowser/.test(ua)) return /Android/.test(ua) ? "chromium-android" : "chromium";
  if (/Macintosh/.test(ua) && /Safari\//.test(ua)) return "mac-safari";
  return "other";
}

type BeforeInstallPromptEvent = Event & {
  prompt: () => Promise<void>;
  userChoice: Promise<{ outcome: "accepted" | "dismissed" }>;
};

let deferred: BeforeInstallPromptEvent | null = null;
let installed = false;
let workerFailed = false;
const listeners = new Set<() => void>();

const standaloneQueries = ["(display-mode: standalone)", "(display-mode: window-controls-overlay)", "(display-mode: minimal-ui)"];

function isStandalone(): boolean {
  const iosApp = (navigator as Navigator & { standalone?: boolean }).standalone === true;
  return iosApp || standaloneQueries.some((q) => window.matchMedia?.(q).matches);
}

function read(): InstallState {
  return {
    platform: installPlatform(navigator.userAgent, navigator.maxTouchPoints ?? 0),
    standalone: isStandalone(),
    canPrompt: deferred !== null,
    installed,
    secure: window.isSecureContext,
    workerFailed,
  };
}

let snapshot: InstallState | null = null;

function emit() {
  snapshot = read();
  listeners.forEach((l) => l());
}

/**
 * Listens for what the browser says about installing, from the start: Chromium fires
 * `beforeinstallprompt` once per page load, long before Settings may be opened.
 */
export function watchInstall() {
  window.addEventListener("beforeinstallprompt", (e) => {
    // The mini-infobar on Android stays away: the panel offers it in Settings, where it is asked for.
    e.preventDefault();
    deferred = e as BeforeInstallPromptEvent;
    emit();
  });
  window.addEventListener("appinstalled", () => {
    deferred = null;
    installed = true;
    emit();
  });
  for (const q of standaloneQueries) window.matchMedia?.(q).addEventListener?.("change", emit);
}

/** Registers public/sw.js (the offline page and what browsers ask of an installable app). */
export function registerWorker() {
  if (!("serviceWorker" in navigator)) return;
  // Relative: the page's <base> is the secret admin path, the worker's scope is the panel alone.
  navigator.serviceWorker.register("sw.js").catch(() => {
    workerFailed = true;
    emit();
  });
}

/** Shows the browser's install dialog; "unavailable" when the browser offered none. */
export async function promptInstall(): Promise<"accepted" | "dismissed" | "unavailable"> {
  const e = deferred;
  if (!e) return "unavailable";
  // The event is good for one dialog: the button goes until the browser offers another.
  deferred = null;
  emit();
  await e.prompt();
  const { outcome } = await e.userChoice;
  if (outcome === "accepted") {
    installed = true;
    emit();
  }
  return outcome;
}

function subscribe(listener: () => void) {
  listeners.add(listener);
  return () => listeners.delete(listener);
}

function current(): InstallState {
  snapshot ??= read();
  return snapshot;
}

/** What this browser can do about installing the panel, kept current. */
export function useInstallState(): InstallState {
  return useSyncExternalStore(subscribe, current, current);
}
