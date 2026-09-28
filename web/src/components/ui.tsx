import * as SwitchPrimitive from "@radix-ui/react-switch";
import clsx from "clsx";
import { LoaderCircle, SearchX, TriangleAlert, UserRound } from "lucide-react";
import type { ButtonHTMLAttributes, CSSProperties, ReactNode } from "react";
import { useMemo } from "react";
import { renderSVG } from "uqr";
import type { UserState } from "../api/client";

type Variant = "primary" | "glass" | "ghost" | "danger" | "danger-solid";

export function Button({
  variant = "glass",
  size,
  loading,
  block,
  className,
  children,
  disabled,
  ...rest
}: ButtonHTMLAttributes<HTMLButtonElement> & { variant?: Variant; size?: "sm"; loading?: boolean; block?: boolean }) {
  const v = variant === "danger" ? "btn-ghost danger" : variant === "danger-solid" ? "btn-danger-solid" : `btn-${variant}`;
  return (
    <button
      type="button"
      className={clsx("btn", v, size === "sm" && "btn-sm", block && "btn-block", className)}
      disabled={disabled || loading}
      aria-busy={loading || undefined}
      {...rest}
    >
      {loading ? <LoaderCircle size={16} className="spin" aria-hidden /> : null}
      {children}
    </button>
  );
}

export const stateInfo: Record<UserState, { tone: "ok" | "warn" | "bad" | "off"; label: string }> = {
  active: { tone: "ok", label: "активен" },
  expiring: { tone: "warn", label: "истекает" },
  limited: { tone: "bad", label: "лимит исчерпан" },
  expired: { tone: "bad", label: "истёк" },
  disabled: { tone: "off", label: "отключён" },
};

export function Pill({ tone, children }: { tone: "ok" | "warn" | "bad" | "off"; children: ReactNode }) {
  return <span className={clsx("pill", tone)}>{children}</span>;
}

export function StatePill({ state }: { state: UserState }) {
  const s = stateInfo[state];
  return <Pill tone={s.tone}>{s.label}</Pill>;
}

const AVATAR_COLORS = ["#FBE3D2", "#DCEBFA", "#DDF1E6", "#F4E6C9", "#EADFF3", "#F6DDE0", "#DDEFF1"];

export function Avatar({ name, seed, size }: { name: string; seed: number; size?: "sm" | "lg" }) {
  const initials = name
    .replace(/[«»"@()]/g, "")
    .trim()
    .split(/\s+/)
    .map((w) => w[0] ?? "")
    .slice(0, 2)
    .join("")
    .toUpperCase();
  const style = { "--av": AVATAR_COLORS[seed % AVATAR_COLORS.length] } as CSSProperties;
  return (
    <span className={clsx("avatar", size)} style={style} aria-hidden>
      {initials || "?"}
    </span>
  );
}

export function Switch({ checked, onChange, label, disabled }: { checked: boolean; onChange: (v: boolean) => void; label: string; disabled?: boolean }) {
  return (
    <SwitchPrimitive.Root className="switch" checked={checked} onCheckedChange={onChange} aria-label={label} disabled={disabled}>
      <SwitchPrimitive.Thumb className="thumb" />
    </SwitchPrimitive.Root>
  );
}

export function Segmented<T extends string>({ value, options, onChange, label }: { value: T; options: { value: T; label: string }[]; onChange: (v: T) => void; label: string }) {
  return (
    <div className="seg" role="group" aria-label={label}>
      {options.map((o) => (
        <button key={o.value} type="button" aria-pressed={o.value === value} onClick={() => onChange(o.value)}>
          {o.label}
        </button>
      ))}
    </div>
  );
}

export function Bar({ pct, className }: { pct: number; className?: string }) {
  return (
    <div className={clsx("bar", className)} role="presentation">
      <i style={{ width: `${Math.max(0, Math.min(100, pct))}%` }} />
    </div>
  );
}

export function Ring({ pct, label, sub, size = 112 }: { pct: number; label: ReactNode; sub: ReactNode; size?: number }) {
  const r = size / 2 - 8;
  const c = 2 * Math.PI * r;
  const tone = pct >= 100 ? "bad" : pct >= 85 ? "warn" : "";
  return (
    <div className={clsx("gauge", tone)} style={{ width: size, height: size }}>
      <svg width={size} height={size} viewBox={`0 0 ${size} ${size}`} aria-hidden>
        <circle className="track" cx={size / 2} cy={size / 2} r={r} fill="none" strokeWidth={10} />
        <circle
          className="val"
          cx={size / 2}
          cy={size / 2}
          r={r}
          fill="none"
          strokeWidth={10}
          strokeLinecap="round"
          strokeDasharray={c}
          strokeDashoffset={c * (1 - Math.min(Math.max(pct, 0), 100) / 100)}
        />
      </svg>
      <div className="gauge-label">
        <div>
          <b className="num">{label}</b>
          <span>{sub}</span>
        </div>
      </div>
    </div>
  );
}

export function QR({ value, size = 136, label = "QR-код подписки" }: { value: string; size?: number; label?: string }) {
  const svg = useMemo(() => renderSVG(value, { ecc: "M", border: 1, blackColor: "#161A24", whiteColor: "#FFFFFF" }), [value]);
  return <div className="qr" style={{ width: size, height: size }} role="img" aria-label={label} dangerouslySetInnerHTML={{ __html: svg }} />;
}

export function Skeleton({ className, style }: { className?: string; style?: CSSProperties }) {
  return <span className={clsx("sk", className)} style={style} aria-hidden />;
}

export function Spinner({ size = 16 }: { size?: number }) {
  return <LoaderCircle size={size} className="spin" aria-label="Загрузка" />;
}

export function EmptyState({ title, text, children, search }: { title: string; text: ReactNode; children?: ReactNode; search?: boolean }) {
  return (
    <div className="state-box">
      <div className="state-mark">{search ? <SearchX size={22} /> : <UserRound size={22} />}</div>
      <h2>{title}</h2>
      <p>{text}</p>
      {children ? <div className="mt-2 flex flex-wrap justify-center gap-2">{children}</div> : null}
    </div>
  );
}

export function ErrorState({ title = "Не удалось загрузить", text, onRetry }: { title?: string; text: string; onRetry?: () => void }) {
  return (
    <div className="state-box" role="alert">
      <div className="state-mark err">
        <TriangleAlert size={22} />
      </div>
      <h2>{title}</h2>
      <p>{text}</p>
      {onRetry ? (
        <Button variant="primary" onClick={onRetry}>
          Повторить
        </Button>
      ) : null}
    </div>
  );
}

export function Field({ label, htmlFor, hint, error, children }: { label: string; htmlFor?: string; hint?: ReactNode; error?: string; children: ReactNode }) {
  return (
    <div className="field">
      {htmlFor ? <label htmlFor={htmlFor}>{label}</label> : <span className="lbl">{label}</span>}
      {children}
      {error ? (
        <span className="err" role="alert">
          {error}
        </span>
      ) : hint ? (
        <span className="hint">{hint}</span>
      ) : null}
    </div>
  );
}

export function PageHeader({ title, sub, actions }: { title: string; sub?: ReactNode; actions?: ReactNode }) {
  return (
    <header className="topbar">
      <div className="min-w-0">
        <h1 className="page-title">{title}</h1>
        {sub ? <p className="page-sub">{sub}</p> : null}
      </div>
      {actions ? <div className="top-actions">{actions}</div> : null}
    </header>
  );
}
