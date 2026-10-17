// Where a user stands in the admin's list: the folder, whether the row is hidden, and where
// the user came from. None of it changes what the user may do.
import * as Menu from "@radix-ui/react-dropdown-menu";
import { errorText, type User } from "../../../api/client";
import { userActions, useFolders, useUserMutation } from "../../../api/hooks";
import { SwitchRow } from "../../../components/switch";
import { useToast } from "../../../components/toast";
import { Button } from "../../../components/ui";
import { t } from "../../../i18n";
import { FolderBadge, FolderMenuItems } from "../user-folders";
import { Section } from "./section";

export function OrganiseSection({ u }: { u: User }) {
  const folders = useFolders();
  const toast = useToast();
  const update = useUserMutation(userActions.update);
  const list = folders.data ?? [];
  const current = list.find((f) => f.id === u.folder_id);
  const fail = (e: unknown) => toast.error(errorText(e));
  const pick = (id: number | null) =>
    update.mutate(
      // 0 takes the user out of the folder.
      { id: u.id, body: { folder_id: id ?? 0 } },
      { onSuccess: () => toast.ok(id === null ? t("userDrawer.folderCleared") : t("userDrawer.folderSet")), onError: fail },
    );
  const setHidden = (hidden: boolean) =>
    update.mutate({ id: u.id, body: { hidden } }, { onSuccess: () => toast.ok(hidden ? t("userDrawer.hiddenToast", { name: u.name }) : t("userDrawer.shownToast", { name: u.name })), onError: fail });
  return (
    <Section title={t("userDrawer.organise")}>
      <div className="panel-soft flex items-center justify-between gap-3 p-4">
        <div className="min-w-0">
          <div className="text-xs text-[var(--ink-500)]">{t("userDrawer.folder")}</div>
          <div className="mt-1 text-[13px]">{current ? <FolderBadge folder={current} /> : <span className="text-[var(--ink-600)]">{t("userDrawer.noFolder")}</span>}</div>
        </div>
        {list.length > 0 ? (
          <Menu.Root>
            <Menu.Trigger asChild>
              <Button variant="ghost" size="sm" loading={update.isPending && update.variables?.body.folder_id !== undefined}>
                {t("userDrawer.change")}
              </Button>
            </Menu.Trigger>
            <Menu.Portal>
              <Menu.Content className="menu glass-strong" align="end" sideOffset={6}>
                <FolderMenuItems folders={list} current={current ? current.id : null} onPick={pick} />
              </Menu.Content>
            </Menu.Portal>
          </Menu.Root>
        ) : null}
      </div>
      {list.length === 0 ? <p className="mt-2 text-xs text-[var(--ink-500)]">{t("userDrawer.noFoldersYet")}</p> : null}
      <SwitchRow className="mt-1" label={t("userDrawer.hide")} sub={t("userDrawer.hideHint")} checked={u.hidden} onChange={setHidden} disabled={update.isPending} />
      <p className="mt-3 text-xs text-[var(--ink-500)]">
        {t("userDrawer.source")}: <span className="text-[var(--ink-700)]">{t(`users.sourceHints.${u.source}`)}</span>
      </p>
    </Section>
  );
}
