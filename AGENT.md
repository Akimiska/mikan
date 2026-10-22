# AGENT.md — правила работы с форком mikan (AoiVPN)

Этот репозиторий — форк `Akimiska/mikan` апстрима `Miroshka000/mikan`. Он отличается от оригинала существенным набором правок, часть которых **не имеет апстримного аналога**. При мержах и обновлениях эти различия уже терялись, поэтому здесь описано, что является fork-специфичным, как это видно в коде и что делать при любом апдейте. Прочти целиком перед тем, как делать merge/rebase/деплой или трогать подписку.

---

## 1. Золотое правило: раскладка подписки НЕ меняется

Подписка (mihomo/clash-профиль) — контракт с клиентами AoiVPN. Её структура заморожена: **ровно две группы**, всегда.

```yaml
proxy-groups:
  - name: AoiVPN            # g.Main (настройка sub_group_main) — единственная select-группа
    type: select
    proxies:                # ТОЛЬКО прокси нод (плоский список; порядок = порядок нод,
      - ...                 # имена прокси при нескольких нодах получают флаг-префикс "🇳🇱 "),
      - "🇷🇺 Обход БС"       # а ПОСЛЕДНИМ членом — ссылка на bypass-группу, НЕ сами bypass-прокси
  - name: "🇷🇺 Обход БС"     # select из bypass-прокси (sub_bypass); её НЕТ, если bypass пуст
```

Ключевое: **в `AoiVPN` нет bypass-прокси поимённо** — только прокси нод + одна ссылка на
группу «🇷🇺 Обход БС». Сами bypass-прокси (их сотни) лежат в `proxies` (общий пул) и
в собственной группе «Обход БС». Если в `AoiVPN` появились члены с префиксом «Обход БС N» —
раскладка сломана (было так в ≤0.5.1.1, исправлено в 0.5.1.2).

**Запрещено в этом форке** (это приносит апстрим — проверяй при каждом мерже):

- url-test/fallback группа «Авто/Auto»;
- группы по странам/нодам (`countryGroups`, url-test per-node);
- скрытый алиас `PROXY` (`AliasGroup`);
- per-service группы от `p.route()` — сервисы идут ПРАВИЛАМИ в `g.Main`, не группами;
- `proxy-groups` больше/меньше двух (одна, если bypass пуст).

`Routes.Servers` (auto/interval/no_countries) на профиль форка **не влияет** — рендер всегда плоский, что бы ни ставил админ (контролы скрыты в `web/src/admin/pages/settings/routing.tsx`, блок `<div hidden>`).

Файл: `internal/panel/subs/render.go`, функция `mihomoConfigOf`. Маркер — комментарий «AoiVPN fork: flat two-group layout». Если его нет — раскладку затёрли.

### DNS-суффикс (второе обязательное правило)

В режимах `ru_direct`/`blocked` DoH-серверы в `dns.nameserver` должны идти с суффиксом **реального имени главной группы** (`#` + g.Main, напр. `#AoiVPN`), а НЕ константы `AliasGroup` ("PROXY"). `#PROXY` указывает на несуществующую группу → mihomo резолвит DNS прямо с устройства → утечка региона (claude.ai «wrong region», whoer «DNS differs»). Фолбэк на `AliasGroup` допустим только если в имени группы есть `#`. История: коммит `5f63d8b`, восстановлен в `0929914`.

### Как проверять после ЛЮБОГО изменения render.go/merge

```bash
# 1. Тесты + golden-файлы (они ловят изменение раскладки байт-в-байт):
CGO_ENABLED=0 go test ./internal/panel/subs/
# 2. Живая проверка на проде после деплоя (SE, панель на :48667, host network):
curl -sk "https://127.0.0.1:48667/<sub_path>/<token>" -A "mihomo/1.19.31" \
  | awk '/^proxy-groups:/{f=1;next} f&&/^  - name:/{print} f&&/^[a-z]/&&!/  /{exit}'
# Ожидание: ровно "AoiVPN" и "🇷🇺 Обход БС".
```

