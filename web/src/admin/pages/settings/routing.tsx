import { useMutation, useQuery } from "@tanstack/react-query";
import { useState, type FormEvent } from "react";
import { api, ApiError, errorText, unwrap, type Schemas } from "../../../api/client";
import { useNodes } from "../../../api/hooks";
import { Disclosure, FormActions, WithPreview } from "../../../components/layout";
import { SwitchRow } from "../../../components/switch";
import { Button, ErrorState, Field, Pill, Segmented, Skeleton } from "../../../components/ui";
import { t, useLocale } from "../../../i18n";
import { useDraft } from "../../../lib/draft";
import { RulesEditor } from "./rules";
import { useSaveSettings } from "./shared";
import { TemplateCard } from "./template";

type Routes = Schemas["Routes"];
type DNS = NonNullable<Routes["dns"]>;
type List = NonNullable<Routes["lists"]>[number];
type View = "simple" | "yaml";

// As many own lists as the server takes (subs.MaxLists).
const MAX_LISTS = 30;

const MODES = [
  { id: "ru_direct", title: "settings.routingRuDirect", sub: "settings.routingRuDirectSub" },
  { id: "all", title: "settings.routingAll", sub: "settings.routingAllSub" },
  { id: "blocked", title: "settings.routingBlocked", sub: "settings.routingBlockedSub" },
] as const;

// Where a service can go; "node:<id>" options follow from the nodes.
const TARGETS = [
  { id: "", label: "settings.routesFollow" },
  { id: "vpn", label: "settings.routesVpn" },
  { id: "direct", label: "settings.routesDirect" },
  { id: "block", label: "settings.routesBlock" },
] as const;

// Ready DNS sets for the advanced part (GitHub issue #70): the panel's own is the empty one.
const DNS_PRESETS: { id: string; dns: DNS }[] = [
  { id: "panel", dns: {} },
  { id: "tunnel", dns: { nameserver: ["https://1.1.1.1/dns-query#PROXY", "https://8.8.8.8/dns-query#PROXY"], proxy_server_nameserver: ["1.1.1.1", "8.8.8.8"] } },
  {
    id: "ruYandex",
    dns: {
      nameserver: ["https://1.1.1.1/dns-query#PROXY", "https://8.8.8.8/dns-query#PROXY"],
      proxy_server_nameserver: ["77.88.8.8", "1.1.1.1"],
      policy: [
        { match: "+.ru", servers: ["https://77.88.8.8/dns-query"] },
        { match: "+.su", servers: ["https://77.88.8.8/dns-query"] },
        { match: "+.xn--p1ai", servers: ["https://77.88.8.8/dns-query"] },
      ],
    },
  },
];

// What is empty is left out, as the server leaves it: a draft that was emptied again is not a change.
function tidy(r: Routes): Routes {
  const out = { ...r };
  if (!Object.keys(out.services ?? {}).length) delete out.services;
  if (!out.direct?.length) delete out.direct;
  if (!out.lists?.length) delete out.lists;
  if (!out.servers?.auto && !out.servers?.interval && !out.servers?.no_countries) delete out.servers;
  if (!out.tune?.block_quic && !out.tune?.sniffer && !out.tune?.real_ip) delete out.tune;
  if (!out.dns?.nameserver?.length && !out.dns?.proxy_server_nameserver?.length && !out.dns?.policy?.length) delete out.dns;
  return out;
}

const lines = (s: string) =>
  s
    .split("\n")
    .map((x) => x.trim())
    .filter(Boolean);

// "+.cn: https://dns.alidns.com/dns-query, 223.5.5.5" per line.
function parsePolicy(text: string): NonNullable<DNS["policy"]> {
  return lines(text).map((l) => {
    const [match = "", rest = ""] = l.split(/:\s+/, 2);
    return { match: match.trim(), servers: rest.split(",").map((x) => x.trim()).filter(Boolean) };
  });
}
const policyText = (p: DNS["policy"]) => (p ?? []).map((x) => `${x.match}: ${x.servers.join(", ")}`).join("\n");

