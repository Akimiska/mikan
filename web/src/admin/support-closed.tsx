import { ShieldAlert } from "lucide-react";
import { t } from "../i18n";

/** In its place where the part of the panel is closed to a support operator at any level (the admin's own credentials, mass mail). */
export function ClosedForSupport({ className }: { className?: string }) {
  return (
    <div className={className ? `banner info ${className}` : "banner info"} role="note">
      <ShieldAlert size={18} className="shrink-0" aria-hidden />
      <span>{t("support.closed")}</span>
    </div>
  );
}
