// The bot's settings as the Telegram page edits them, and their one save.
import { useMutation, useQueryClient } from "@tanstack/react-query";
import { api, unwrap, type Schemas } from "../../../api/client";
import { qk } from "../../../api/hooks";

export type View = Schemas["TelegramView"];

export type Config = Schemas["Config"];

export type MenuButton = Schemas["MenuButton"];

export type TextKey = keyof Schemas["Texts"];

export function usePatchTelegram() {
  const qc = useQueryClient();
  return useMutation({
    mutationFn: (body: Schemas["PatchTelegramInputBody"]) => unwrap(api.PATCH("/api/v1/telegram", { body })),
    onSuccess: (v) => {
      qc.setQueryData(qk.telegram, v);
      // Turning the bot on again brings the official bot addon back.
      void qc.invalidateQueries({ queryKey: qk.botSlot });
    },
  });
}

// Cards rise in turn, as on the other tabs.
export const rise = (i: number) => ({ className: "card glass reveal", style: { "--i": i } as React.CSSProperties });

// Menu buttons slide to their new place when moved, shown or hidden.
export const slide = { type: "spring", stiffness: 520, damping: 40 } as const;