/** The routing of Clash profiles: the mode, the services, the apps past the tunnel and DNS, with the profile it makes. */
export function RoutingSection({ s, view, onView }: { s: Schemas["SettingsView"]; view: View; onView: (v: View) => void }) {
  const save = useSaveSettings();
  const nodes = useNodes();
  const locale = useLocale();
  const catalog = useQuery({ queryKey: ["routes-catalog"], queryFn: ({ signal }) => unwrap(api.GET("/api/v1/settings/routes/catalog", { signal })), staleTime: Infinity });
  const { draft, setDraft, dirty, reset } = useDraft({ mode: s.sub_routing, routes: s.sub_routes, rules: s.sub_rules });
  const routes = draft.routes;
  const setRoutes = (f: (r: Routes) => Routes) => setDraft((d) => ({ ...d, routes: tidy(f(d.routes)) }));
  const dnsTextOf = (dns: DNS | undefined) => ({ nameserver: (dns?.nameserver ?? []).join("\n"), proxy: (dns?.proxy_server_nameserver ?? []).join("\n"), policy: policyText(dns?.policy) });
  const [dnsText, setDnsText] = useState(() => dnsTextOf(routes.dns));
  // The DNS fields keep their own text, so they are put back with the rest.
  const discard = () => {
    reset();
    setDnsText(dnsTextOf(s.sub_routes.dns));
  };
  const setDns = (dns: DNS) => {
    setRoutes((r) => ({ ...r, dns }));
    setDnsText(dnsTextOf(dns));
  };
  const editDns = (k: keyof typeof dnsText) => (e: React.ChangeEvent<HTMLTextAreaElement>) => {
    const next = { ...dnsText, [k]: e.target.value };
    setDnsText(next);
    setRoutes((r) => ({ ...r, dns: { nameserver: lines(next.nameserver), proxy_server_nameserver: lines(next.proxy), policy: parsePolicy(next.policy) } }));
  };
  const setService = (id: string, target: string) =>
    setRoutes((r) => {
      const services = { ...(r.services ?? {}) };
      if (target) services[id] = target;
      else delete services[id];
      return { ...r, services };
    });
  const toggleDirect = (id: string) =>
    setRoutes((r) => {
      const has = (r.direct ?? []).includes(id);
      return { ...r, direct: has ? (r.direct ?? []).filter((x) => x !== id) : [...(r.direct ?? []), id] };
    });
  const setList = (i: number, patch: Partial<List>) => setRoutes((r) => ({ ...r, lists: (r.lists ?? []).map((l, j) => (j === i ? { ...l, ...patch } : l)) }));
  const addList = () =>
    setRoutes((r) => ({ ...r, lists: [...(r.lists ?? []), { name: `list-${(r.lists?.length ?? 0) + 1}`, url: "", behavior: "classical", format: "yaml", target: "vpn" }] }));
  const dropList = (i: number) => setRoutes((r) => ({ ...r, lists: (r.lists ?? []).filter((_, j) => j !== i) }));
  const servers = routes.servers ?? {};
  const setServers = (patch: Partial<NonNullable<Routes["servers"]>>) => setRoutes((r) => ({ ...r, servers: { ...(r.servers ?? {}), ...patch } }));
  const tune = routes.tune ?? {};
  const setTune = (patch: Partial<NonNullable<Routes["tune"]>>) => setRoutes((r) => ({ ...r, tune: { ...(r.tune ?? {}), ...patch } }));
  const apiErr = save.error instanceof ApiError ? save.error : null;
  const errors = apiErr?.fields ?? {};
  const error = errors.sub_routes;
  const badLine = errors.sub_rules && typeof apiErr?.values.sub_rules === "number" ? apiErr.values.sub_rules : 0;
  const submit = (e: FormEvent) => {
    e.preventDefault();
    save.mutate({ sub_routing: draft.mode, sub_routes: draft.routes, sub_rules: draft.rules });
  };
  const nodeName = (n: { id: number; name: string; local: boolean }) => t("settings.routesOnServer", { name: n.name || (n.local ? t("settings.routesThisServer") : `#${n.id}`) });
  const routed = Object.keys(routes.services ?? {}).length + (routes.direct ?? []).length + (routes.lists ?? []).length;
  const targetSelect = (id: string, value: string, onChange: (v: string) => void, follow = true, label?: string) => (
    <select id={id} className="input max-w-[220px]" value={value} onChange={(e) => onChange(e.target.value)} aria-label={label}>
      {TARGETS.filter((x) => follow || x.id).map((x) => (
        <option key={x.id} value={x.id}>
          {t(x.label)}
        </option>
      ))}
      {(nodes.data ?? []).length ? (
        <optgroup label={t("settings.routesServer")}>
          {(nodes.data ?? []).map((n) => (
            <option key={n.id} value={`node:${n.id}`}>
              {nodeName(n)}
            </option>
          ))}
        </optgroup>
      ) : null}
    </select>
  );
  // Simple settings or an own YAML: one choice of how the routing is set, above either.
  const views = (
    <div className="flex flex-wrap items-center gap-3">
      <Segmented
        label={t("settings.routesView")}
        value={view}
        onChange={onView}
        options={[
          { value: "simple", label: t("settings.routesViewSimple") },
          { value: "yaml", label: t("settings.routesViewYaml") },
        ]}
      />
      {s.sub_template.trim() ? <Pill tone="warn">{t("settings.routesYamlOn")}</Pill> : null}
    </div>
  );
  // The own profile's text lives here, so switching between the simple mode and it keeps it.
  const [yaml, setYaml] = useState(s.sub_template);
  const starter = () => unwrap(api.POST("/api/v1/settings/routes/preview", { body: { sub_routing: draft.mode, sub_routes: draft.routes, starter: true } })).then((r) => r.profile);
  if (view === "yaml") {
    return (
      <WithPreview title={t("settings.routesPreview")} preview={(bare) => <PreviewCard mode={draft.mode} routes={draft.routes} template={yaml} bare={bare} />}>
        {views}
        <TemplateCard s={s} text={yaml} setText={setYaml} starter={starter} />
      </WithPreview>
    );
  }
  return (
    <WithPreview title={t("settings.routesPreview")} preview={(bare) => <PreviewCard mode={draft.mode} routes={draft.routes} rules={draft.rules} bare={bare} />}>
      {views}
      {s.sub_template.trim() ? <div className="banner info">{t("settings.routesYamlLive")}</div> : null}
        <section className="card glass reveal" style={{ "--i": 1 } as React.CSSProperties}>
          <form onSubmit={submit} noValidate>
            <div className="card-head">
              <div>
                <h2 className="card-title">{t("settings.routes")}</h2>
                <div className="card-sub">{t("settings.routesSub")}</div>
              </div>
              {routed ? <Pill tone="off">{t("settings.routesCount", { n: routed })}</Pill> : null}
            </div>

            <Field label={t("settings.routing")} hint={t("settings.routingHint")}>
              <div className="grid gap-2 sm:grid-cols-3" role="radiogroup" aria-label={t("settings.routing")}>
                {MODES.map((m) => (
                  <button key={m.id} type="button" role="radio" aria-checked={draft.mode === m.id} className="opt" onClick={() => setDraft((d) => ({ ...d, mode: m.id }))}>
                    <span className="font-semibold">{t(m.title)}</span>
                    <span className="text-xs text-[var(--ink-500)]">{t(m.sub)}</span>
                  </button>
                ))}
              </div>
            </Field>

            <Field label={t("settings.routesServices")} hint={t("settings.routesServicesHint")}>
              {catalog.isError ? <ErrorState text={errorText(catalog.error)} onRetry={() => void catalog.refetch()} /> : null}
              {catalog.isPending ? (
                <div className="flex flex-col gap-2" aria-busy="true">
                  {[0, 1, 2, 3].map((i) => (
                    <Skeleton key={i} className="h-9 w-full rounded-xl" />
                  ))}
                </div>
              ) : null}
              <div className="flex flex-col divide-y divide-[var(--hairline)]">
                {(catalog.data?.services ?? []).map((svc) => {
                  const id = `s-svc-${svc.id}`;
                  return (
                    <div key={svc.id} className="flex flex-wrap items-center justify-between gap-2 py-2">
                      <label htmlFor={id} className="flex min-w-0 items-center gap-2 text-sm">
                        <span aria-hidden>{svc.icon}</span>
                        <span className="truncate">{locale === "en" ? svc.name_en : svc.name}</span>
                      </label>
                      {targetSelect(id, routes.services?.[svc.id] ?? "", (v) => setService(svc.id, v))}
                    </div>
                  );
                })}
              </div>
            </Field>

            <Field label={t("settings.routesLists")} hint={t("settings.routesListsHint")}>
              <div className="flex flex-col gap-3">
                {(routes.lists ?? []).map((l, i) => (
                  <div key={i} className="panel-soft grid gap-2 p-3 sm:grid-cols-[140px_minmax(0,1fr)]">
                    <input className="input mono" value={l.name} onChange={(e) => setList(i, { name: e.target.value.toLowerCase() })} maxLength={32} aria-label={t("settings.routesListName")} placeholder="whitelist" />
                    <input className="input mono" value={l.url} onChange={(e) => setList(i, { url: e.target.value.trim() })} maxLength={500} inputMode="url" autoComplete="off" spellCheck={false} aria-label={t("settings.routesListUrl")} placeholder="https://raw.githubusercontent.com/…/list.yaml" />
                    <div className="flex flex-wrap items-center gap-2 sm:col-span-2">
                      <select className="input max-w-[170px]" value={l.behavior} onChange={(e) => setList(i, { behavior: e.target.value })} aria-label={t("settings.routesListBehavior")}>
                        <option value="classical">{t("settings.routesListClassical")}</option>
                        <option value="domain">{t("settings.routesListDomain")}</option>
                        <option value="ipcidr">{t("settings.routesListIP")}</option>
                      </select>
                      <select className="input max-w-[110px]" value={l.format} onChange={(e) => setList(i, { format: e.target.value })} aria-label={t("settings.routesListFormat")}>
                        <option value="yaml">YAML</option>
                        <option value="text">TXT</option>
                        <option value="mrs">MRS</option>
                      </select>
                      {targetSelect(`s-list-${i}`, l.target, (v) => setList(i, { target: v }), false, t("settings.routesListTarget"))}
                      <Button variant="ghost" onClick={() => dropList(i)} aria-label={t("settings.routesListRemoveNamed", { name: l.name })}>
                        {t("settings.routesListRemove")}
                      </Button>
                    </div>
                  </div>
                ))}
                <div>
                  <Button variant="ghost" onClick={addList} disabled={(routes.lists?.length ?? 0) >= MAX_LISTS}>
                    + {t("settings.routesListAdd")}
                  </Button>
                </div>
              </div>
            </Field>

            <Field label={t("settings.routesApps")} hint={t("settings.routesAppsHint")}>
              <div className="flex flex-col gap-2">
                {(catalog.data?.direct ?? []).map((d) => (
                  <label key={d.id} className="flex items-start gap-2 text-sm">
                    <input type="checkbox" className="check mt-0.5 shrink-0" checked={(routes.direct ?? []).includes(d.id)} onChange={() => toggleDirect(d.id)} />
                    <span>{locale === "en" ? d.name_en : d.name}</span>
                  </label>
                ))}
              </div>
            </Field>

            <Field label={t("settings.rules")}>
              <RulesEditor value={draft.rules} onChange={(f) => setDraft((d) => ({ ...d, rules: f(d.rules) }))} error={errors.sub_rules} badLine={badLine} targets={s.rule_targets} />
            </Field>

            <Disclosure title={t("settings.routesServers")} sub={t("settings.routesServersSub")} open={!!errors.sub_routes && apiErr?.fields.sub_routes === t("errors.api.routes_servers")}>
              <Field label={t("settings.routesAutoKind")} hint={t(servers.auto === "fallback" ? "settings.routesAutoFallbackHint" : "settings.routesAutoFastestHint")}>
                <Segmented
                  label={t("settings.routesAutoKind")}
                  value={servers.auto === "fallback" ? "fallback" : "fastest"}
                  onChange={(v) => setServers({ auto: v === "fallback" ? "fallback" : undefined })}
                  options={[
                    { value: "fastest", label: t("settings.routesAutoFastest") },
                    { value: "fallback", label: t("settings.routesAutoFallback") },
                  ]}
                />
              </Field>
              <Field label={t("settings.routesInterval")} htmlFor="s-interval" hint={t("settings.routesIntervalHint")}>
                <select id="s-interval" className="input max-w-[280px]" value={servers.interval ?? 0} onChange={(e) => setServers({ interval: Number(e.target.value) || undefined })}>
                  {[...new Set([0, 60, 120, 600, 1800, servers.interval ?? 0])].map((n) => (
                    <option key={n} value={n}>
                      {n ? t("settings.routesIntervalSec", { n }) : t("settings.routesIntervalDefault")}
                    </option>
                  ))}
                </select>
              </Field>
              <SwitchRow label={t("settings.routesCountries")} sub={t("settings.routesCountriesSub")} checked={!servers.no_countries} onChange={(v) => setServers({ no_countries: v ? undefined : true })} />
            </Disclosure>

            <Disclosure title={t("settings.routesTune")} sub={t("common.forExperts")}>
              <SwitchRow label={t("settings.routesQuic")} sub={t("settings.routesQuicSub")} checked={!!tune.block_quic} onChange={(v) => setTune({ block_quic: v || undefined })} />
              <SwitchRow label={t("settings.routesSniffer")} sub={t("settings.routesSnifferSub")} checked={!!tune.sniffer} onChange={(v) => setTune({ sniffer: v || undefined })} />
              <SwitchRow label={t("settings.routesRealIP")} sub={t("settings.routesRealIPSub")} checked={!!tune.real_ip} onChange={(v) => setTune({ real_ip: v || undefined })} />
            </Disclosure>

            <Disclosure title={t("settings.routesDns")} sub={t("common.forExperts")}>
              <p className="text-xs text-[var(--ink-500)]">{t("settings.routesDnsHint")}</p>
              <div className="my-3 flex flex-wrap gap-2" role="group" aria-label={t("settings.routesDnsPresets")}>
                {DNS_PRESETS.map((p) => (
                  <button key={p.id} type="button" className="chip-btn" onClick={() => setDns(p.dns)}>
                    {t(`settings.routesDnsPreset.${p.id}` as "settings.routesDnsPreset.panel")}
                  </button>
                ))}
              </div>
              <Field label={t("settings.routesDnsServers")} htmlFor="s-dns-ns" hint={t("settings.routesDnsServersHint")}>
                <textarea id="s-dns-ns" className="input mono min-h-[72px]" value={dnsText.nameserver} onChange={editDns("nameserver")} spellCheck={false} placeholder="https://1.1.1.1/dns-query#PROXY" />
              </Field>
              <Field label={t("settings.routesDnsProxy")} htmlFor="s-dns-proxy" hint={t("settings.routesDnsProxyHint")}>
                <textarea id="s-dns-proxy" className="input mono min-h-[56px]" value={dnsText.proxy} onChange={editDns("proxy")} spellCheck={false} placeholder="1.1.1.1" />
              </Field>
              <Field label={t("settings.routesDnsPolicy")} htmlFor="s-dns-policy" hint={t("settings.routesDnsPolicyHint")}>
                <textarea id="s-dns-policy" className="input mono min-h-[72px]" value={dnsText.policy} onChange={editDns("policy")} spellCheck={false} placeholder="+.cn: https://dns.alidns.com/dns-query" />
              </Field>
            </Disclosure>

            {error ? (
              <p className="mt-3 text-xs text-[var(--berry-600)]" role="alert">
                {error}
              </p>
            ) : null}
            <p className="mt-3 text-xs text-[var(--ink-500)]">{t("settings.routesNote")}</p>
            <FormActions dirty={dirty} saving={save.isPending} onReset={discard} />
          </form>
        </section>
    </WithPreview>
  );
}

