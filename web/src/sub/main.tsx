import "../styles/app.css";
import { Check, Copy, LifeBuoy, QrCode } from "lucide-react";
import { motion } from "motion/react";
import { StrictMode, useEffect, useState } from "react";
import { createRoot } from "react-dom/client";
import { Atmosphere } from "../components/atmosphere";
import { LangSwitch } from "../components/lang";
import { Pill, QR, Ring, Skeleton } from "../components/ui";
import { t, useLocale } from "../i18n";
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

type App = { name: string; note: "easiest" | "free" | "stable" | "openSource" | "modern" | "bestWindows" | "tun" | "oneButton"; link: (url: string, brand: string) => string };
const enc = encodeURIComponent;
const clash = (url: string, brand: string) => `clash://install-config?url=${enc(url)}&name=${enc(brand)}`;
const APPS: Record<Platform, App[]> = {
  ios: [
    { name: "Happ", note: "easiest", link: (u) => `happ://add/${u}` },
    { name: "Streisand", note: "free", link: (u, b) => `streisand://import/${u}#${enc(b)}` },
    { name: "v2RayTun", note: "stable", link: (u) => `v2raytun://import/${u}` },
  ],
  android: [
    { name: "Happ", note: "easiest", link: (u) => `happ://add/${u}` },
    { name: "INCY", note: "modern", link: (u) => `incy://add/${u}` },
    { name: "v2RayTun", note: "stable", link: (u) => `v2raytun://import/${u}` },
    { name: "Hiddify", note: "openSource", link: (u, b) => `hiddify://import/${u}#${enc(b)}` },
  ],
  windows: [
    { name: "Koala Clash", note: "bestWindows", link: (u, b) => `koala-clash://install-config?url=${enc(u)}&name=${enc(b)}` },
    { name: "Hiddify", note: "easiest", link: (u, b) => `hiddify://import/${u}#${enc(b)}` },
    { name: "Clash Verge Rev", note: "tun", link: clash },
  ],
  macos: [
    { name: "Clash Verge Rev", note: "tun", link: clash },
    { name: "Happ", note: "oneButton", link: (u) => `happ://add/${u}` },
    { name: "Hiddify", note: "openSource", link: (u, b) => `hiddify://import/${u}#${enc(b)}` },
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
          <h1 className="font-display text-xl font-medium">{t("sub.loadFailed")}</h1>
          <p className="mt-2 text-[13px] text-[var(--ink-500)]">{t("sub.loadFailedText")}</p>
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
  const firstName = info.name.split(/\s+/)[0] ?? "";
  const tone = ({ active: "ok", expiring: "warn", limited: "bad", expired: "bad", disabled: "off" } as const)[info.state];
  const [leftValue, leftUnit] = left != null ? bytes(left).split(" ") : ["∞", ""];

  return (
    <Shell brand={info.brand}>
      <motion.section className="glass rounded-3xl p-4" initial={{ opacity: 0, y: 8 }} animate={{ opacity: 1, y: 0 }}>
        <h1 className="font-display text-xl leading-7 font-medium tracking-tight">{t(`sub.status.${info.state}`, { name: firstName })}</h1>
        <div className="mt-2 flex items-center justify-between gap-2 text-[13px] text-[var(--ink-600)]">
          <span>{info.expires_at ? t("sub.until", { date: dateLong(info.expires_at) }) : t("sub.forever")}</span>
          {d !== null && d >= 0 ? <Pill tone={tone}>{days(d)}</Pill> : null}
        </div>
        {(info.state === "expired" || info.state === "limited" || info.state === "disabled") && info.support_url ? (
          <a className="btn btn-primary btn-block mt-4" href={info.support_url} target="_blank" rel="noreferrer noopener">
            {t("sub.renew")}
          </a>
        ) : null}
      </motion.section>

      <section className="glass grid grid-cols-[104px_1fr] items-center gap-4 rounded-3xl p-4">
        <Ring size={104} pct={info.limit != null ? pct : 100} label={leftValue} sub={left != null ? t("sub.left", { unit: leftUnit ?? "" }) : t("users.unlimited")} />
        <div className="flex flex-col gap-2 text-xs text-[var(--ink-500)]">
          <div>
            {t("userDrawer.used")}
            <b className="num block text-base font-medium text-[var(--ink-900)]">{info.limit != null ? `${bytes(used)} ${t("users.of", { total: bytes(info.limit) })}` : bytes(used)}</b>
          </div>
          {info.resets_at ? (
            <div>
              {t("sub.resets")}
              <b className="block text-base font-medium text-[var(--ink-900)]">{dateShort(info.resets_at)}</b>
            </div>
          ) : null}
          {info.device_limit ? (
            <div>
              {t("sub.devices")}
              <b className="num block text-base font-medium text-[var(--ink-900)]">{t("sub.upTo", { n: info.device_limit })}</b>
            </div>
          ) : null}
        </div>
      </section>

      <section className="glass rounded-3xl p-4">
        <h2 className="mb-3 text-[15px] font-semibold">{t("sub.connect")}</h2>
        <div className="mb-3 flex gap-1 rounded-[14px] bg-[var(--hover)] p-1" role="group" aria-label={t("sub.platform")}>
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
                  {i === 0 ? <span className="font-medium text-[var(--mikan-700)]">{t("sub.recommended")} · </span> : null}
                  {t(`sub.notes.${a.note}`)}
                </div>
              </div>
              <a className={i === 0 ? "btn btn-primary btn-sm" : "btn btn-glass btn-sm"} href={a.link(subURL, info.brand)}>
                {t("common.add")}
              </a>
            </div>
          ))}
        </div>
      </section>

      <section className="glass rounded-3xl p-4">
        <h2 className="mb-3 text-[15px] font-semibold">{t("sub.howTo")}</h2>
        <ol className="flex flex-col gap-3 text-[13px] text-[var(--ink-600)]">
          {([1, 2, 3] as const).map((n) => (
            <li key={n} className="grid grid-cols-[28px_1fr] items-start gap-3">
              <span className="font-display grid h-7 w-7 place-items-center rounded-full border border-[var(--hairline)] bg-white text-xs font-semibold">{n}</span>
              <div>
                <b className="block font-semibold text-[var(--ink-900)]">{t(`sub.steps.${n}.title`)}</b>
                {t(`sub.steps.${n}.text`)}
              </div>
            </li>
          ))}
        </ol>
      </section>

      <section className="glass rounded-3xl p-4">
        <h2 className="mb-3 text-[15px] font-semibold">{t("sub.link")}</h2>
        <div className="link-field">
          <span className="mono">{subURL}</span>
          <button type="button" className="icon-btn" onClick={copy} aria-label={t("common.copyLink")}>
            {copied ? <Check size={18} className="text-[var(--leaf-500)]" /> : <Copy size={18} />}
          </button>
        </div>
        <button type="button" className="btn btn-glass btn-sm mt-2" onClick={() => setQr((v) => !v)} aria-expanded={qr}>
          <QrCode size={16} aria-hidden /> {t("sub.qrOther")}
        </button>
        {qr ? (
          <div className="mt-3 flex justify-center">
            <QR value={subURL} size={200} />
          </div>
        ) : null}
      </section>

      {info.support_url ? (
        <a className="btn btn-glass btn-block h-12 rounded-2xl" href={info.support_url} target="_blank" rel="noreferrer noopener">
          <LifeBuoy size={18} aria-hidden /> {t("sub.support")}
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
        <LangSwitch className="ml-auto" />
      </div>
      {children}
    </main>
  );
}

function Root() {
  const locale = useLocale();
  return <SubPage key={locale} />;
}

createRoot(document.getElementById("root")!).render(
  <StrictMode>
    <Atmosphere />
    <Root />
  </StrictMode>,
);