Golden-тесты (`internal/panel/subs/testdata/golden/`) перегенерируй осознанно
(`go test ./internal/panel/subs/ -update`) и в диффе убеждайся, что меняются только
те части, которые ты менял намеренно. Дифф golden с исчезнувшими группами «Авто»,
странами или `PROXY` = регрессия, коммитить нельзя.

---

## 2. История мержей: как различия терялись

1. **Merge v0.5.1.1-rc.6 (29edb18, 2026-10-17)** взял апстримовую версию
   `mihomoConfigOf` целиком → подписка стала 6 групп (AoiVPN + Авто + 🇳🇱/🇸🇪/🇫🇮 +
   PROXY). Восстановлено в `0929914` (плоская раскладка + DNS-суффикс `#g.Main`).
2. **Ребейс openapi** (коммит `371e95c` так и назван «восстановлено после rebase») —
   fork-правки openapi.json/schema потерялись при rebase и их восстанавливали вручную.
3. **Merge v0.5.1.1-rc.11 (21.10.2026)**: конфликты в 5 файлах (settings ×2, panel.go, settings/index.tsx, routing.tsx — апстрим разбил страницу маршрутизации на части, fork-правки перенесены заново). Апстрим заменил ядро нод mihomo → **Citrus** (getmikan/Citrus), вынес секреты из `.env` в `/run/secrets` (env-переменные `MIKAN_DATABASE_URL`/`MIKAN_NODE_JOIN` по-прежнему читаются — наши ручные compose работают), добавил `sub_domain` (рядом с нашим `sub_public_base`), SNI-роутер на 443 и блокировку API нод (по умолчанию выкл.).

Мержи здесь большие (merge rc.6 = 824 файла, +131k строк), git при этом молча берёт
апстримную сторону там, где обе стороны меняли одно место. Поэтому: после каждого
merge — обязательный чек-лист из §6.

---

## 3. Инвентарь fork-различий (на 22.10.2026, после merge v0.5.1.1-rc.12)

### 3.1 Подписка (`internal/panel/subs/`)

| Что | Где | Суть |
|---|---|---|
| Плоские 2 группы + bypass + icon | `render.go` (`mihomoConfigOf`, `selectGroup`), `Profile.Bypass` | dc04222, 0929914, 76d5298 (0.5.1.2: обход-прокси не члены главной группы) |
| Имя/иконка главной группы | settings `sub_group_main`, `sub_group_icon`; дефолт имени — «VPN», прод = «AoiVPN» | dc04222 |
| Внешние bypass-прокси | `Profile.Bypass`, settings `sub_bypass` (JSON `{group, proxies[]}`); не инбаунды mikan, панелью не трекаются | dc04222 |
| Порядок нод/пресетов | settings `sub_sort` (JSON `{nodes:[substring], presets:[id]}`) | f9a37ba |
| Локальная нода по IP, не по домену панели | `internal/panel/app/panel.go` (~:475): `.ru`-домен через DoH под ТСПУ умирает; SNI остаётся доменом | 4ca7eb3 |
| `sub_public_base` | публичный базовый URL подписок (панель за прокси); карточка `SubPublicBaseCard` в настройках; nginx bare-token на NL | 5980aab, b5b5baf |
| Content-Disposition inline, filename = brand (без .yaml) | `handler.go` (~:336) | dc04222 |
| DetectApp читает UA `core/X.Y.Z` (клиент AoiVPN) как версию mihomo | `1ac2317` | |
| DNS клиента: `respect-rules` + `prefer-h3` вместо sniffer (ломал hysteria/anilibria); `fake-ip-filter` (Roblox и excluded-from-tunnel приложения); `find-process-mode: always` (PROCESS-NAME,DIRECT для игр) | `render.go` | 7d0a798, de4fbf0, 549f9f4, d588187 |
| DNS-суффикс `#g.Main` вместо `#PROXY` | `render.go` (~:396) | 5f63d8b, восстановлен 0929914 |
| hysteria2: эмитить up/down в клиентский конфиг | `f7c3f2a` | |
| UI: блок «Выбор сервера» скрыт, DNS-пресет «tunnel» = `#AoiVPN` | `web/src/admin/pages/settings/routing.tsx` | 0929914 |

