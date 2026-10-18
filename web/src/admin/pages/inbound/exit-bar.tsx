import * as Menu from "@radix-ui/react-dropdown-menu";
import { useMutation, useQueryClient } from "@tanstack/react-query";
import { Globe, X } from "lucide-react";
import { AnimatePresence, motion } from "motion/react";
import { api, ApiError, errorText, unwrap, type Inbound, type Schemas } from "../../../api/client";
import { qk } from "../../../api/hooks";
import { useToast } from "../../../components/toast";
import { Button } from "../../../components/ui";
import { t } from "../../../i18n";
import { nodeLabel } from "../../../lib/node-label";

type Node = Schemas["NodeInfo"];
type Exit = Schemas["SetExitsInputBody"];

/** Where the ticked inbounds leave to the internet, set for all of them at once. */
export function ExitBar({ chosen, nodes, clear }: { chosen: Inbound[]; nodes: Node[]; clear: () => void }) {
  const qc = useQueryClient();
  const toast = useToast();
  const n = chosen.length;
  const set = useMutation({
    mutationFn: (body: Exit) => unwrap(api.POST("/api/v1/inbounds/exit", { body })),
    onSuccess: (rows, body) => {
      const via = body.outbound === "node" ? nodes.find((x) => x.id === body.exit_node_id) : undefined;
      toast.ok(
        body.outbound === "node"
          ? t("inbounds.exitBulk.doneNode", { n: rows.length, node: via ? nodeLabel(via) : "" })
          : t(`inbounds.exitBulk.done.${body.outbound}`, { n: rows.length }),
      );
      clear();
    },
    onError: (e) => {
      // The API names the inbound that stopped the change.
      const name = e instanceof ApiError ? e.values.exit_node_id : undefined;
      toast.error(typeof name === "string" && name ? `${name}: ${e instanceof ApiError ? e.fields.exit_node_id : ""}` : errorText(e));
    },
    onSettled: () => void qc.invalidateQueries({ queryKey: qk.inbounds }),
  });
  const run = (body: Omit<Exit, "ids">) => set.mutate({ ids: chosen.map((i) => i.id), ...body });
  // An inbound cannot leave through the node it is on.
  const on = new Set(chosen.map((i) => i.node_id));
  const exits = nodes.filter((x) => !on.has(x.id));
  return (
    <AnimatePresence>
      {n > 0 ? (
        <motion.div
          className="bulk-bar glass-strong"
          role="region"
          aria-label={t("inbounds.exitBulk.label")}
          initial={{ opacity: 0, y: 24 }}
          animate={{ opacity: 1, y: 0 }}
          exit={{ opacity: 0, y: 24 }}
          transition={{ type: "spring", stiffness: 420, damping: 32 }}
        >
          <span className="num mr-2 font-semibold whitespace-nowrap">{t("inbounds.exitBulk.selected", { n })}</span>
          <Menu.Root>
            <Menu.Trigger asChild>
              <Button size="sm" loading={set.isPending} disabled={set.isPending}>
                <Globe size={16} aria-hidden />
                <span>{t("inbounds.outbound")}</span>
              </Button>
            </Menu.Trigger>
            <Menu.Portal>
              <Menu.Content className="menu glass-strong max-h-[60vh] overflow-y-auto" side="top" align="center" sideOffset={8}>
                <Menu.Item className="menu-item" onSelect={() => run({ outbound: "direct" })}>
                  {t("inbounds.outboundDirect")}
                </Menu.Item>
                <Menu.Item className="menu-item" onSelect={() => run({ outbound: "warp" })}>
                  {t("inbounds.exitBulk.warp")}
                </Menu.Item>
                {exits.length > 0 ? (
                  <>
                    <Menu.Separator className="menu-sep" />
                    <Menu.Label className="px-3 py-1 text-xs text-[var(--ink-500)]">{t("inbounds.outboundNode")}</Menu.Label>
                    {exits.map((x) => (
                      <Menu.Item key={x.id} className="menu-item" disabled={!x.enabled} onSelect={() => run({ outbound: "node", exit_node_id: x.id })}>
                        <span className="min-w-0 flex-1 truncate">{nodeLabel(x)}</span>
                        {x.enabled ? null : <span className="text-xs text-[var(--ink-500)]">{t("nodes.disabled")}</span>}
                      </Menu.Item>
                    ))}
                  </>
                ) : null}
              </Menu.Content>
            </Menu.Portal>
          </Menu.Root>
          <button type="button" className="icon-btn" aria-label={t("inbounds.exitBulk.clear")} disabled={set.isPending} onClick={clear}>
            <X size={16} />
          </button>
        </motion.div>
      ) : null}
    </AnimatePresence>
  );
}
