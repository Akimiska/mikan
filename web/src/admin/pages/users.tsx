import { useNavigate, useSearch } from "@tanstack/react-router";
import clsx from "clsx";
import { CalendarPlus, ChevronRight, Plus, Power, RotateCcw, Search, Trash2, X } from "lucide-react";
import { AnimatePresence, motion } from "motion/react";
import { useEffect, useMemo, useState } from "react";
import { errorText, type Tariff, type User } from "../../api/client";
import { userActions, useTariffs, useUserMutation, useUsers } from "../../api/hooks";
import { Confirm } from "../../components/overlay";
import { useToast } from "../../components/toast";
import { Avatar, Bar, Button, EmptyState, ErrorState, PageHeader, Skeleton, StatePill } from "../../components/ui";
import { bytes, dateShort, expiryText, num, plural } from "../../lib/format";
import { CreateUserDrawer } from "./user-create";
import { UserDrawer } from "./user-drawer";

export type UsersSearch = { state: "all" | User["state"]; q: string; user?: number; create?: true };

const FILTERS: { id: UsersSearch["state"]; label: string; count: keyof NonNullable<ReturnType<typeof useUsers>["data"]>["counts"] }[] = [
  { id: "all", label: "Все", count: "all" },
  { id: "active", label: "Активные", count: "active" },
  { id: "expiring", label: "Истекают", count: "expiring" },
  { id: "limited", label: "Исчерпан лимит", count: "limited" },
  { id: "expired", label: "Истекли", count: "expired" },
  { id: "disabled", label: "Отключены", count: "disabled" },
];

