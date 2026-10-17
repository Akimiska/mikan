import * as SwitchPrimitive from "@radix-ui/react-switch";
import clsx from "clsx";
import { useId, type ReactNode } from "react";

/** An on/off switch. Kept out of ui.tsx: the subscription page has none and should not carry Radix. */
export function Switch({ checked, onChange, label, disabled, describedBy }: { checked: boolean; onChange: (v: boolean) => void; label: string; disabled?: boolean; describedBy?: string }) {
  return (
    <SwitchPrimitive.Root className="switch" checked={checked} onCheckedChange={onChange} aria-label={label} aria-describedby={describedBy} disabled={disabled}>
      <SwitchPrimitive.Thumb className="thumb" />
    </SwitchPrimitive.Root>
  );
}

/** A row with a switch: what it is (and what it does) on the left, the switch on the right. */
export function SwitchRow({ label, sub, warn, checked, onChange, disabled, className }: { label: string; sub?: ReactNode; warn?: boolean; checked: boolean; onChange: (v: boolean) => void; disabled?: boolean; className?: string }) {
  const id = useId();
  return (
    <div className={clsx("switch-row", className)}>
      <div className="min-w-0">
        <div className="switch-row-title">{label}</div>
        {sub ? (
          <div id={id} className={clsx("switch-row-sub", warn && "warn")}>
            {sub}
          </div>
        ) : null}
      </div>
      <Switch checked={checked} label={label} onChange={onChange} disabled={disabled} describedBy={sub ? id : undefined} />
    </div>
  );
}