### 3.2 Протоколы (`internal/proto/`)

| Что | Коммит | Статус в main |
|---|---|---|
| hysteria2 up/down в клиентский конфиг | f7c3f2a | ✅ в main |
| Hysteria2/VLESS-клиент: `pcs` для закреплённого серта | есть (в форме апстрима `28fb242`) | ✅ |
| mieru `udp:true` (QUIC/HTTP3 claude.ai через туннель) | `1a73a98` | ✅ апстрим сам отдаёт `udp: true` (rc.11, `extra.go`) — fork-правка не нужна |
| reality `support-x25519mlkem768: true` в reality-opts | ea9a6da | ✅ апстрим ставит его, когда сервер требует (`require-x25519mlkem768`, `client.go`) — наши ноды на Citrus, не xray |
| только `pcs`, без `allowInsecure` (Xray ≥26.1 удалил allowInsecure; битый vless-outbound роняет весь конфиг) | 82f5f1c | ✅ восстановлено 21.10 (merge rc.11), маркер «AoiVPN fork (82f5f1c)» в `client.go` |
| без `pqv=1` в reality-ссылке (Xray падает на invalid mldsa65Verify) | 77e62c9 | ✅ (в main pqv никогда не эмитится) |
| mihomo v1.19.32, ipv6 off на ноде | 3f500cb | ✅ |

### 3.3 Инбаунды/пресеты

- **Shadowsocks снят с раздачи** (решение владельца): инбаунды типа shadowsocks не рендерятся в подписку — восстановлено 21.10 в `render.go` (`build`, маркер «AoiVPN fork: shadowsocks is off»). Пресет `shadowsocks_2022` в каталоге панели остаётся (апстрим с rc.11 даёт SS2022 ключ на клиента) — вернуть SS в раздачу = убрать этот `continue`, только по решению владельца.
- **Control-домены через туннель** (`ea9a6da`): `DOMAIN-SUFFIX,{sub,panel,vpn}.aoimusic.ru,<g.Main>` сразу после `directRules` (шаблоны режут правила по `len(directRules)` — ставить ДО них нельзя) + `nameserver-policy` `+.aoimusic.ru` через `#<g.Main>`. Восстановлено 21.10.

### 3.4 Панель/Люди

- Telegram-identity в списке People + состояние `no_subscription` (`NULL expires_at` = «без подписки», не «active»): `2350e33`, `3284450`, `379d11f`, `371e95c` (openapi) — ✅ в main.
- Версия форка в сайдбаре («AoiVPN · форк mikan X»), ручная проверка ядер протоколов нод: `23e1963`.
- Версия форка: `FORK_VERSION` в `.github/workflows/aoi-image.yml` → тег образа + версия в сайдбаре. Схема с 21.10: **`<версия апстрима>.aoi.<N>`** через точку внутри pre-release (сейчас `0.5.1.1-rc.12.aoi.16`): по semver она новее своей базы rc.12 и старше rc.13/0.5.1.1, т.е. честно показывает, на каком апстриме форк. Новый мерж апстрима → база меняется, N=1; fork-релиз без мержа → N+1. НЕ через дефис (`rc.11-aoi.1`): `11-aoi` — буквенный идентификатор и обгонит любой числовой rc. Раньше форк «спешил» (0.5.1.3 при апстриме 0.5.1.1-rc.x) — это была защита от апдейтера, теперь её роль играет блокировка ниже.
- **Блокировка апдейтера** (`internal/panel/updates/updates.go`, `forkLocked`/`ErrForkManual`): панель показывает, что вышло у апстрима, но `Request()` не пишет файл-запрос для хоста (API → 409 `update_fork_manual`), а `policy.json` всегда `auto:false`. Тест `TestForkLocked`. Не удалять при мерже.
- CI апстрима `goals.yml`/`pages.yml` (подписанный индекс релизов, сайт) в форке выключены условием `github.repository == 'Miroshka000/mikan'`.
- Брендинг AoiVPN (логотип/название/favicon вместо mikan) — см. §4.

