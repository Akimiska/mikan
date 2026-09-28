import { Check, TriangleAlert } from "lucide-react";
import { AnimatePresence, motion } from "motion/react";
import { createContext, useCallback, useContext, useMemo, useRef, useState, type ReactNode } from "react";

type Toast = { id: number; text: string; error?: boolean; action?: { label: string; run: () => void } };
type Api = {
  ok: (text: string, action?: Toast["action"]) => void;
  error: (text: string) => void;
};

const Ctx = createContext<Api | null>(null);

export function ToastProvider({ children }: { children: ReactNode }) {
  const [items, setItems] = useState<Toast[]>([]);
  const seq = useRef(0);

  const dismiss = useCallback((id: number) => setItems((xs) => xs.filter((x) => x.id !== id)), []);
  const push = useCallback(
    (t: Omit<Toast, "id">) => {
      const id = ++seq.current;
      setItems((xs) => [...xs.slice(-2), { ...t, id }]);
      window.setTimeout(() => dismiss(id), t.error ? 6000 : 4200);
    },
    [dismiss],
  );
  const api = useMemo<Api>(() => ({ ok: (text, action) => push({ text, action }), error: (text) => push({ text, error: true }) }), [push]);

  return (
    <Ctx.Provider value={api}>
      {children}
      <div className="toast-region" aria-live="polite">
        <AnimatePresence>
          {items.map((t) => (
            <motion.div
              key={t.id}
              className={t.error ? "toast err" : "toast"}
              role={t.error ? "alert" : "status"}
              initial={{ opacity: 0, y: -8, scale: 0.98 }}
              animate={{ opacity: 1, y: 0, scale: 1 }}
              exit={{ opacity: 0, y: -8 }}
              transition={{ type: "spring", stiffness: 500, damping: 34 }}
            >
              {t.error ? <TriangleAlert size={16} aria-hidden /> : <Check size={16} aria-hidden className="text-[var(--mikan-400)]" />}
              <span>{t.text}</span>
              {t.action ? (
                <button
                  type="button"
                  className="toast-act"
                  onClick={() => {
                    t.action!.run();
                    dismiss(t.id);
                  }}
                >
                  {t.action.label}
                </button>
              ) : null}
            </motion.div>
          ))}
        </AnimatePresence>
      </div>
    </Ctx.Provider>
  );
}

export function useToast(): Api {
  const v = useContext(Ctx);
  if (!v) throw new Error("useToast outside ToastProvider");
  return v;
}
