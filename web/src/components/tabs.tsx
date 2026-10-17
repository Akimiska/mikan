import type { LucideIcon } from "lucide-react";
import { useRef, type KeyboardEvent, type ReactNode } from "react";

type Tab<T extends string> = { id: T; label: string; icon?: LucideIcon };

/**
 * A page's few sections (up to four) as tabs: one bar as wide as the content, in equal
 * parts, so it never scrolls and never changes its width (WAI-ARIA tab list: arrows, Home
 * and End move between them). Pages with more sections take SectionNav. The caller keeps
 * the value, usually in the URL so a link opens the section; the panel's content goes in
 * children.
 */
export function Tabs<T extends string>({ id, tabs, value, onChange, label, children }: { id: string; tabs: Tab<T>[]; value: T; onChange: (v: T) => void; label: string; children: ReactNode }) {
  const refs = useRef<Partial<Record<T, HTMLButtonElement | null>>>({});
  const go = (next: T) => {
    onChange(next);
    refs.current[next]?.focus();
  };
  const onKey = (e: KeyboardEvent) => {
    const i = tabs.findIndex((x) => x.id === value);
    const step = e.key === "ArrowRight" ? 1 : e.key === "ArrowLeft" ? -1 : 0;
    if (e.key === "Home") go(tabs[0]!.id);
    else if (e.key === "End") go(tabs[tabs.length - 1]!.id);
    else if (step) go(tabs[(i + step + tabs.length) % tabs.length]!.id);
    else return;
    e.preventDefault();
  };
  return (
    <div className="flex flex-col gap-4">
      <div className="tabs" role="tablist" aria-label={label} onKeyDown={onKey}>
        {tabs.map(({ id: tab, label: text, icon: Icon }) => (
          <button
            key={tab}
            ref={(el) => {
              refs.current[tab] = el;
            }}
            type="button"
            role="tab"
            id={`${id}-tab-${tab}`}
            aria-selected={value === tab}
            aria-controls={`${id}-panel`}
            tabIndex={value === tab ? 0 : -1}
            title={text}
            onClick={() => onChange(tab)}
          >
            {Icon ? <Icon size={16} aria-hidden /> : null}
            <span>{text}</span>
          </button>
        ))}
      </div>
      <div id={`${id}-panel`} role="tabpanel" aria-labelledby={`${id}-tab-${value}`} className="flex min-w-0 flex-col gap-4">
        {children}
      </div>
    </div>
  );
}
