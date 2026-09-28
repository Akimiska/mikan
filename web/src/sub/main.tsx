import "../styles/app.css";
import { Check, Copy, LifeBuoy, QrCode } from "lucide-react";
import { motion } from "motion/react";
import { StrictMode, useEffect, useState } from "react";
import { createRoot } from "react-dom/client";
import { Atmosphere } from "../components/atmosphere";
import { Pill, QR, Ring, Skeleton } from "../components/ui";
import { bytes, dateLong, dateShort, days, daysUntil } from "../lib/format";

type Info = {
  name: string;
  brand: string;
  support_url?: string;
  state: "active" | "expiring" | "limited" | "expired" | "disabled";
  used_up: number;
  used_down: number;
  limit?: number;
  expires_at?: string;
  resets_at?: string;
  device_limit: number;
  protocols: string[];
};

type Platform = "ios" | "android" | "windows" | "macos";

// The same subscription URL serves the config to apps; the page only links to it.
const subURL = location.origin + location.pathname.replace(/\/$/, "");

type App = { name: string; note: string; link: (url: string, brand: string) => string };
const enc = encodeURIComponent;
const clash = (url: string, brand: string) => `clash://install-config?url=${enc(url)}&name=${enc(brand)}`;
const APPS: Record<Platform, App[]> = {
  ios: [
    { name: "Happ", note: "Проще всего: одна кнопка", link: (u) => `happ://add/${u}` },
    { name: "Streisand", note: "Бесплатно, без рекламы", link: (u, b) => `streisand://import/${u}#${enc(b)}` },
    { name: "v2RayTun", note: "Лёгкое и стабильное", link: (u) => `v2raytun://import/${u}` },
  ],
  android: [
    { name: "Happ", note: "Проще всего: одна кнопка", link: (u) => `happ://add/${u}` },
    { name: "v2RayTun", note: "Лёгкое и стабильное", link: (u) => `v2raytun://import/${u}` },
    { name: "Hiddify", note: "Открытый код", link: (u, b) => `hiddify://import/${u}#${enc(b)}` },
    { name: "FlClash", note: "Гибкие маршруты", link: clash },
  ],
  windows: [
    { name: "Hiddify", note: "Проще всего", link: (u, b) => `hiddify://import/${u}#${enc(b)}` },
    { name: "Clash Verge Rev", note: "Маршруты и режим TUN", link: clash },
    { name: "FlClash", note: "Лёгкий клиент mihomo", link: clash },
  ],
  macos: [
    { name: "Clash Verge Rev", note: "Маршруты и режим TUN", link: clash },
    { name: "Happ", note: "Одна кнопка", link: (u) => `happ://add/${u}` },
    { name: "Hiddify", note: "Открытый код", link: (u, b) => `hiddify://import/${u}#${enc(b)}` },
  ],
};

function detect(): Platform {
  const ua = navigator.userAgent;
  if (/iPhone|iPad|iPod/.test(ua)) return "ios";
  if (/Android/.test(ua)) return "android";
  if (/Mac OS X/.test(ua)) return "macos";
  if (/Windows/.test(ua)) return "windows";
  return "android";
}