### 3.5 Только на ветке `origin/aoi-0.5.0.4` (в main НЕТ; см. §3.2–3.3 и §7)

- `5f63d8b` DNS через реальную главную группу — восстановлен в main вручную (0929914);
- `ea9a6da`: control-домены `sub/panel/vpn.aoimusic.ru` → через туннель ДО RU-правил (чинило «Failed host lookup: sub.aoimusic.ru»), shadowsocks снят с раздачи;
- `82f5f1c`, `1a73a98`, `43fe0b2`, `f941a07`, `77e62c9` — см. таблицу выше; incy-RU-direct (`f941a07`, `43fe0b2`) в main нет, но upstream покрыл часть (`happroute.go` + `happ/geosite.txt` содержит `category-ru`).

---

### 3.6 API приложения AoiVPN (`internal/panel/aoiapp/`, с 0.5.1.1-rc.11.aoi.2)
- Замена `/api/app/*` легаси-бота (тот же формат ответов, AoiVPN 0.3.0 не меняется): `<sub path>/app/{plans,pay,subscription,subscriptions,order,core-report,test-sub}`; nginx на NL маппит `sub.aoimusic.ru/api/app/*` сюда (на переключении, этап 7 плана `docs/vpn-bot-to-mikan-migration.md` в монорепо).
- Свои таблицы `aoi_*` (+ sequence `aoi_app_tg_seq`) создаёт сам пакет (`CREATE … IF NOT EXISTS`), **не** goose-миграции — номера апстрима не конфликтуют.
- App-установка = синтетический отрицательный tg_id (`-(7e9+n)`; легаси-боты ≈ −5.5e9 переносятся как есть). Точки сшивки: `subs.Handler.SetApp` + маршрут `token == "app"` (handler.go), `billing.WithReturnURL` (aoi_return.go + 3 строки в `openAddonInvoice`), `tgbot.HandleEvent` пропускает `TgID < 0`, `panel.go` — `SetApp(aoiapp.New(...))`. При мерже проверить: `grep -n "AoiVPN fork" internal/panel/{subs/handler.go,billing/addon.go,tgbot/events.go}`.
- IP клиента: `X-Forwarded-For` принимается только от `MIKAN_AOI_APP_PROXY` (по умолчанию NL 79.137.198.59), без глобального `MIKAN_TRUST_PROXY`.
- Тариф приложения — `MIKAN_AOI_APP_TARIFF` (по умолчанию 3 «Стандарт»), возврат после оплаты — `MIKAN_AOI_APP_RETURN_URL` (по умолчанию `https://vpn.aoimusic.ru/app-pay-return.html`).

### 3.6.1 Merge rc.12 (22.10.2026) — что переехало
- Серверная часть бота вынесена апстримом в `internal/panel/botpanel` (Local/RPCHandler/Panel), ошибки и общие типы бота — в `internal/panel/model`, Telegram-клиент — в `internal/tgapi`. Наши вставки: `botpanel/local.go` (Referral/Refer), `model/aoi_referral.go` (ReferralStats, ErrReferralInvalid + код RPC через `init`), `tgbot` ссылается только на `model`.
- Страницы админки разрезаны: кнопка `invite` — `web/src/admin/pages/telegram/menu.tsx` (ACTIONS), строка ядра в карточке ноды — `nodes/card.tsx`, карточка «Ядро протоколов» — конец `nodes.tsx`, `SubPublicBaseCard` — `settings/subscription.tsx`.

