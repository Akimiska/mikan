import clsx from "clsx";
import { Check } from "lucide-react";
import { useEffect, useState, type FormEvent } from "react";
import { ApiError, errorText } from "../../api/client";
import { userActions, useTariffs, useUserMutation } from "../../api/hooks";
import { Drawer } from "../../components/overlay";
import { useToast } from "../../components/toast";
import { Button, Field, Skeleton } from "../../components/ui";
import { tariffSummary } from "./tariffs";

export function CreateUserDrawer({ open, onOpenChange, onCreated }: { open: boolean; onOpenChange: (v: boolean) => void; onCreated: (id: number) => void }) {
  const tariffs = useTariffs();
  const toast = useToast();
  const create = useUserMutation(userActions.create);
  const [name, setName] = useState("");
  const [contact, setContact] = useState("");
  const [tariffId, setTariffId] = useState<number>();
  const [errors, setErrors] = useState<Record<string, string>>({});

  useEffect(() => {
    if (!open) return;
    setName("");
    setContact("");
    setErrors({});
    create.reset();
    // Reset only when the drawer opens; `create` changes identity on every render.
  }, [open]);

  useEffect(() => {
    if (tariffId === undefined && tariffs.data?.length) setTariffId(tariffs.data[Math.min(1, tariffs.data.length - 1)]!.id);
  }, [tariffs.data, tariffId]);

  const submit = (e: FormEvent) => {
    e.preventDefault();
    if (!name.trim()) {
      setErrors({ name: "Укажите имя — так пользователя будет проще найти" });
      return;
    }
    if (!tariffId) {
      setErrors({ tariff_id: "Выберите тариф" });
      return;
    }
    create.mutate(
      { name: name.trim(), contact: contact.trim() || undefined, tariff_id: tariffId },
      {
        onSuccess: async (u) => {
          try {
            await navigator.clipboard.writeText(u.sub_url);
            toast.ok(`${u.name} создан — ссылка скопирована`);
          } catch {
            toast.ok(`${u.name} создан`);
          }
          onCreated(u.id);
        },
        onError: (err) => {
          if (err instanceof ApiError && Object.keys(err.fields).length) setErrors(err.fields);
          else toast.error(errorText(err));
        },
      },
    );
  };

  return (
    <Drawer
      open={open}
      onOpenChange={onOpenChange}
      title="Новый пользователь"
      meta="Ссылка на подписку появится сразу после создания"
      footer={
        <>
          <Button variant="ghost" onClick={() => onOpenChange(false)}>
            Отмена
          </Button>
          <Button variant="primary" type="submit" form="create-user" loading={create.isPending}>
            <Check size={18} aria-hidden /> Создать и скопировать ссылку
          </Button>
        </>
      }
    >
      <form id="create-user" onSubmit={submit} className="pt-5" noValidate>
        <Field label="Имя или название" htmlFor="nu-name" hint="Видно только вам и на странице подписки клиента." error={errors.name}>
          <input id="nu-name" className="input" placeholder="Например, Анна или ООО «Север»" value={name} onChange={(e) => setName(e.target.value)} aria-invalid={!!errors.name} maxLength={100} autoFocus autoComplete="off" />
        </Field>
        <Field label="Контакт" htmlFor="nu-contact" hint="Необязательно: @telegram или телефон, чтобы быстро найти.">
          <input id="nu-contact" className="input" placeholder="@telegram" value={contact} onChange={(e) => setContact(e.target.value)} maxLength={100} autoComplete="off" />
        </Field>
        <Field label="Тариф" error={errors.tariff_id} hint="Срок, объём и лимит устройств подставятся из тарифа — их можно поменять в карточке.">
          {tariffs.isPending ? (
            <div className="grid grid-cols-2 gap-2">
              <Skeleton style={{ height: 76, borderRadius: 16 }} />
              <Skeleton style={{ height: 76, borderRadius: 16 }} />
            </div>
          ) : (
            <div className="grid grid-cols-1 gap-2 sm:grid-cols-2" role="radiogroup" aria-label="Тариф">
              {(tariffs.data ?? []).map((t) => (
                <button key={t.id} type="button" role="radio" aria-checked={tariffId === t.id} className={clsx("opt")} onClick={() => setTariffId(t.id)}>
                  <span className="font-semibold">{t.name}</span>
                  <span className="text-xs text-[var(--ink-500)]">{tariffSummary(t)}</span>
                  {t.price_label ? <span className="mt-1 text-[13px] font-medium text-[var(--mikan-700)]">{t.price_label}</span> : null}
                </button>
              ))}
            </div>
          )}
        </Field>
      </form>
    </Drawer>
  );
}