// The profile a user with every connection gets, before anything is saved: the unsaved
// rules too.
function PreviewCard({ mode, routes, rules, template, bare }: { mode: Schemas["SettingsView"]["sub_routing"]; routes: Routes; rules?: string; template?: string; bare?: boolean }) {
  const preview = useMutation({
    mutationFn: () =>
      unwrap(
        api.POST("/api/v1/settings/routes/preview", {
          body: { sub_routing: mode, sub_routes: routes, ...(rules !== undefined ? { sub_rules: rules } : {}), ...(template?.trim() ? { sub_template: template } : {}) },
        }),
      ),
  });
  return (
    <section className={bare ? undefined : "card glass reveal"} style={{ "--i": 2 } as React.CSSProperties}>
      {bare ? (
        <p className="mb-3 text-[13px] text-[var(--ink-500)]">{t("settings.routesPreviewSub")}</p>
      ) : (
        <div className="card-head">
          <div>
            <h2 className="card-title">{t("settings.routesPreview")}</h2>
            <div className="card-sub">{t("settings.routesPreviewSub")}</div>
          </div>
        </div>
      )}
      <Button variant="ghost" loading={preview.isPending} onClick={() => preview.mutate()}>
        {preview.data ? t("settings.routesPreviewAgain") : t("settings.routesPreviewShow")}
      </Button>
      {preview.error ? (
        <p className="mt-3 text-xs text-[var(--berry-600)]" role="alert">
          {errorText(preview.error)}
        </p>
      ) : null}
      {preview.data ? <pre className="panel-soft mono mt-3 max-h-[70vh] overflow-auto p-3 text-[12px] leading-[1.45]">{preview.data.profile}</pre> : null}
    </section>
  );
}