### 3.6.2 Блок «Обновления» в настройках (с rc.12.aoi.3)
- Индекс релизов — оригинала (Miroshka000). В форке плашка сравнивает его с базой форка: «в форке последняя версия оригинала (X)» / «в оригинале вышла X — нужен перенос в форк»; список изменений оригинала показывается всегда («Последние изменения оригинала — mikan X»), кнопки «Обновить» нет (обновление = merge + подмена образа). Ключи `settings.forkUpstream*` в i18n, правка в `settings/general.tsx`. Канал «бета» включён — видны и rc.

### 3.6.3 Страница подписки и уведомления (rc.12.aoi.5)
- `web/src/sub/apps.ts`: приложение **AoiVPN** первым на Android и Windows (`flclashx://install-config`, поле `download` → `https://api.aoimusic.ru/api/version/download?platform=aoivpn[&os=windows]` — постоянная ссылка бэкенда Aoi на последнюю версию); кнопка «Скачать» в `sub/view.tsx`, ключи `sub.download`, `sub.notes.official`.
- rc.12.aoi.9: у AoiVPN своя иконка (`sub/aoivpn.png` = лаунчер-иконка приложения, поле `icon` в apps.ts), INCY на iOS, описание «Наше приложение», подзаголовок бренда переносится (не truncate). Platega в магазине называется «Картой, СБП или SberPay» — `aoiAddonName()` в `billing/aoi_return.go` (+2 строки в `AddonName`).
- rc.12.aoi.11: магазин для ОДНОГО тарифа (`tgbot/aoi_shop.go`): «Продлить»/«Купить» сразу показывают сроки («3 месяца · 456 ₽ (−5%)», скидка от цены 30-дневного срока), заголовки «Продление на 3 месяца — …»; хуки `aoiSingle/aoiBack/aoiTermLabel/aoiPickText/aoiTermText` в screens.go/shop.go. Под текстом тарифа — `tariffs.description`. Страница-кнопка с id `about` получает кнопки канала/документов/поддержки (`aoiPageRows`).
- rc.12.aoi.12: магазин Mini App/страницы (`sub/shop.tsx`): сроки в месяцах + бейдж скидки (`aoiTermLabel`/`aoiDiscount` в конце файла).
- `botpanel/panel.go` `due()`: «осталось 3 дня» не шлётся подпискам со сроком ≤ 3 дней (триал), как в легаси.

### 3.6.4 Бот: кабинет как в легаси (rc.12.aoi.7)
- `tgbot`: имена подписок в боте без хвоста « (<tg id>)» — `subName()` в `aoi_referral.go`, все `u.Name` в экранах/сообщениях заменены на неё (при мерже апстрима новые `u.Name` в tgbot тоже заворачивать).
- Переменные `{until_full}` («16 декабря 2026, 21:00 МСК») и `{term_line}` (по состоянию: «Действует до …», «Закончилась … — продлите», «Бессрочно») — `aoiVars()` в конце `vars()`; статусы «✅ Подписка активна»/«⏳ Подписка скоро закончится» (texts.go).
- Экран «Подключить» (rc.12.aoi.15): Android/Windows — AoiVPN (ссылки скачивания в тексте `connectText`), iPhone/Mac/Linux — через страницу подписки; кнопки: «⚡ Открыть в AoiVPN» (`aoiConnectRows`, `<subURL>#open=AoiVPN` — Telegram не открывает `flclashx://`, страница по `#open=` запускает приложение) и «🌐 Страница подписки» (`openPage`); кнопка «🔁 Подписки (N)» только при N>1 (`screens.go` menu).
- Golden-тесты бота (`model/testdata/BotConfig.*.json`, `methods.txt`) обновлены под `invite`/Referral/Refer — **гонять `go test ./internal/panel/tgbot/ ./internal/panel/model/` перед релизом** (CI форка тесты не запускает).
- Настройки бота на проде (tg_config) пишутся в Postgres (API session_only), проверка — `tgbot.Config.Validate`.

