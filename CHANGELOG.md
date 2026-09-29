# Changelog

Each release has a section in English and in Russian; the release workflow puts them in
the signed manifest, and the panel shows the one in its language.

## 0.3.9
### en
- The panel has a default language, picked at install: the admin panel and the subscription page open in it until a visitor picks their own, and default names (tariffs, the auto-select group, the bot's menu) are in it. Settings → Default language changes it; "Browser language" keeps the old behaviour.
- The server's command line (`mikan admin …`) is in English.
- A node joins with one command that installs everything from the latest release; the Nodes page shows it with the join key.

### ru
- У панели есть язык по умолчанию, его выбирают при установке: админка и страница подписки открываются на нём, пока человек не выбрал свой, и на нём же названия по умолчанию (тарифы, группа автовыбора, меню бота). Меняется в «Настройки → Язык по умолчанию»; «Как в браузере» — прежнее поведение.
- Серверные команды (`mikan admin …`) — на английском.
- Нода подключается одной командой, которая ставит всё из последнего релиза; страница «Ноды» показывает её вместе с ключом.

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