export function UsersPage() {
  const search = useSearch({ from: "/_app/users" });
  const navigate = useNavigate({ from: "/users" });
  const [q, setQ] = useState(search.q);
  const users = useUsers({ state: search.state, q: search.q });
  const tariffs = useTariffs();
  const [selected, setSelected] = useState<Set<number>>(new Set());

  useEffect(() => {
    const t = window.setTimeout(() => {
      if (q !== search.q) void navigate({ search: (s) => ({ ...s, q }), replace: true });
    }, 250);
    return () => window.clearTimeout(t);
  }, [q, search.q, navigate]);

  const tariffById = useMemo(() => new Map((tariffs.data ?? []).map((t) => [t.id, t])), [tariffs.data]);
  const items = users.data?.items ?? [];
  const counts = users.data?.counts;
  const openUser = (id?: number) => void navigate({ search: (s) => ({ ...s, user: id, create: undefined }) });
  const toggle = (id: number) =>
    setSelected((s) => {
      const n = new Set(s);
      if (n.has(id)) n.delete(id);
      else n.add(id);
      return n;
    });
  const allSelected = items.length > 0 && items.every((u) => selected.has(u.id));

  let body: React.ReactNode;
  if (users.isPending) {
    body = <TableSkeleton />;
  } else if (users.isError) {
    body = <ErrorState title="Не удалось загрузить список" text={errorText(users.error)} onRetry={() => void users.refetch()} />;
  } else if (counts && counts.all === 0) {
    body = (
      <EmptyState title="Пока ни одного пользователя" text="Создайте первого — ссылка на подписку и QR-код появятся сразу.">
        <Button variant="primary" onClick={() => void navigate({ search: (s) => ({ ...s, create: true }) })}>
          <Plus size={18} aria-hidden /> Новый пользователь
        </Button>
      </EmptyState>
    );
  } else if (items.length === 0) {
    body = (
      <EmptyState search title="Никого не нашли" text={search.q ? `По запросу «${search.q}» совпадений нет. Проверьте написание или сбросьте фильтр.` : "В этой группе сейчас никого нет."}>
        <Button
          onClick={() => {
            setQ("");
            void navigate({ search: { state: "all", q: "" } });
          }}
        >
          Сбросить фильтр
        </Button>
      </EmptyState>
    );
  } else {
    body = (
      <>
        <table className="utable">
          <thead>
            <tr>
              <th className="w-10">
                <input
                  type="checkbox"
                  className="check"
                  checked={allSelected}
                  aria-label="Выбрать всех на странице"
                  onChange={() => setSelected(allSelected ? new Set() : new Set(items.map((u) => u.id)))}
                />
              </th>
              <th>Пользователь</th>
              <th>Тариф</th>
              <th>Трафик</th>
              <th>Срок</th>
              <th>Устройства</th>
              <th>Статус</th>
              <th className="w-12">
                <span className="sr-only">Открыть</span>
              </th>
            </tr>
          </thead>
          <tbody>
            {items.map((u) => (
              <UserRow key={u.id} u={u} tariff={u.tariff_id ? tariffById.get(u.tariff_id) : undefined} selected={selected.has(u.id)} onToggle={() => toggle(u.id)} onOpen={() => openUser(u.id)} />
            ))}
          </tbody>
        </table>
        <div className="flex flex-col gap-2 md:hidden">
          {items.map((u) => (
            <UserCard key={u.id} u={u} tariff={u.tariff_id ? tariffById.get(u.tariff_id) : undefined} onOpen={() => openUser(u.id)} />
          ))}
        </div>
        <div className="flex items-center justify-between gap-3 border-t border-[var(--hairline)] p-3 text-[13px] text-[var(--ink-500)]">
          <span>
            Показано {num(items.length)} из {num(users.data?.total ?? 0)}
          </span>
          <span className="max-md:hidden">Строка открывает карточку · пробел выбирает</span>
        </div>
      </>
    );
  }

  return (
    <>
      <PageHeader
        title="Пользователи"
        sub={counts ? `${num(counts.all)} ${plural(counts.all, "человек", "человека", "человек")} · ${num(counts.active)} с активной подпиской` : "…"}
        actions={
          <Button variant="primary" onClick={() => void navigate({ search: (s) => ({ ...s, create: true, user: undefined }) })}>
            <Plus size={18} aria-hidden />
            <span className="max-[760px]:hidden">Новый пользователь</span>
          </Button>
        }
      />
      <div className="reveal flex flex-col items-stretch justify-between gap-3 lg:flex-row lg:items-center">
        <div className="-mx-3 flex gap-2 overflow-x-auto px-3 pb-1 lg:mx-0 lg:flex-wrap lg:overflow-visible lg:px-0 lg:pb-0" role="group" aria-label="Фильтр">
          {FILTERS.map((f) => (
            <button
              key={f.id}
              type="button"
              className="chip shrink-0"
              aria-pressed={search.state === f.id}
              onClick={() => {
                setSelected(new Set());
                void navigate({ search: (s) => ({ ...s, state: f.id }) });
              }}
            >
              {f.label}
              {counts ? <span className="chip-count num">{num(counts[f.count])}</span> : null}
            </button>
          ))}
        </div>
        <label className="search-field">
          <Search size={16} aria-hidden />
          <input type="search" placeholder="Имя, контакт, тег или заметка" value={q} onChange={(e) => setQ(e.target.value)} aria-label="Поиск пользователей" />
        </label>
      </div>
      <section className="card glass reveal overflow-hidden !p-2 md:!pb-0" style={{ "--i": 1 } as React.CSSProperties} aria-label="Список пользователей" aria-busy={users.isFetching}>
        {body}
      </section>

      <BulkBar selected={selected} clear={() => setSelected(new Set())} />
      <CreateUserDrawer
        open={!!search.create}
        onOpenChange={(v) => void navigate({ search: (s) => ({ ...s, create: v ? true : undefined }) })}
        onCreated={(id) => void navigate({ search: (s) => ({ ...s, create: undefined, user: id }) })}
      />
      <UserDrawer id={search.user} onClose={() => openUser(undefined)} />
    </>
  );
}

