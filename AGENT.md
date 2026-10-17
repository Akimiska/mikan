# AGENT.md — правила работы с форком mikan (AoiVPN)

Этот репозиторий — форк `Akimiska/mikan` апстрима `Miroshka000/mikan`. Он отличается от оригинала существенным набором правок, часть которых **не имеет апстримного аналога**. При мержах и обновлениях эти различия уже терялись, поэтому здесь описано, что является fork-специфичным, как это видно в коде и что делать при любом апдейте. Прочти целиком перед тем, как делать merge/rebase/деплой или трогать подписку.

---

## 1. Золотое правило: раскладка подписки НЕ меняется

Подписка (mihomo/clash-профиль) — контракт с клиентами AoiVPN. Её структура заморожена: **ровно две группы**, всегда.

```yaml
proxy-groups:
  - name: AoiVPN            # g.Main (настройка sub_group_main) — единственная select-группа
    type: select
    proxies:                # ВСЕ прокси всех нод плоским списком; порядок = порядок нод,
      - ...                 # имена прокси при нескольких нодах получают флаг-префикс ("🇳🇱 ")
      - "🇷🇺 Обход БС"       # последним членом, если есть sub_bypass
  - name: "🇷🇺 Обход БС"     # select из bypass-прокси (sub_bypass); её НЕТ, если bypass пуст
```

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

## 2. История: как различия терялись (два прецедента)

1. **Merge v0.5.1.1-rc.6 (29edb18, 2026-10-17)** взял апстримовую версию
   `mihomoConfigOf` целиком → подписка стала 6 групп (AoiVPN + Авто + 🇳🇱/🇸🇪/🇫🇮 +
   PROXY). Восстановлено в `0929914` (плоская раскладка + DNS-суффикс `#g.Main`).
2. **Ребейс openapi** (коммит `371e95c` так и назван «восстановлено после rebase») —
   fork-правки openapi.json/schema потерялись при rebase и их восстанавливали вручную.

Мержи здесь большие (merge rc.6 = 824 файла, +131k строк), git при этом молча берёт
апстримную сторону там, где обе стороны меняли одно место. Поэтому: после каждого
merge — обязательный чек-лист из §5.

---

## 3. Инвентарь fork-различий (на 17.10.2026, main = 0929914)

### 3.1 Подписка (`internal/panel/subs/`)

| Что | Где | Суть |
|---|---|---|
| Плоские 2 группы + bypass + icon | `render.go` (`mihomoConfigOf`, `selectGroup`), `Profile.Bypass` | dc04222, 0929914 |
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
| **mieru `udp:true`** (QUIC/HTTP3 claude.ai через туннель) | `1a73a98` | ❌ ПОТЕРЯН в main — `extra.go` ~:168 сейчас `udp: false` |
| **reality `support-x25519mlkem768: true` в reality-opts** (xray ≥26.9.8 требует PQ keyshare; без него mihomo/AoiVPN не коннектится к xhttp-reality) | ea9a6da | ❌ ПОТЕРЯН в main (`client.go:220` — только pbk+sid) |
| только `pcs`, без `allowInsecure` (Xray ≥26.1 удалил allowInsecure; битый vless-outbound роняет весь конфиг) | 82f5f1c | ❌ на main вернулись `allowInsecure=1` + `insecure=1` + `pcs` |
| без `pqv=1` в reality-ссылке (Xray падает на invalid mldsa65Verify) | 77e62c9 | ✅ (в main pqv никогда не эмитится) |
| mihomo v1.19.32, ipv6 off на ноде | 3f500cb | ✅ |

### 3.3 Инбаунды/пресеты

- **Shadowsocks снят с раздачи** (решение владельца): инбаунды типа shadowsocks не рендерятся в подписку + пресет Shadowsocks-2022 убран из каталога — коммит `ea9a6da` на ветке `origin/aoi-0.5.0.4`, в main **ПОТЕРЯН**: preset `shadowsocks_2022` в каталоге, инбаунды рендерятся. При следующем касании presets/render — восстановить или спросить владельца.

### 3.4 Панель/Люди

- Telegram-identity в списке People + состояние `no_subscription` (`NULL expires_at` = «без подписки», не «active»): `2350e33`, `3284450`, `379d11f`, `371e95c` (openapi) — ✅ в main.
- Версия форка в сайдбаре («Aoi · mikan X»), ручная проверка ядер протоколов нод: `23e1963`.
- Версия форка: `FORK_VERSION` в `.github/workflows/aoi-image.yml` (сейчас 0.5.1.1) → тег образа + версия в сайдбаре.

### 3.5 Только на ветке `origin/aoi-0.5.0.4` (в main НЕТ; см. §3.2–3.3 и §6)

- `5f63d8b` DNS через реальную главную группу — восстановлен в main вручную (0929914);
- `ea9a6da`: control-домены `sub/panel/vpn.aoimusic.ru` → через туннель ДО RU-правил (чинило «Failed host lookup: sub.aoimusic.ru»), shadowsocks снят с раздачи;
- `82f5f1c`, `1a73a98`, `43fe0b2`, `f941a07`, `77e62c9` — см. таблицу выше; incy-RU-direct (`f941a07`, `43fe0b2`) в main нет, но upstream покрыл часть (`happroute.go` + `happ/geosite.txt` содержит `category-ru`).
---

## 4. CI/деплой форка

- `.github/workflows/aoi-image.yml` — единственный CI форка: lean amd64-образ → `ghcr.io/akimiska/mikan:{latest,sha,VERSION}`. `FORK_VERSION` в env — бампать при каждом fork-релизе. Апстримный `ci.yml` (go test) на push в main НЕ бегает (только PR) — **тесты локально обязательны**.
- **Обновление панели — НЕ через кнопку «Обновить» и НЕ авто:** host-updater тянет подписанный индекс **upstream** (`installer/src/release.rs`: REPO + PUBLIC_KEY захардкожены) → заменит форк чистым апстримом. Деплой только руками: на SE `cd /opt/mikan && MIKAN_IMAGE=ghcr.io/akimiska/mikan:<tag> docker compose pull && docker compose up -d`. `nodes_follow_panel=false` в БД (дефолт апстрима ON!). Подробнее: policy.json `{"auto":false}`, не включать автообновление.
- Rollback: на SE лежат старые образы (0.5.0.x), откат = сменить тег в `.env` + `docker compose up -d`.

---

## 5. Чек-лист обновления панели/ноды (merge или апдейт) — ОБЯЗАТЕЛЬНЫЙ

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
6. **Версия**: бампнуть `FORK_VERSION` в `.github/workflows/aoi-image.yml`, задеплоить образ (`cd /opt/mikan && MIKAN_IMAGE=... docker compose up -d`), образы старых версий остаются локально для отката. Держать `FORK_VERSION` ≥ последнего upstream-релиза, `auto=false`, `nodes_follow_panel=false`.
7. **Не жать «Обновить» в панели** и не включать auto-update: updater тянет upstream-релизы (installer/src/release.rs: REPO + PUBLIC_KEY upstream), обновление затрёт форк.

## 6. Если раскладка всё же разъехалась

- Быстрый откат образа: сменить `MIKAN_IMAGE` на предыдущий тег (`0.5.0.x` лежат на SE) + `docker compose up -d`.
- Код: сравнить с `dc04222` (оригинал плоской раскладки) и `0929914` (восстановление после rc.6): `git log --oneline -- internal/panel/subs/render.go`.
- После фикса — перегенерить golden (`go test ./internal/panel/subs/ -update`), обновить тесты, коммитить с явным «subs: вернуть плоскую раскладку» в сообщении.