### 3.7 Рефералка (`internal/panel/aoiref/`, с 0.5.1.1-rc.11.aoi.3)
- Правила легаси-бота: ссылка `t.me/<bot>?start=ref-<code>` (старые коды перенесены — разосланные ссылки работают), рефералом становится аккаунт без подписок/триала/оплат; за его первую оплату тарифа (kind new/renew) рефереру +7 дней, не раньше 24 ч после приглашения; нет подписки — дни копятся в `aoi_ref_bank` и добавятся, когда она появится; возврат платежа — дни списываются, реферал → `ineligible`.
- Свои таблицы `aoi_ref_codes`, `aoi_referrals`, `aoi_ref_bank` (создаёт пакет). Фоновый цикл `Referrals.Run` (5 мин) в workers панели.
- Бот: `Backend.Referral/Refer` (Local + Remote + rpc-ошибка `referral_invalid`; с rc.12 ещё белый список RPC аддона — `addons/panelapi/aoi_botrpc.go`, без него «the method is not open to addons»), `/start ref-…` в `bot.go`, встроенная кнопка `invite` (config.go: builtins, Default, enum; screens.go: меню + `case "rf"`), экран и тексты — `tgbot/aoi_referral.go`; админка: `ACTIONS` в `web/src/admin/pages/telegram.tsx`, `telegram.action.invite` в i18n, enum в `openapi.json`/`schema.d.ts`. При мерже: `grep -rn "aoiref\|invite" internal/panel/tgbot/*.go web/src/admin/pages/telegram.tsx`.

## 4. Брендинг (правила с 18.10.2026)

По TRADEMARK.md апстрима имя «mikan», логотип и маскот НЕ входят в GPL-лицензию —
форк не должен называться mikan и использовать его брендинг. Поэтому:

- **Пользователь-видимое = AoiVPN:** `<title>` в `web/index.html`, `brand-name` в
  `web/src/admin/shell.tsx`, экран логина `web/src/admin/pages/login.tsx`,
  ассеты `web/src/assets/logo.webp` + `web/public/favicon.png` +
  `web/public/apple-touch-icon.png`, i18n `shell.forkVersion` («AoiVPN · форк mikan {v}» —
  атрибуция «форк mikan» текстом разрешена). README.md описывает форк как AoiVPN.
- **НЕ переименовывать** (технические идентификаторы, не бренд): CLI-команды
  бинарника (`mikan status`, `mikan reset-password`, … — на них ссылаются тексты
  подсказок), Go-модуль `mikan`, ключи localStorage (`mikan.sidebar`, `mikan-theme`),
  имя палитры `mikan` в `theme.tsx`/`theme-boot.js` (это имя цветовой темы, не
  продукта), конфиг-ключи `mikan.tls`/`mikan.client` в шаблонах, маркер
  `<!-- mikan:base -->` в `web/index.html`, образ `ghcr.io/akimiska/mikan`,
  `LICENSE`/`NOTICE.md`/`TRADEMARK.md` апстрима.
- При мерже апстрима: любые новые пользователь-видимые строки с «mikan» (экраны,
  письма, `<title>`) — проверить и при необходимости заменить на AoiVPN; технические
  ключи не трогать.

---

## 5. CI/деплой форка

- `.github/workflows/aoi-image.yml` — единственный CI форка: lean amd64-образ → `ghcr.io/akimiska/mikan:{latest,sha,VERSION}`. `FORK_VERSION` в env — бампать при каждом fork-релизе. Апстримный `ci.yml` (go test) на push в main НЕ бегает (только PR) — **тесты локально обязательны**.
- **Обновление панели — НЕ через кнопку «Обновить» и НЕ авто:** host-updater тянет подписанный индекс **upstream** (`installer/src/release.rs`: REPO + PUBLIC_KEY захардкожены) → заменит форк чистым апстримом. Деплой только руками: на SE `cd /opt/mikan && MIKAN_IMAGE=ghcr.io/akimiska/mikan:<tag> docker compose pull && docker compose up -d`. `nodes_follow_panel=false` в БД (дефолт апстрима ON!). Подробнее: policy.json `{"auto":false}`, не включать автообновление.
- Rollback: на SE лежат старые образы (0.5.0.x), откат = сменить тег в `.env` + `docker compose up -d`.