function Usage({ u }: { u: User }) {
  const used = u.used_up + u.used_down;
  if (u.traffic_limit == null) {
    return (
      <div className="usage inf min-w-[160px]">
        <div className="usage-txt">
          <span>
            <b className="num">{bytes(used)}</b>
          </span>
          <span>без лимита</span>
        </div>
        <Bar pct={100} />
      </div>
    );
  }
  const pct = u.traffic_limit > 0 ? (used / u.traffic_limit) * 100 : 100;
  return (
    <div className={clsx("usage min-w-[160px]", pct >= 100 ? "bad" : pct >= 85 && "warn")}>
      <div className="usage-txt">
        <span>
          <b className="num">{bytes(used)}</b> из {bytes(u.traffic_limit)}
        </span>
        <span className="num">{Math.min(100, Math.round(pct))}%</span>
      </div>
      <Bar pct={pct} />
    </div>
  );
}

function Expiry({ u }: { u: User }) {
  const e = expiryText(u.expires_at);
  return (
    <>
      <div className="font-medium">{u.expires_at ? `до ${dateShort(u.expires_at)}` : "бессрочно"}</div>
      {u.expires_at ? <div className={clsx("exp-days", e.tone)}>{e.text}</div> : null}
    </>
  );
}

function UserRow({ u, tariff, selected, onToggle, onOpen }: { u: User; tariff?: Tariff; selected: boolean; onToggle: () => void; onOpen: () => void }) {
  const devices = u.online_ips.length;
  return (
    <tr
      className={selected ? "sel" : undefined}
      tabIndex={0}
      onClick={onOpen}
      onKeyDown={(e) => {
        if (e.key === "Enter") onOpen();
        if (e.key === " ") {
          e.preventDefault();
          onToggle();
        }
      }}
    >
      <td onClick={(e) => e.stopPropagation()}>
        <input type="checkbox" className="check" checked={selected} onChange={onToggle} aria-label={`Выбрать: ${u.name}`} />
      </td>
      <td>
        <div className="flex min-w-[200px] items-center gap-3">
          <Avatar name={u.name} seed={u.id} />
          <div className="min-w-0">
            <div className="truncate font-medium">
              {u.name}
              {u.online ? <span className="online-dot" title="Сейчас онлайн" /> : null}
            </div>
            <div className="mt-0.5 flex flex-wrap items-center gap-1.5 text-xs text-[var(--ink-500)]">
              {u.contact ? <span>{u.contact}</span> : null}
              {u.tags.map((t) => (
                <span key={t} className="tag">
                  {t}
                </span>
              ))}
            </div>
          </div>
        </div>
      </td>
      <td>
        <div className="font-medium">{tariff?.name ?? "—"}</div>
        <div className="mt-0.5 text-xs text-[var(--ink-500)]">{u.traffic_limit != null ? `${bytes(u.traffic_limit)}` : "без лимита"}</div>
      </td>
      <td>
        <Usage u={u} />
      </td>
      <td>
        <Expiry u={u} />
      </td>
      <td>
        <span className={clsx("num font-medium", u.device_limit != null && devices >= u.device_limit && devices > 0 && "text-[var(--honey-600)]")}>
          {devices}
          <small className="font-normal text-[var(--ink-500)]"> из {u.device_limit ?? "∞"}</small>
        </span>
      </td>
      <td>
        <StatePill state={u.state} />
      </td>
      <td className="text-right">
        <span className="icon-btn" aria-hidden>
          <ChevronRight size={18} />
        </span>
      </td>
    </tr>
  );
}

function UserCard({ u, tariff, onOpen }: { u: User; tariff?: Tariff; onOpen: () => void }) {
  const e = expiryText(u.expires_at);
  return (
    <button type="button" onClick={onOpen} className="panel-soft grid w-full grid-cols-[auto_minmax(0,1fr)_auto] items-center gap-3 p-3 text-left">
      <Avatar name={u.name} seed={u.id} />
      <div className="min-w-0">
        <div className="truncate font-medium">
          {u.name}
          {u.online ? <span className="online-dot" /> : null}
        </div>
        <div className="truncate text-xs text-[var(--ink-500)]">
          {tariff?.name ?? "без тарифа"} · {e.text}
        </div>
      </div>
      <StatePill state={u.state} />
      <div className="col-span-3">
        <Usage u={u} />
      </div>
    </button>
  );
}

