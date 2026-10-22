// The blocks of the page in their order: which are shown, what they are called, their own text and links.
import { ArrowDown, ArrowUp, ChevronDown, Link2, Plus, Trash2, Type } from "lucide-react";
import { useState } from "react";
import type { Schemas } from "../../../../api/client";
import { Switch } from "../../../../components/switch";
import { Button, Field } from "../../../../components/ui";
import { t, type Key } from "../../../../i18n";
import { safeHref } from "../../../../lib/url";
import { CUSTOM_TYPES, MINI_APP_ONLY, type BlockType } from "../../../../sub/page";
import { Card, MAX_CUSTOM, type Block, type Config, type Set } from "./parts";

/** A block's usual heading on the page, for the placeholder of its own. */
const USUAL: Partial<Record<BlockType, Key>> = {
  promo: "sub.promoTitle",
  shop: "sub.shop",
  devices: "sub.devicesTitle",
  apps: "sub.connect",
  guide: "sub.howTo",
  instructions: "sub.docs",
  link: "sub.link",
  reissue: "sub.reissueTitle",
  locations: "sub.locations",
  telegram: "sub.openTelegram",
  support: "sub.support",
};

const newId = (type: "text" | "links") => `${type}-${Math.random().toString(36).slice(2, 10) || "a"}`;

function blockName(b: Block): string {
  if (b.type === "text") return b.title || firstLine(b.text ?? "") || t("settings.page.blockNames.text");
  if (b.type === "links") return b.title || t("settings.page.blockNames.links");
  return t(`settings.page.blockNames.${b.type}`);
}