function SubPage() {
  const [info, setInfo] = useState<Info | null>(null);
  const [failed, setFailed] = useState(false);
  const [platform, setPlatform] = useState<Platform>(detect);
  const [qr, setQr] = useState(false);
  const [copied, setCopied] = useState(false);

  useEffect(() => {
    fetch(subURL + "/info", { cache: "no-store" })
      .then((r) => (r.ok ? r.json() : Promise.reject(new Error(String(r.status)))))
      .then((d: Info) => {
        setInfo(d);
        document.title = d.brand;
      })
      .catch(() => setFailed(true));
  }, []);

  const copy = async () => {
    try {
      await navigator.clipboard.writeText(subURL);
      setCopied(true);
      window.setTimeout(() => setCopied(false), 2000);
    } catch {
      setQr(true);
    }
  };

  if (failed) {
    return (
      <Shell>
        <section className="glass rounded-3xl p-6 text-center">
          <h1 className="font-display text-xl font-medium">Не удалось загрузить подписку</h1>
          <p className="mt-2 text-[13px] text-[var(--ink-500)]">Проверьте интернет и обновите страницу. Если не помогает — напишите тому, кто выдал ссылку.</p>
        </section>
      </Shell>
    );
  }
  if (!info) {
    return (
      <Shell>
        <Skeleton style={{ height: 96, borderRadius: 24 }} />
        <Skeleton style={{ height: 140, borderRadius: 24 }} />
        <Skeleton style={{ height: 220, borderRadius: 24 }} />
      </Shell>
    );
  }

  const used = info.used_up + info.used_down;
  const left = info.limit != null ? Math.max(0, info.limit - used) : null;
  const pct = info.limit ? (used / info.limit) * 100 : 0;
  const d = info.expires_at ? daysUntil(info.expires_at) : null;
  const firstName = info.name.split(/\s+/)[0];
  const status = {
    active: { title: `${firstName}, всё работает`, tone: "ok" as const },
    expiring: { title: `${firstName}, подписка скоро закончится`, tone: "warn" as const },
    limited: { title: "Трафик на этот период закончился", tone: "bad" as const },
    expired: { title: "Подписка закончилась", tone: "bad" as const },
    disabled: { title: "Доступ приостановлен", tone: "off" as const },
  }[info.state];

  return (
    <Shell brand={info.brand}>
      <motion.section className="glass rounded-3xl p-4" initial={{ opacity: 0, y: 8 }} animate={{ opacity: 1, y: 0 }}>
        <h1 className="font-display text-xl leading-7 font-medium tracking-tight">{status.title}</h1>
        <div className="mt-2 flex items-center justify-between gap-2 text-[13px] text-[var(--ink-600)]">
          <span>{info.expires_at ? `Подписка до ${dateLong(info.expires_at)}` : "Подписка бессрочная"}</span>
          {d !== null && d >= 0 ? <Pill tone={status.tone === "off" ? "off" : status.tone}>{days(d)}</Pill> : null}
        </div>
        {(info.state === "expired" || info.state === "limited" || info.state === "disabled") && info.support_url ? (
          <a className="btn btn-primary btn-block mt-4" href={info.support_url} target="_blank" rel="noreferrer noopener">
            Продлить через поддержку
          </a>
        ) : null}
      </motion.section>

      <section className="glass grid grid-cols-[104px_1fr] items-center gap-4 rounded-3xl p-4">
        <Ring size={104} pct={info.limit != null ? pct : 100} label={left != null ? bytes(left).split(" ")[0] : "∞"} sub={left != null ? `${bytes(left).split(" ")[1]} осталось` : "без лимита"} />
        <div className="flex flex-col gap-2 text-xs text-[var(--ink-500)]">
          <div>
            Израсходовано
            <b className="num block text-base font-medium text-[var(--ink-900)]">{info.limit != null ? `${bytes(used)} из ${bytes(info.limit)}` : bytes(used)}</b>
          </div>
          {info.resets_at ? (
            <div>
              Обновится
              <b className="block text-base font-medium text-[var(--ink-900)]">{dateShort(info.resets_at)}</b>
            </div>
          ) : null}
          {info.device_limit ? (
            <div>
              Устройств одновременно
              <b className="num block text-base font-medium text-[var(--ink-900)]">до {info.device_limit}</b>
            </div>
          ) : null}
        </div>
      </section>

      <section className="glass rounded-3xl p-4">
        <h2 className="mb-3 text-[15px] font-semibold">Подключить устройство</h2>
        <div className="mb-3 flex gap-1 rounded-[14px] bg-[var(--hover)] p-1" role="group" aria-label="Платформа">
          {(
            [
              ["ios", "iPhone"],
              ["android", "Android"],
              ["windows", "Windows"],
              ["macos", "Mac"],
            ] as const
          ).map(([k, l]) => (
            <button
              key={k}
              type="button"
              aria-pressed={platform === k}
              onClick={() => setPlatform(k)}
              className="h-8 flex-1 rounded-[10px] text-xs font-semibold text-[var(--ink-600)] aria-pressed:bg-white aria-pressed:text-[var(--ink-900)] aria-pressed:shadow-sm"
            >
              {l}
            </button>
          ))}
        </div>
        <div className="row-list">
          {APPS[platform].map((a, i) => (
            <div key={a.name} className="grid grid-cols-[40px_minmax(0,1fr)_auto] items-center gap-3 py-2">
              <span className="font-display grid h-10 w-10 place-items-center rounded-xl border border-[var(--hairline)] bg-white text-sm font-semibold text-[var(--ink-700)]" aria-hidden>
                {a.name[0]}
              </span>
              <div className="min-w-0">
                <div className="text-sm font-semibold">{a.name}</div>
                <div className="text-xs text-[var(--ink-500)]">
                  {i === 0 ? <span className="font-medium text-[var(--mikan-700)]">Рекомендуем · </span> : null}
                  {a.note}
                </div>
              </div>
              <a className={i === 0 ? "btn btn-primary btn-sm" : "btn btn-glass btn-sm"} href={a.link(subURL, info.brand)}>
                Добавить
              </a>
            </div>
          ))}
        </div>
      </section>

      <section className="glass rounded-3xl p-4">
        <h2 className="mb-3 text-[15px] font-semibold">Как подключиться</h2>
        <ol className="flex flex-col gap-3 text-[13px] text-[var(--ink-600)]">
          {[
            ["Установите приложение", "Любое из списка выше, из официального магазина."],
            ["Нажмите «Добавить»", "Приложение откроется и само добавит ваш профиль."],
            ["Включите VPN", "Разрешите добавить конфигурацию, если телефон спросит."],
          ].map(([t, d], i) => (
            <li key={t} className="grid grid-cols-[28px_1fr] items-start gap-3">
              <span className="font-display grid h-7 w-7 place-items-center rounded-full border border-[var(--hairline)] bg-white text-xs font-semibold">{i + 1}</span>
              <div>
                <b className="block font-semibold text-[var(--ink-900)]">{t}</b>
                {d}
              </div>
            </li>
          ))}
        </ol>
      </section>

      <section className="glass rounded-3xl p-4">
        <h2 className="mb-3 text-[15px] font-semibold">Ссылка на подписку</h2>
        <div className="link-field">
          <span className="mono">{subURL}</span>
          <button type="button" className="icon-btn" onClick={copy} aria-label="Скопировать ссылку">
            {copied ? <Check size={18} className="text-[var(--leaf-500)]" /> : <Copy size={18} />}
          </button>
        </div>
        <button type="button" className="btn btn-glass btn-sm mt-2" onClick={() => setQr((v) => !v)} aria-expanded={qr}>
          <QrCode size={16} aria-hidden /> QR-код для другого устройства
        </button>
        {qr ? (
          <div className="mt-3 flex justify-center">
            <QR value={subURL} size={200} />
          </div>
        ) : null}
      </section>

      {info.support_url ? (
        <a className="btn btn-glass btn-block h-12 rounded-2xl" href={info.support_url} target="_blank" rel="noreferrer noopener">
          <LifeBuoy size={18} aria-hidden /> Написать в поддержку
        </a>
      ) : null}
    </Shell>
  );
}

function Shell({ brand, children }: { brand?: string; children: React.ReactNode }) {
  return (
    <main className="mx-auto flex max-w-[440px] flex-col gap-3 px-4 pt-6 pb-10">
      <div className="flex items-center gap-2 px-1 pb-1">
        <span className="font-display grid h-7 w-7 place-items-center rounded-[9px] bg-[var(--ink-900)] text-[13px] font-semibold text-white">{(brand ?? "V")[0]}</span>
        <span className="font-display text-[15px] font-semibold tracking-tight">{brand ?? ""}</span>
      </div>
      {children}
    </main>
  );
}

createRoot(document.getElementById("root")!).render(
  <StrictMode>
    <Atmosphere />
    <SubPage />
  </StrictMode>,
);