function TableSkeleton() {
  return (
    <div aria-busy aria-label="Загрузка списка">
      {Array.from({ length: 8 }, (_, i) => (
        <div key={i} className="grid grid-cols-[40px_2fr_1fr_1.4fr_1fr_0.8fr_0.9fr] items-center gap-3 border-t border-[var(--hairline)] px-3 py-4 first:border-t-0">
          <Skeleton style={{ width: 18, height: 18, borderRadius: 6 }} />
          <div className="flex items-center gap-3">
            <Skeleton style={{ width: 36, height: 36, borderRadius: "50%" }} />
            <div className="flex flex-1 flex-col gap-2">
              <Skeleton style={{ width: "70%" }} />
              <Skeleton style={{ width: "40%" }} />
            </div>
          </div>
          <Skeleton style={{ width: "60%" }} />
          <Skeleton />
          <Skeleton style={{ width: "50%" }} />
          <Skeleton style={{ width: "40%" }} />
          <Skeleton style={{ width: "70%", height: 24, borderRadius: 12 }} />
        </div>
      ))}
    </div>
  );
}

function BulkBar({ selected, clear }: { selected: Set<number>; clear: () => void }) {
  const toast = useToast();
  const bulk = useUserMutation(userActions.bulk);
  const [confirmDelete, setConfirmDelete] = useState(false);
  const ids = [...selected];
  const n = ids.length;
  const run = (action: "extend" | "reset" | "disable" | "enable" | "delete", done: string) =>
    bulk.mutate(
      { ids, action, days: action === "extend" ? 30 : undefined },
      {
        onSuccess: (r) => {
          toast.ok(`${done}: ${r.affected}`);
          clear();
          setConfirmDelete(false);
        },
        onError: (e) => toast.error(errorText(e)),
      },
    );
  return (
    <>
      <AnimatePresence>
        {n > 0 ? (
          <motion.div
            className="bulk-bar glass-strong"
            role="region"
            aria-label="Действия с выбранными"
            initial={{ opacity: 0, y: 24, x: "-50%" }}
            animate={{ opacity: 1, y: 0, x: "-50%" }}
            exit={{ opacity: 0, y: 24, x: "-50%" }}
            transition={{ type: "spring", stiffness: 420, damping: 32 }}
          >
            <span className="num mr-2 font-semibold whitespace-nowrap">Выбрано {n}</span>
            <Button size="sm" loading={bulk.isPending && bulk.variables?.action === "extend"} onClick={() => run("extend", "Продлено")}>
              <CalendarPlus size={16} aria-hidden />
              <span className="max-sm:hidden">+30 дней</span>
            </Button>
            <Button size="sm" onClick={() => run("reset", "Трафик сброшен")}>
              <RotateCcw size={16} aria-hidden />
              <span className="max-sm:hidden">Сбросить трафик</span>
            </Button>
            <Button size="sm" variant="danger" onClick={() => run("disable", "Отключено")}>
              <Power size={16} aria-hidden />
              <span className="max-sm:hidden">Отключить</span>
            </Button>
            <Button size="sm" variant="danger" onClick={() => setConfirmDelete(true)} aria-label="Удалить выбранных">
              <Trash2 size={16} aria-hidden />
            </Button>
            <button type="button" className="icon-btn" aria-label="Снять выделение" onClick={clear}>
              <X size={16} />
            </button>
          </motion.div>
        ) : null}
      </AnimatePresence>
      <Confirm
        open={confirmDelete}
        onOpenChange={setConfirmDelete}
        title={`Удалить ${n} ${plural(n, "пользователя", "пользователей", "пользователей")}?`}
        text="Их ссылки перестанут работать сразу, а статистика будет удалена. Отменить это нельзя."
        confirm="Удалить"
        danger
        loading={bulk.isPending}
        onConfirm={() => run("delete", "Удалено")}
      />
    </>
  );
}
