import { useEffect, useState } from "react";
import { Check, Gift, RefreshCw } from "lucide-react";
import { Button } from "../components/ui";
import { t } from "../i18n";
import { initData, subRoot, tokenOf } from "./net";

type Item = { code: string; days: number; bytes: number; discount: number; currency: string; at: number };

export function PromoSection({ token, activeCode, onApplied, onBonusApplied }: { token: string; activeCode: string; onApplied: (code: string) => void; onBonusApplied: () => void }) {
  const [code, setCode] = useState("");
  const [items, setItems] = useState<Item[]>([]);
  const [busy, setBusy] = useState(false);
  const [msg, setMsg] = useState("");
  const [err, setErr] = useState("");

  const load = () => fetch(`${subRoot}/tg/promo-history`, { method: "POST", headers: { "Content-Type": "application/json" }, body: JSON.stringify({ init_data: initData }), cache: "no-store" })
    .then((r) => r.ok ? r.json() : Promise.reject())
    .then((x) => setItems(x.items ?? []))
    .catch(() => undefined);

  useEffect(() => { void load(); }, [token]);

  const errorMessage = (code: string) => ({
    promo_try_later: t("sub.promoTryLater"),
    promo_not_found: t("sub.promoNotFound"),
    promo_inactive: t("sub.promoInactive"),
    promo_expired: t("sub.promoExpired"),
    promo_limit: t("sub.promoLimit"),
    promo_user_limit: t("sub.promoUserLimit"),
    promo_tariff: t("sub.promoTariff"),
    promo_minimum: t("sub.promoMinimum"),
    promo_new_user: t("sub.promoNewUser"),
    promo_first_purchase: t("sub.promoFirstPurchase"),
    promo_currency: t("sub.promoCurrency"),
    promo_not_discount: t("sub.promoNotDiscount"),
    promo_invalid_value: t("sub.promoUnavailable"),
    promo_refund_unsupported: t("sub.promoRefundUnsupported"),
    promo_subscription_required: t("sub.promoSubscription"),
    promo_no_expiry: t("sub.promoNoExpiry"),
  } as Record<string, string>)[code] ?? t("sub.promoUnavailable");

  const apply = async () => {
    if (!code.trim()) return;
    setBusy(true); setMsg(""); setErr("");
    try {
      const r = await fetch(`${subRoot}/tg/promo`, { method: "POST", headers: { "Content-Type": "application/json" }, body: JSON.stringify({ init_data: initData, token: tokenOf(token), code: code.trim() }), cache: "no-store" });
      const body = await r.json().catch(() => ({}));
      if (!r.ok) { setErr(errorMessage(body.code || "")); return; }
      if (body.type === "days") { setMsg(t("sub.promoDaysApplied", { n: body.days })); onApplied(""); onBonusApplied(); }
      else if (body.type === "traffic") { setMsg(t("sub.promoTrafficApplied", { n: (body.bytes / 1073741824).toFixed(0) })); onApplied(""); onBonusApplied(); }
      else { setMsg(t("sub.promoDiscountApplied")); onApplied(code.trim().toUpperCase()); }
      setCode(""); await load();
    } catch {
      setErr(t("sub.promoUnavailable"));
    } finally { setBusy(false); }
  };

  return <section id="promocodes" className="glass rounded-3xl p-4"><div className="flex items-center gap-2"><Gift size={18} /><h2 className="text-[15px] font-semibold">{t("sub.promoTitle")}</h2></div><p className="mt-1 text-xs text-[var(--ink-500)]">{t("sub.promoHint")}</p>{activeCode && <div className="mt-3 flex items-center justify-between rounded-xl bg-[var(--leaf-50)] px-3 py-2 text-xs text-[var(--leaf-700)]"><span>{t("sub.promoSelected", { code: activeCode })}</span><button className="link-btn" onClick={() => { onApplied(""); setMsg(""); }} type="button">{t("sub.promoClear")}</button></div>}<div className="mt-3 flex gap-2"><input className="input min-w-0 flex-1" value={code} onChange={(e) => setCode(e.target.value.toUpperCase())} placeholder="WELCOME30" onKeyDown={(e) => { if (e.key === "Enter") void apply(); }} /><Button variant="primary" loading={busy} onClick={() => void apply()}>{t("sub.promoApply")}</Button></div>{msg && <p className="mt-3 flex items-center gap-2 text-xs text-[var(--leaf-600)]"><Check size={14} />{msg}</p>}{err && <p className="mt-3 text-xs text-[var(--berry-600)]" role="alert">{err}</p>}<div className="mt-4 border-t border-[var(--hairline)] pt-3"><div className="mb-2 flex items-center justify-between text-xs font-semibold"><span>{t("sub.promoHistory")}</span><button className="link-btn" onClick={() => void load()} aria-label={t("common.retry")}><RefreshCw size={13} /></button></div>{items.length === 0 ? <div className="text-xs text-[var(--ink-500)]">{t("sub.promoNoHistory")}</div> : <div className="flex flex-col gap-2">{items.map((item, i) => <div key={`${item.code}-${item.at}-${i}`} className="text-xs"><b>{item.code}</b> · {new Date(item.at * 1000).toLocaleString()}{item.days ? ` · +${item.days} ${t("promocodes.daysUnit")}` : ""}{item.bytes ? ` · +${(item.bytes / 1073741824).toFixed(0)} ${t("promocodes.trafficUnit")}` : ""}{item.discount ? ` · −${item.discount} ${item.currency}` : ""}</div>)}</div>}</div></section>;
}
