# Changelog

Each release has a section in English and in Russian; the release workflow puts them in
the signed manifest, and the panel shows the one in its language.

## 0.4.0
### en
- Pick the TLS fingerprint clients send: Settings → Subscription sets the default for all protocols, and a protocol's settings can choose its own (Chrome, Firefox, Safari, iOS, Android, Edge, 360, QQ or a random one). Links (`fp=`) and Clash profiles (`client-fingerprint`) follow the choice; Hysteria2 and TUIC have no such fingerprint.
- API keys and an API reference: the new API page makes keys for scripts and integrations (`Authorization: Bearer`, read-only or full access, an optional expiry, revocable) and lists every method with its parameters, responses, curl examples and a "Try it" button for GET requests.
- Selling subscriptions: mark a plan "On sale" with a price in Telegram Stars and/or rubles, turn payment methods on in the new Payments section (Telegram Stars needs only the bot; YooKassa for cards and SBP; CryptoBot for crypto), and people buy or renew in the bot and the Mini App. The panel creates or renews the subscription as soon as the provider confirms the payment and the bot sends the link. A renewal adds the plan's term after the current one and starts a new traffic period. Payments has the history and Stars refunds.
- Cloudflare WARP as a way out for chosen protocols (#4): Nodes → WARP registers a free account in one click (a WARP+ key is optional) or takes your own WireGuard config. A protocol's settings pick "Way out: WARP", and a list of domains and networks goes through WARP for every protocol of the node; the rest goes direct. When WARP is down its traffic does not fall back to the server's own address. The node shows the address sites see through WARP.

### ru
- Выбор TLS-отпечатка клиентов: в «Настройки → Подписка» — общий для всех протоколов, в настройках протокола — свой (Chrome, Firefox, Safari, iOS, Android, Edge, 360, QQ или случайный). Ссылки (`fp=`) и Clash-профили (`client-fingerprint`) берут выбранный; у Hysteria2 и TUIC такого отпечатка нет.
- Ключи API и справочник: в новом разделе «API» создаются ключи для скриптов и интеграций (`Authorization: Bearer`, только чтение или полный доступ, срок по желанию, отзыв), а все методы описаны с параметрами, ответами, примерами curl и кнопкой «Выполнить» для GET-запросов.
- Продажа подписок: отметьте тариф «В продаже» с ценой в Telegram Stars и/или рублях, включите способы оплаты в новом разделе «Платежи» (Telegram Stars — нужен только бот; ЮKassa — карты и СБП; CryptoBot — криптовалюта), и люди покупают и продлевают подписку в боте и Mini App. Панель создаёт или продлевает подписку, как только провайдер подтвердил оплату, а бот присылает ссылку. Продление добавляет срок тарифа после текущего и начинает новый период трафика. В «Платежах» — история и возврат Stars.
- Cloudflare WARP как выход для выбранных подключений (#4): «Ноды → WARP» регистрирует бесплатный аккаунт в один клик (ключ WARP+ — по желанию) или принимает свой WireGuard-конфиг. В настройках подключения выбирается «Выход в интернет: WARP», а список доменов и сетей идёт через WARP у всех подключений ноды; остальное — напрямую. Если WARP недоступен, его трафик не уходит с адреса сервера. Нода показывает адрес, который видят сайты через WARP.

## 0.3.9
### en
- The panel has a default language, picked at install: the admin panel and the subscription page open in it until a visitor picks their own, and default names (tariffs, the auto-select group, the bot's menu) are in it. Settings → Default language changes it; "Browser language" keeps the old behaviour.
- The server's command line (`mikan admin …`) is in English.
- A node joins with one command that installs everything from the latest release; the Nodes page shows it with the join key.
- A new installer and server menu in the terminal. First the panel's language, then checks of the server and of the domain's DNS, REALITY sites next to the server, and the admin's login with a QR code. Later runs of `mikan` open a menu: status, updates, logs, access, REALITY sites, nodes, backups.
- Releases come from GitHub: the image from GitHub Packages, trusted through a signed manifest. Updates back up first and go back when the new version does not start.
- The panel looks for a new release once a day and shows what changed. Settings → Updates installs it with a button or turns on automatic updates at night; a badge in the sidebar tells when one is out.

### ru
- У панели есть язык по умолчанию, его выбирают при установке: админка и страница подписки открываются на нём, пока человек не выбрал свой, и на нём же названия по умолчанию (тарифы, группа автовыбора, меню бота). Меняется в «Настройки → Язык по умолчанию»; «Как в браузере» — прежнее поведение.
- Серверные команды (`mikan admin …`) — на английском.
- Нода подключается одной командой, которая ставит всё из последнего релиза; страница «Ноды» показывает её вместе с ключом.
- Новый установщик и меню сервера в терминале. Сначала язык панели, потом проверки сервера и DNS домена, сайты REALITY рядом с сервером и вход администратора с QR-кодом. Повторный запуск `mikan` открывает меню: состояние, обновления, логи, доступ, сайты REALITY, ноды, бэкапы.
- Релизы приходят с GitHub: образ из GitHub Packages, доверие через подписанный манифест. Обновление сначала делает бэкап и откатывается, если новая версия не запустилась.
- Панель раз в сутки проверяет новые релизы и показывает, что изменилось. «Настройки → Обновления» ставят релиз по кнопке или включают автообновление ночью; значок в боковой панели подскажет, когда вышла новая версия.

## 0.3.8
### en
- The Telegram tab of the admin panel animates like the others: cards rise in turn, the unsaved-changes bar slides in and out, menu buttons slide to their new place.

### ru
- Вкладка Telegram в админке анимирована как остальные: карточки выезжают по очереди, плашка несохранённых изменений выезжает и уезжает, кнопки меню плавно переставляются.

## 0.3.7
### en
- The Telegram bot sends through a queue within Telegram's limits: replies first, then notices, then broadcasts; fast taps show the last screen; flood waits are waited out.
- Notices at night (22:00–9:00 Moscow time) arrive silently; broadcasts show their progress.
- New "dawn" background in the panel.
- The Mini App button next to the chat's input field is one word, so the field is not pushed out on phones.
- A REALITY target given by IP shows and checks its site name (SNI).

### ru
- Telegram-бот отправляет сообщения через очередь в рамках лимитов Telegram: сначала ответы, потом уведомления, потом рассылки; при быстрых нажатиях виден последний экран; флуд-ожидания выдерживаются.
- Уведомления ночью (22:00–9:00 МСК) приходят без звука; у рассылки виден прогресс.
- Новый фон панели «Рассвет».
- Кнопка Mini App у поля ввода — одно слово, поле больше не пропадает на телефонах.
- У цели REALITY по IP видно и проверяется имя сайта (SNI).

## 0.3.6
### en
- Telegram bot for subscribers with a menu builder in the panel, notifications and broadcasts.
- The subscription page opens as a Telegram Mini App.

### ru
- Telegram-бот для подписчиков с конструктором меню в панели, уведомлениями и рассылками.
- Страница подписки открывается как Mini App в Telegram.

## 0.3.5
### en
- New protocols: TrustTunnel, ShadowQUIC, Mieru; shared-key Shadowsocks-2022, Sudoku, Snell.
- Subscriptions give every app only the protocols it can run.

### ru
- Новые протоколы: TrustTunnel, ShadowQUIC, Mieru; с общим ключом — Shadowsocks-2022, Sudoku, Snell.
- Подписка отдаёт каждому приложению только те протоколы, которые оно умеет.

## 0.3.4
### en
- Automatic moves judge a port by the devices that reached it before; no more false "devices cannot connect".

### ru
- Автоподбор судит о порте по устройствам, которые до него раньше доходили; ложное «не доходят устройства» ушло.

## 0.3.3
### en
- Billing days and device binding against key sharing.
- VLESS with post-quantum encryption (VLESS PQ).

### ru
- День оплаты и привязка к устройствам против перепродажи ключа.
- VLESS с постквантовым шифрованием (VLESS PQ).
