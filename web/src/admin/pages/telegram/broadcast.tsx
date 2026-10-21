// A message to every subscriber of the bot, and how far it got.
import { useMutation, useQueryClient } from "@tanstack/react-query";
import { Send } from "lucide-react";
import { useMemo, useState } from "react";
import { api, errorText, unwrap, type Schemas } from "../../../api/client";
import { qk } from "../../../api/hooks";
import { useSupportMode } from "../../../api/support";
import { CardHead } from "../../../components/layout";
import { Confirm } from "../../../components/overlay";
import { useToast } from "../../../components/toast";
import { Bar, Button } from "../../../components/ui";
import { t } from "../../../i18n";
import { ago, num } from "../../../lib/format";
import { ClosedForSupport } from "../../support-closed";
import { MarkdownHint, TgTextPreview } from "../telegram-text";
import { useSampleVars } from "./preview";
import { rise, type View } from "./shared";

export function BroadcastCard({ v }: { v: View }) {
  const operator = useSupportMode();
  const toast = useToast();
  const qc = useQueryClient();
  const [text, setText] = useState("");
  const [confirm, setConfirm] = useState(false);
  const send = useMutation({
    mutationFn: () => unwrap(api.POST("/api/v1/telegram/broadcast", { body: { text } })),
    onSuccess: (r) => {
      toast.ok(t("telegram.broadcastSent", { n: r.queued }));
      setText("");
      setConfirm(false);
      void qc.invalidateQueries({ queryKey: qk.telegram });
    },
    onError: (e) => toast.error(errorText(e)),
  });
  const busy = !!v.broadcast?.active;
  const ready = v.running && v.accounts > 0 && !busy;
  // A broadcast fills only {brand}.
  const sample = useSampleVars();
  const brandOnly = useMemo(() => ({ brand: sample.brand ?? "VPN" }), [sample.brand]);
  // A mailing to every subscriber is the admin's alone.
  if (operator) return <ClosedForSupport />;
  return (
    <section {...rise(4)}>
      <CardHead title={t("telegram.broadcast")} sub={t("telegram.broadcastSub", { n: v.accounts })} />
      <textarea className="input" rows={4} maxLength={3500} value={text} onChange={(e) => setText(e.target.value)} placeholder={t("telegram.broadcastPlaceholder")} aria-label={t("telegram.broadcast")} disabled={!v.running} />
      <div className="mt-1">
        <MarkdownHint />
      </div>
      <TgTextPreview text={text} vars={brandOnly} label={t("telegram.md.preview")} />
      <div className="mt-3 flex flex-wrap items-center gap-3">
        <Button variant="primary" disabled={!ready || !text.trim()} onClick={() => setConfirm(true)}>
          <Send size={16} aria-hidden /> {t("telegram.broadcastButton")}
        </Button>
        <span className="text-xs text-[var(--ink-500)]">{busy ? t("telegram.broadcasting") : !v.running ? t("telegram.broadcastOff") : t("telegram.broadcastHint")}</span>
      </div>
      {v.broadcast && <BroadcastProgress b={v.broadcast} />}
      <Confirm
        open={confirm}
        onOpenChange={setConfirm}
        title={t("telegram.broadcastTitle", { n: v.accounts })}
        text={t("telegram.broadcastText")}
        confirm={t("telegram.broadcastButton")}
        loading={send.isPending}
        onConfirm={() => send.mutate()}
      />
    </section>
  );
}

/** How far the last broadcast went; the bot sends up to 20 a second. */
function BroadcastProgress({ b }: { b: Schemas["TelegramBroadcast"] }) {
  const done = b.sent + b.failed;
  const sec = Math.ceil((b.total - done) / 20);
  const eta = sec < 60 ? t("telegram.broadcastEtaSec", { n: Math.max(1, sec) }) : t("telegram.broadcastEtaMin", { n: Math.ceil(sec / 60) });
  return (
    <div className="mt-4 rounded-2xl border border-[var(--hairline)] bg-[var(--glass-strong)] p-3" aria-live="polite">
      <div className="flex items-baseline justify-between gap-3 text-[13px]">
        <span className="font-medium">{b.active ? t("telegram.broadcastGoing") : t("telegram.broadcastLast", { when: ago(new Date(b.started * 1000).toISOString()) })}</span>
        <span className="tabular-nums text-[var(--ink-500)]">{t("telegram.broadcastCount", { done: num(b.sent), total: num(b.total) })}</span>
      </div>
      <div className="mt-2" role="progressbar" aria-label={t("telegram.broadcast")} aria-valuemin={0} aria-valuemax={b.total} aria-valuenow={done}>
        <Bar pct={b.total ? (done / b.total) * 100 : 100} />
      </div>
      {(b.active || b.failed > 0) && (
        <div className="mt-2 flex flex-wrap gap-x-3 gap-y-1 text-xs text-[var(--ink-500)]">
          {b.active && <span>{eta}</span>}
          {b.failed > 0 && <span>{t("telegram.broadcastFailed", { n: num(b.failed) })}</span>}
        </div>
      )}
    </div>
  );
}
