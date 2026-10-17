// Where a support operator comes in. The admin's link is <panel>/<secret path>/support#<token>:
// the token sits after the #, so it never reaches a server or a proxy's log. It is read once,
// removed from the address at once (history, bookmarks, a shared screen), and exchanged for
// a session. A link works for one browser only: opening it again gives "already used".
import { useQueryClient } from "@tanstack/react-query";
import { useNavigate, useSearch } from "@tanstack/react-router";
import { LifeBuoy, LinkIcon, ShieldAlert } from "lucide-react";
import { useEffect, useState } from "react";
import { api, ApiError, setCsrf, unwrap } from "../../api/client";
import { supportKey, whoamiQuery, type SupportWhoami } from "../../api/support";
import { LangSwitch } from "../../components/lang";
import { EmptyState, Skeleton } from "../../components/ui";
import { t, tMaybe } from "../../i18n";

/** A token as the API makes it: ms_ and 40 base62. */
const TOKEN = /^ms_[0-9A-Za-z]{40}$/;

type Outcome = { ok: true; who: SupportWhoami } | { ok: false; code: string };

// The entry runs once per page load, whatever React does with the effect (StrictMode runs it
// twice): a second call would burn the one-time link and answer "already used".
let entering: Promise<Outcome> | null = null;

function enterOnce(): Promise<Outcome> | null {
  if (entering) return entering;
  const hash = window.location.hash.replace(/^#/, "");
  if (!hash) return null;
  // Out of the address before anything else can see it.
  window.history.replaceState(null, "", window.location.pathname + window.location.search);
  if (!TOKEN.test(hash)) {
    entering = Promise.resolve({ ok: false, code: "invalid_support_token" });
    return entering;
  }
  entering = unwrap(api.POST("/api/v1/support/enter", { body: { token: hash } })).then(
    (who): Outcome => ({ ok: true, who }),
    (e): Outcome => ({ ok: false, code: e instanceof ApiError ? (e.status === 429 ? "too_many" : e.status === 0 ? "network" : e.detail) : "network" }),
  );
  return entering;
}

export function SupportEnterPage() {
  const { ended } = useSearch({ from: "/support" });
  const navigate = useNavigate();
  const qc = useQueryClient();
  const [outcome, setOutcome] = useState<Outcome | null>(null);
  const [asked, setAsked] = useState(false);
  // No link in the address: an operator who is in already is taken on to the panel.
  const [probing, setProbing] = useState(false);

  useEffect(() => {
    const p = enterOnce();
    let alive = true;
    if (!p) {
      if (ended) return;
      setProbing(true);
      qc.fetchQuery(whoamiQuery).then(
        (who) => {
          if (!alive) return;
          setCsrf(who.csrf_token ?? "");
          void navigate({ to: "/", replace: true });
        },
        () => alive && setProbing(false),
      );
      return () => {
        alive = false;
      };
    }
    setAsked(true);
    void p.then((o) => {
      if (!alive) return;
      if (o.ok) {
        setCsrf(o.who.csrf_token ?? "");
        qc.setQueryData(supportKey.whoami, o.who);
        // The shell reads the operator from this cache entry (the admin's own data is closed to them).
        void navigate({ to: "/", replace: true });
      } else {
        setOutcome(o);
      }
    });
    return () => {
      alive = false;
    };
  }, [navigate, qc, ended]);

  return (
    <div className="grid min-h-[100dvh] place-items-center px-4 py-12">
      <section className="card glass w-full max-w-[520px]" aria-live="polite">
        {(asked && !outcome) || probing ? (
          <div className="state-box" role="status" aria-busy>
            <div className="state-mark">
              <LifeBuoy size={22} aria-hidden />
            </div>
            <h1 className="sr-only">{t("support.enter.title")}</h1>
            <Skeleton style={{ width: 200, height: 20 }} />
            <p>{probing ? t("support.enter.checking") : t("support.enter.entering")}</p>
          </div>
        ) : outcome && !outcome.ok ? (
          <EnterError code={outcome.code} />
        ) : ended ? (
          <EmptyState icon={ShieldAlert} title={t("support.enter.endedTitle")} text={t("support.enter.endedText")} />
        ) : (
          <EmptyState icon={LinkIcon} title={t("support.enter.noLinkTitle")} text={t("support.enter.noLinkText")} />
        )}
      </section>
      <LangSwitch className="mt-4" />
    </div>
  );
}

function EnterError({ code }: { code: string }) {
  const known = ["invalid_support_token", "support_link_used", "too_many", "network"].includes(code) ? code : "other";
  return (
    <div className="state-box" role="alert">
      <div className="state-mark err">
        <ShieldAlert size={22} aria-hidden />
      </div>
      <h1 className="text-[20px] font-medium leading-7">{tMaybe(`support.enter.err.${known}Title`)}</h1>
      <p>{tMaybe(`support.enter.err.${known}Text`) ?? ""}</p>
    </div>
  );
}