---

## 6. Чек-лист обновления панели/ноды (merge или апдейт) — ОБЯЗАТЕЛЬНЫЙ

**Принцип: fork-правки восстанавливаются ВСЕГДА. Апстрим-фичи принимаются, но не в ущерб пункту 1.**

1. **До мержа**: `git diff <текущий-main-форка> -- internal/panel/subs/render.go` — сохранить копию `mihomoConfigOf` (или зафиксировать хэш `git hash-object`), список fork-коммитов: `git log --author=Akimiska --oneline --no-merges`.
2. **Мержить осознанно**: файлы с маркерами «AoiVPN fork» (`render.go`, `handler.go`, `panel.go`, `settings.go`, `routing.tsx`) разбирать конфликт за конфликтом, НЕ брать апстримную сторону целиком. Если конфликт в `mihomoConfigOf` — раскладка форка выигрывает всегда.
3. **Сразу после мержа, до коммита**, прогнать:
   ```bash
   grep -n "flat two-group layout" internal/panel/subs/render.go          # маркер раскладки
   grep -n "dnsGroup := g.Main" internal/panel/subs/render.go             # DNS-суффикс
   grep -n "support-x25519mlkem768" internal/proto/client.go              # reality keyshare
   grep -rn "countryGroups\|\"PROXY\", \"type\": \"select\", \"hidden\"" internal/panel/subs/render.go
   gofmt -l internal/ | head
   ```
   Всё, что вернуло апстримское поведение, — вернуть (см. §1 и §3).
4. **Тесты локально** (`CGO_ENABLED=0` в этом окружении; для `api`/`acme` нужен `MIKAN_TEST_DATABASE_URL` — disposable Postgres, потом вычистить):
   ```bash
   go test ./internal/panel/subs/ ./internal/panel/api/ ./internal/panel/acme/
   cd web && tsc -b && vite build
   ```
   Golden-дифф: `git diff -- internal/panel/subs/testdata/golden/ | grep -E "^[+-].*name:"` — там не должны появиться Авто/страны/PROXY.
5. **Живой smoke на SE после деплоя** (см. §1 curl): ровно 2 группы, DNS nameserver с `#<g.Main>`, обход-группа последняя.
6. **Версия**: `FORK_VERSION` = `<апстрим>.aoi.<N>` (см. §3.4), задеплоить образ (`cd /opt/mikan && MIKAN_IMAGE=... docker compose up -d`), образы старых версий остаются локально для отката. Проверить, что `forkLocked` на месте (`grep -n forkLocked internal/panel/updates/updates.go` — 3 места: SetPolicy, Request, Host: с 22.10 панель не показывает отчёт хост-апдейтера «Не удалось обновить до …»), `auto=false`, `nodes_follow_panel=false`.
   Тесты пакетов с Postgres (`api`, `settings`, `app`, …) в CLI-окружении НЕ гонять (proot: initdb виснет) — `go build ./... && go vet` + тесты без БД + живой smoke.
7. **Не жать «Обновить» в панели** и не включать auto-update: updater тянет upstream-релизы (installer/src/release.rs: REPO + PUBLIC_KEY upstream), обновление затрёт форк.

## 7. Если раскладка всё же разъехалась

- Быстрый откат образа: сменить `MIKAN_IMAGE` на предыдущий тег (`0.5.0.x` лежат на SE) + `docker compose up -d`.
- Код: сравнить с `dc04222` (оригинал плоской раскладки) и `0929914` (восстановление после rc.6): `git log --oneline -- internal/panel/subs/render.go`.
- После фикса — перегенерить golden (`go test ./internal/panel/subs/ -update`), обновить тесты, коммитить с явным «subs: вернуть плоскую раскладку» в сообщении.