function firstLine(md: string): string {
  const line = md.split("\n").find((l) => l.trim()) ?? "";
  return line.replace(/^[#>*\-\s]+/, "").slice(0, 60);
}

export function BlocksSection({ config, set, errors, announce }: { config: Config; set: Set; errors: Record<string, string>; announce: (text: string) => void }) {
  const [open, setOpen] = useState<string | null>(null);
  const blocks = config.blocks;
  const custom = blocks.filter((b) => CUSTOM_TYPES.includes(b.type)).length;
  const setBlock = (i: number, patch: Partial<Block>) => set((c) => ({ ...c, blocks: c.blocks.map((b, j) => (j === i ? { ...b, ...patch } : b)) }));
  const move = (i: number, step: -1 | 1) => {
    const b = blocks[i]!;
    set((c) => {
      const next = [...c.blocks];
      next.splice(i, 1);
      next.splice(i + step, 0, b);
      return { ...c, blocks: next };
    });
    announce(t("settings.page.moved", { name: blockName(b), n: i + step + 1, total: blocks.length }));
    window.requestAnimationFrame(() => {
      const want = document.getElementById(`blk-${step < 0 ? "up" : "down"}-${b.id}`) as HTMLButtonElement | null;
      (want && !want.disabled ? want : document.getElementById(`blk-${step < 0 ? "down" : "up"}-${b.id}`))?.focus();
    });
  };
  const add = (type: "text" | "links") => {
    const b: Block = type === "text" ? { id: newId(type), type, on: true, title: "", text: "" } : { id: newId(type), type, on: true, title: "", links: [{ label: "", url: "https://", emoji: "" }] };
    set((c) => ({ ...c, blocks: [...c.blocks, b] }));
    setOpen(b.id);
  };
  // Errors of a block's fields by its index, at the block (open it so they show).
  const blockErrors = (i: number) => Object.entries(errors).filter(([k]) => k.startsWith(`config.blocks[${i}]`));
  return (
    <Card title={t("settings.page.blocks")} sub={t("settings.page.blocksSub")}>
      <ul className="flex flex-col gap-2">
        {blocks.map((b, i) => {
          const expanded = open === b.id || blockErrors(i).length > 0;
          const name = blockName(b);
          const panel = `blk-panel-${b.id}`;
          return (
            <li key={b.id} className="panel-soft p-3">
              <div className="flex items-center gap-3">
                <Switch checked={b.on} label={t("settings.page.showBlock", { name })} onChange={(on) => setBlock(i, { on })} />
                <div className={`min-w-0 flex-1 ${b.on ? "" : "opacity-60"}`}>
                  <div className="flex items-center gap-2">
                    <span className="truncate text-[13px] font-semibold">{name}</span>
                    {MINI_APP_ONLY.includes(b.type) ? <span className="tag shrink-0">Mini App</span> : null}
                  </div>
                  <div className="truncate text-xs text-[var(--ink-500)]">{t(`settings.page.blockDesc.${b.type}`)}</div>
                </div>
                <button type="button" id={`blk-up-${b.id}`} className="icon-btn" disabled={i === 0} onClick={() => move(i, -1)} aria-label={t("settings.page.moveUp", { name })}>
                  <ArrowUp size={16} />
                </button>
                <button type="button" id={`blk-down-${b.id}`} className="icon-btn" disabled={i === blocks.length - 1} onClick={() => move(i, 1)} aria-label={t("settings.page.moveDown", { name })}>
                  <ArrowDown size={16} />
                </button>
                {b.type !== "status" ? (
                  <button type="button" className="icon-btn" aria-expanded={expanded} aria-controls={panel} onClick={() => setOpen(open === b.id ? null : b.id)} aria-label={t("settings.page.blockSetup", { name })}>
                    <ChevronDown size={16} className={`transition-transform ${expanded ? "rotate-180" : ""}`} />
                  </button>
                ) : (
                  <span className="w-9" aria-hidden />
                )}
              </div>
              {expanded && b.type !== "status" ? (
                <div id={panel} className="mt-3 border-t border-[var(--hairline)] pt-3">
                  <BlockSetup block={b} index={i} setBlock={(patch) => setBlock(i, patch)} errors={errors} />
                  {CUSTOM_TYPES.includes(b.type) ? (
                    <Button
                      size="sm"
                      variant="danger"
                      onClick={() => {
                        set((c) => ({ ...c, blocks: c.blocks.filter((x) => x.id !== b.id) }));
                        announce(t("settings.page.blockRemoved", { name }));
                      }}
                    >
                      <Trash2 size={14} aria-hidden /> {t("settings.page.blockRemove")}
                    </Button>
                  ) : null}
                </div>
              ) : null}
            </li>
          );
        })}
      </ul>
      <div className="mt-3 flex flex-wrap items-center gap-2">
        <button type="button" className="chip-btn inline-flex items-center gap-1" onClick={() => add("text")} disabled={custom >= MAX_CUSTOM}>
          <Type size={14} aria-hidden /> {t("settings.page.addText")}
        </button>
        <button type="button" className="chip-btn inline-flex items-center gap-1" onClick={() => add("links")} disabled={custom >= MAX_CUSTOM}>
          <Link2 size={14} aria-hidden /> {t("settings.page.addLinks")}
        </button>
        <span className="text-xs text-[var(--ink-500)]">{custom >= MAX_CUSTOM ? t("errors.api.too_many_blocks", { value: MAX_CUSTOM }) : t("settings.page.customHint")}</span>
      </div>
    </Card>
  );
}

function BlockSetup({ block: b, index: i, setBlock, errors }: { block: Block; index: number; setBlock: (patch: Partial<Block>) => void; errors: Record<string, string> }) {
  const at = (field: string) => errors[`config.blocks[${i}].${field}`];
  const usual = USUAL[b.type];
  const titleLabel = b.type === "telegram" || b.type === "support" ? t("settings.page.buttonText") : t("settings.page.blockTitle");
  const links = b.links ?? [];
  const setLink = (j: number, patch: Partial<Schemas["PageLink"]>) => setBlock({ links: links.map((l, k) => (k === j ? { ...l, ...patch } : l)) });
  return (
    <>
      <Field label={titleLabel} htmlFor={`blk-title-${b.id}`} hint={usual ? t("settings.page.blockTitleHint") : t("settings.page.blockTitleNone")} error={at("title")}>
        <input id={`blk-title-${b.id}`} className="input" value={b.title} maxLength={60} placeholder={usual ? t(usual) : ""} onChange={(e) => setBlock({ title: e.target.value })} aria-invalid={!!at("title")} />
      </Field>
      {b.type === "text" ? (
        <Field label={t("settings.page.text")} htmlFor={`blk-text-${b.id}`} hint={t("settings.page.markdownHint")} error={at("text")}>
          <textarea id={`blk-text-${b.id}`} className="input mono" rows={6} value={b.text ?? ""} maxLength={8000} onChange={(e) => setBlock({ text: e.target.value })} aria-invalid={!!at("text")} />
        </Field>
      ) : null}
      {b.type === "links" ? (
        <div className="field">
          <span className="lbl">{t("settings.page.links")}</span>
          <ul className="flex flex-col gap-2">
            {links.map((l, j) => {
              const urlErr = at(`links[${j}].url`) ?? (l.url && l.url !== "https://" && !safeHref(l.url, { tg: true }) ? t("errors.api.link_invalid") : undefined);
              const labelErr = at(`links[${j}].label`);
              return (
                <li key={j} className="grid grid-cols-[56px_minmax(0,1fr)_auto] gap-2">
                  <input className="input text-center" value={l.emoji ?? ""} maxLength={16} autoComplete="off" onChange={(e) => setLink(j, { emoji: e.target.value.trim() })} aria-label={t("settings.page.emoji")} />
                  <input className="input" value={l.label} maxLength={40} placeholder={t("settings.page.linkLabel")} onChange={(e) => setLink(j, { label: e.target.value })} aria-label={t("settings.page.linkLabel")} aria-invalid={!!labelErr} />
                  <button type="button" className="icon-btn self-center" onClick={() => setBlock({ links: links.filter((_, k) => k !== j) })} aria-label={t("settings.page.linkRemove", { name: l.label || String(j + 1) })}>
                    <Trash2 size={16} />
                  </button>
                  <input className="input mono col-span-2 col-start-2" value={l.url} maxLength={500} placeholder="https://… / tg://…" onChange={(e) => setLink(j, { url: e.target.value.trim() })} aria-label={t("settings.page.linkURL")} aria-invalid={!!urlErr} spellCheck={false} />
                  {urlErr || labelErr ? (
                    <span className="col-span-2 col-start-2 text-xs text-[var(--berry-600)]" role="alert">
                      {labelErr ?? urlErr}
                    </span>
                  ) : null}
                </li>
              );
            })}
          </ul>
          <div>
            <button type="button" className="chip-btn inline-flex items-center gap-1" disabled={links.length >= 8} onClick={() => setBlock({ links: [...links, { label: "", url: "https://", emoji: "" }] })}>
              <Plus size={14} aria-hidden /> {t("settings.page.addLink")}
            </button>
          </div>
        </div>
      ) : null}
    </>
  );
}
