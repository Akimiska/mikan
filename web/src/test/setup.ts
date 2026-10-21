// The browser globals i18n reads when it loads (the saved language, the panel's, the
// browser's), for tests in node; then the Russian dictionary, the texts the tests compare.
// i18n is imported after them: a static import would run before this code.
const memory = new Map<string, string>([["mikan.lang", "ru"]]);
Object.assign(globalThis, {
  localStorage: { getItem: (k: string) => memory.get(k) ?? null, setItem: (k: string, v: string) => void memory.set(k, v) },
  document: { querySelector: () => null, documentElement: {} },
});

const { initI18n } = await import("../i18n");
await initI18n({ ru: () => import("../i18n/ru.json"), en: () => import("../i18n/en.json") });

export {};
