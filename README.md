# White-Shield-IMAP-Transport

## Telegram group: https://t.me/wsitproject

> [!IMPORTANT]
> **Proof of Concept (PoC).** WSIT — экспериментальный транспорт в активной
> разработке. Протокол, конфигурация, интерфейс и производительность могут
> меняться между коммитами.

## Быстрый раскур

Готовые файлы находятся в [релизе v1.0.0](https://github.com/Nikkkaaws/White-Shield-IMAP-Transport/releases/tag/v1.0.0).

### 1. Сначала VPS

На чистом Linux VPS создай рабочую папку, скачай бинарник и шаблон конфига:

```bash
mkdir -p ~/wsit-install
cd ~/wsit-install

# amd64; для ARM64 замени имя файла на WSIT-VPS-Client-linux-arm64
curl -fL -o wsit-vps https://github.com/Nikkkaaws/White-Shield-IMAP-Transport/releases/download/v1.0.0/WSIT-VPS-Client-linux-amd64
curl -fL -o config.yaml https://raw.githubusercontent.com/Nikkkaaws/White-Shield-IMAP-Transport/main/config.vps.example.yaml
chmod +x wsit-vps
```

В итоге до запуска должны существовать только два файла:

```text
~/wsit-install/wsit-vps      # скачанный серверный клиент
~/wsit-install/config.yaml   # твоя конфигурация с аккаунтами
```

Готовый шаблон также лежит в репозитории: [`config.vps.example.yaml`](config.vps.example.yaml).
Отдельного `accounts.yaml` создавать не нужно: основной аккаунт и все
дополнительные линии находятся внутри `config.yaml` в блоке `imap`.

В скачанном шаблоне активен только основной аккаунт. Блок `accounts` в нём
закомментирован специально: если нужна одна почта, ничего дополнительно не
меняй. Если нужны дополнительные линии, раскомментируй записи, замени в них
логины и пароли и удали неиспользуемые примеры — строки `CHANGE_ME` оставлять
нельзя.

Открой конфиг и заполни минимум `passphrase`, `target`, `imap.username`,
`imap.password`, `imap.folder_send` и `imap.folder_recv`. Для VPS поставь:

```yaml
mode: server
target: direct
listen: 127.0.0.1:1080
```

#### Как заполнять `config.yaml`

Для первого запуска достаточно заполнить этот блок. Значения в кавычках можно
заменять своими, названия полей и отступы YAML менять нельзя:

```yaml
mode: server                    # на VPS всегда server
listen: "127.0.0.1:1080"       # локальный SOCKS VPS, наружу не открывать
target: "direct"               # direct или локальный SOCKS, например 127.0.0.1:2080
dns_resolver: "1.1.1.1:53"      # DNS через VPS: IP:порт
passphrase: "СВОЯ-ДЛИННАЯ-ФРАЗА" # общий ключ VPS и клиентов
client_id: 1                    # для сервера оставь 1

imap:
  host: imap.rambler.ru         # адрес IMAP-сервера
  port: 993                     # TLS IMAP, обычно 993
  pin_ip: ""                    # необязательно: IP IMAP без DNS
  direct_interface: auto        # auto, off или имя интерфейса
  username: "первый@email"      # основной аккаунт
  password: "пароль-или-app-password"
  folder_send: Notes             # папка для отправки транспортных данных
  folder_recv: Journal           # папка для приёма транспортных данных
  accounts:                      # дополнительные почтовые линии
    - username: "второй@email"
      password: "второй-пароль"
    - host: imap.example.org     # можно указать другой IMAP для этой линии
      port: 993
      username: "третий@email"
      password: "третий-пароль"
```

Что именно вписывать:

| Поле | Что указать |
| --- | --- |
| `mode` | На VPS `server`. Клиентский режим нужен только для ручного запуска ядра. |
| `listen` | `127.0.0.1:1080`. Это локальный SOCKS VPS; порт не открывай в firewall. |
| `target` | `direct`, если VPS выходит в интернет напрямую. Если на VPS уже работает свой SOCKS, укажи его loopback-адрес, например `127.0.0.1:2080`. |
| `dns_resolver` | DNS в формате `адрес:порт`, например `1.1.1.1:53`. |
| `passphrase` | Своя длинная фраза. Она должна быть одинаковой на VPS и всех клиентах; значение `change-me-long-secret` оставлять нельзя. |
| `client_id` | На сервере обычно `1`. На каждом Windows/Linux/Android-клиенте выбери отдельный ID от `1` до `255`. |
| `imap.host` | IMAP-хост провайдера, например `imap.rambler.ru`. Rambler не обязателен. |
| `imap.port` | Обычно `993` для IMAP поверх TLS. |
| `imap.pin_ip` | Необязательно. IP хоста, если нужно убрать DNS-резолвинг; SNI остаётся именем `host`. |
| `imap.direct_interface` | `auto` — обычно правильный вариант; `off` отключает привязку к интерфейсу. |
| `imap.username` / `password` | Логин и пароль первой почты. Если провайдер выдаёт пароль приложения, используй его. |
| `folder_send` / `folder_recv` | Реальные папки, которые аккаунты могут создавать и изменять. На всех линиях используй одинаковые названия. |
| `imap.accounts` | Список дополнительных аккаунтов. Для каждой записи обязательны `username` и `password`; `host`, `port`, `pin_ip` и `direct_interface` можно унаследовать из верхнего блока. |

Минимальный рабочий вариант с одной почтой выглядит так:

```yaml
mode: server
listen: "127.0.0.1:1080"
target: "direct"
dns_resolver: "1.1.1.1:53"
passphrase: "замени-на-свою-фразу-длиной-от-20-символов"
client_id: 1
imap:
  host: imap.rambler.ru
  port: 993
  username: "твой-логин@rambler.ru"
  password: "твой-пароль"
  folder_send: Notes
  folder_recv: Journal
```

После проверки первой линии добавляй аккаунты по одному в `imap.accounts`.
Одинаковые `host + username` повторно не добавляются. Пустые записи и записи
без пароля пропускаются. Каждая рабочая почта становится отдельной линией и
увеличивает параллельную пропускную способность.

Остальные параметры в шаблоне уже имеют рабочие значения и для первого запуска
их лучше не менять:

| Параметры | Назначение и стартовое решение |
| --- | --- |
| `batch_delay_ms` | Задержка сборки пакета. Меньше — ниже задержка, больше — эффективнее крупные передачи. Оставь `5`. |
| `batch_min_kb`, `batch_max_kb` | Размеры пакетов IMAP. Оставь `192` и `384`, пока линии не проверены. |
| `stripe_data` | Распределяет один поток по рабочим почтам. Оставь `true`. |
| `stream_read_kb` | Размер чтения из потока. Оставь `64`. |
| `stream_window_kb` | Окно очереди потока. Оставь `8192`, увеличивай только при стабильных линиях и достаточной памяти. |
| `ack_every_frames`, `send_queue_frames`, `reorder_max_kb` | Подтверждения, очередь и буфер переупорядочивания. Оставь значения шаблона. |
| `imap_idle_refresh_sec` | Обновление долгого IMAP-сеанса. Для Rambler оставь `45`. |
| `imap_append_workers` | Воркеры отправки в IMAP. Начни с `1`; увеличивай только после проверки квот провайдера. |
| `stats_interval_sec` | Частота статистики. `15` подходит для менеджера. `0` отключает счётчики. |
| `optimistic_open_ms` | Ожидание первых байтов при открытии потока. Оставь `20`. |
| `ping_interval_ms` | Интервал поддержания линий. Оставь `10000`. |
| `purge_after_sec`, `purge_every_sec` | Очистка старых транспортных черновиков. Оставь `90` и `30`, чтобы почта не забивалась. |
| `purge_owner` | Кто чистит черновики. На VPS оставь `server`, чтобы не запускать лишний уборщик на клиентах. |
| `log_level` | `info` для обычной работы, `debug` только на время диагностики. |

После любого изменения файла сначала открой **Проверка** в менеджере. Если
проверка успешна, перезапусти `wsit.service`; клиенты перезапускать после этого
не нужно, если код подключения и `passphrase` не менялись.

Запусти установщик из этой же папки:

```bash
nano config.yaml
./wsit-vps -config ./config.yaml
```

Установщик сам создаст системные файлы:

```text
/usr/local/lib/wsit/wsit       # установленный бинарник
/usr/local/bin/wsit            # команда менеджера
/wsit                          # короткая команда менеджера
/etc/wsit/config.yaml          # рабочий конфиг VPS, права 600
/etc/systemd/system/wsit.service
```

Исходный `~/wsit-install/config.yaml` после установки больше не используется
сервисом: редактируй `/etc/wsit/config.yaml`. В менеджере открой **Проверка**,
убедись, что IMAP-линии рабочие, запусти сервер и скопируй код подключения.

Если конфиг не был создан до первого запуска, сервис не стартует — бинарник
установится, но `/etc/wsit/config.yaml` останется отсутствующим. В этом случае
создай файл из шаблона и запусти бинарник повторно с `-config ./config.yaml`.

### 2. Клиент

- [Windows-клиент](https://github.com/Nikkkaaws/White-Shield-IMAP-Transport/releases/download/v1.0.0/WSIT-Client-Windows.exe) — скачай и запусти;
- [Linux amd64](https://github.com/Nikkkaaws/White-Shield-IMAP-Transport/releases/download/v1.0.0/WSIT-Client-Linux-amd64) / [arm64](https://github.com/Nikkkaaws/White-Shield-IMAP-Transport/releases/download/v1.0.0/WSIT-Client-Linux-arm64) — `chmod +x` и запусти;
- [Android APK](https://github.com/Nikkkaaws/White-Shield-IMAP-Transport/releases/download/v1.0.0/WSIT-Android-debug.apk) — установи на телефон.

В клиенте импортируй код подключения, добавь те же IMAP-аккаунты, выбери
уникальный ID и нажми **Включить**. Windows/Linux используют SOCKS5
`127.0.0.1:1080`; Android направляет трафик через встроенный VPN.

WSIT создаёт локальный SOCKS5 и переносит TCP-потоки через один или несколько
IMAP-аккаунтов до WSIT на VPS. Несколько аккаунтов работают как параллельные
почтовые линии. SOCKS5 UDP сейчас поддерживается для DNS-запросов; произвольный
UDP-трафик пока не передаётся.

Новости разработки, обсуждение и идеи:
[t.me/wsitproject](https://t.me/wsitproject)

- [Русская инструкция](#быстрый-старт)
- [English quick start](#english-quick-start)
- [Windows-клиент](WINDOWS-CLIENT.md)
- [Linux-клиент](LINUX-CLIENT.md)
- [VPS-клиент](SERVER-CONTROL.md)
- [Android-клиент](android/README.md)

## Что входит в PoC

| Компонент | Назначение |
| --- | --- |
| `WSIT-Client-Windows.exe` | Единый установщик и интерактивный клиент Windows |
| `WSIT-Client-Linux-*` | Единый установщик и интерактивный клиент Linux |
| `WSIT-VPS-Client-linux-*` | Единый установщик, systemd-сервис и менеджер VPS |
| `WSIT-Android-*.apk` | Нативный Android-клиент без root |
| `cmd/wsit` | Транспортное ядро для ручного запуска |
| `cmd/wsitbench` | Проверка задержки, загрузки и отдачи через SOCKS5 |

```text
Приложение → 127.0.0.1:1080 (SOCKS5) → WSIT-клиент
           → IMAP-аккаунты → WSIT на VPS → direct/локальный SOCKS5 → Интернет
```

На Windows и Linux нужное приложение подключается к локальному SOCKS5. На
Android кнопка **Включить** создаёт системное VPN-подключение и направляет
трафик в тот же SOCKS5 автоматически.

## Быстрый старт

### 1. Требования

- Go 1.25 или новее для сборки;
- Windows 10/11;
- Linux VPS с systemd, `amd64` или `arm64`;
- Android 8.0 или новее для мобильного клиента;
- один или несколько IMAP-аккаунтов с TLS и возможностью создавать папки;
- исходящий доступ VPS напрямую либо через локальный SOCKS5.

Rambler доступен как готовый пресет, но не обязателен. Можно вручную указать
любой IMAP-сервер, порт и учётные данные.

### 2. Сборка

Из PowerShell в корне проекта:

```powershell
go test ./...
go vet ./...

.\scripts\build-windows-client.ps1
.\scripts\build-linux-client.ps1 -Arch amd64
.\scripts\build-server-control.ps1 -Arch amd64
.\scripts\build-android.ps1 -Variant Debug
```

Результат:

```text
build/WSIT-Client-Windows.exe
build/WSIT-Client-Linux-amd64
build/WSIT-VPS-Client-linux-amd64
build/WSIT-Android-debug.apk
```

Для VPS на ARM64:

```powershell
.\scripts\build-server-control.ps1 -Arch arm64
```

### 3. Конфигурация VPS

Создайте локальную конфигурацию:

```powershell
Copy-Item .\config.example.yaml .\config.yaml
```

Минимально измените:

- `passphrase` — длинная случайная строка;
- `target` — `direct` либо локальный SOCKS5 на VPS, например
  `127.0.0.1:1080`;
- `imap.host`, `imap.port`, `imap.username` и `imap.password`;
- `imap.accounts`, если нужны дополнительные параллельные линии.

`config.yaml` содержит секреты, исключён через `.gitignore` и не должен
попадать в коммиты. Проверка конфигурации:

```powershell
go run ./cmd/wsit -config .\config.yaml -mode server -check-config
```

### 4. Установка на VPS

Передайте бинарник и конфигурацию на сервер. Например:

```powershell
scp .\build\WSIT-VPS-Client-linux-amd64 root@VPS_IP:/root/wsit-vps
scp .\config.yaml root@VPS_IP:/root/wsit-config.yaml
```

На VPS:

```bash
chmod +x /root/wsit-vps
/root/wsit-vps -config /root/wsit-config.yaml
```

При первом запуске бинарник установится в `/usr/local/lib/wsit/wsit`, сохранит
конфигурацию как `/etc/wsit/config.yaml`, создаст `wsit.service` и команды
`wsit` и `/wsit`. Если запуск выполнен не от root, будет запрошен `sudo`.
Во время установки и удаления в терминале последовательно отображаются все
шесть стадий. После успешной установки в том же окне открывается менеджер.

После установки менеджер открывается так:

```bash
wsit
# или буквально:
/wsit
```

Сначала откройте **Проверка**, затем запустите сервер. Пункт
**Код подключения** покажет строку `WSIT1.…` для Windows и Android. Выход из
менеджера не останавливает сервис. **Удалить WSIT** удаляет сервис и бинарник,
но сохраняет `/etc/wsit/config.yaml`.

#### Как добавить аккаунты на VPS

Менеджер показывает аккаунты и проверяет их, а сами данные добавляются в
конфигурацию VPS:

```bash
sudo cp /etc/wsit/config.yaml /etc/wsit/config.yaml.bak
sudo nano /etc/wsit/config.yaml
```

Основной аккаунт указывается в `imap.username` и `imap.password`. Дополнительные
линии добавляются в `imap.accounts`:

```yaml
imap:
  host: imap.rambler.ru
  port: 993
  username: first@example.com
  password: first-password
  folder_send: Notes
  folder_recv: Journal
  accounts:
    - username: second@example.com
      password: second-password
    - host: imap.example.org
      port: 993
      username: third@example.com
      password: third-password
```

Для каждого аккаунта можно отдельно указать `host`, `port`, `pin_ip` и
`direct_interface`; если их нет, используются значения из верхнего блока
`imap`. После сохранения откройте `wsit`, нажмите **Настройки → R** для
перечитывания файла, затем выполните **Проверка** и перезапустите сервис:

```bash
sudo systemctl restart wsit
sudo systemctl status wsit --no-pager
```

В разделе **Аккаунты** пароли не показываются. Нерабочая линия будет отмечена
при проверке и не должна блокировать остальные рабочие линии.

### 5. Windows

1. Запустите `WSIT-Client-Windows.exe`.
2. Первый запуск установит клиент в `%LOCALAPPDATA%\Programs\WSIT` и создаст
   ярлык в меню «Пуск». В окне последовательно отображаются шесть стадий
   установки, после чего открывается основной интерфейс.
3. В **Настройки → Код подключения** вставьте код из VPS-менеджера.
4. В **Аккаунты** добавьте те же IMAP-аккаунты, что настроены на VPS.
5. Выберите уникальный **ID клиента** от 1 до 255.
6. Запустите WSIT и дождитесь состояния **Работает**.
7. Укажите `127.0.0.1:1080` как SOCKS5 в нужном приложении.

Пароли и код подключения сохраняются через Windows DPAPI. Удаление доступно в
**Настройки → Удалить WSIT**.

Для локальной миграции существующего `config.yaml` можно один раз запустить:

```powershell
.\build\WSIT-Client-Windows.exe -import-config .\config.yaml
```

Клиент импортирует параметры, проверит каждый аккаунт, зашифрует состояние
через DPAPI и затем откроет обычный интерфейс. Сам `config.yaml` никуда не
копируется.

### 6. Linux

Скопируйте `build/WSIT-Client-Linux-amd64` на настольный Linux и запустите:

```bash
chmod +x ./WSIT-Client-Linux-amd64
./WSIT-Client-Linux-amd64
```

Первый запуск установит клиент в `~/.local/lib/wsit`, создаст команду
`wsit-client` и пункт меню **WSIT Control**. Дальше настройка совпадает с
Windows: импортируйте код подключения, добавьте IMAP-аккаунты, выберите
уникальный ID и подключите нужное приложение к `127.0.0.1:1080`.

Для ARM64 соберите `-Arch arm64`. Удаление и автозапуск доступны в настройках.

### 7. Android

Установка отладочной сборки на подключённое устройство:

```powershell
.\scripts\install-android.ps1
```

Либо установите `build/WSIT-Android-debug.apk` обычным способом. Затем:

1. Импортируйте VPS-код в **Настройки → Код подключения**.
2. Добавьте те же IMAP-аккаунты, что используются сервером.
3. Назначьте устройству отдельный ID клиента.
4. Нажмите **Включить** и при первом запуске подтвердите системный запрос VPN.
5. Дождитесь состояния **Работает**. Трафик приложений уже направлен через
   WSIT; NekoBox, v2rayNG и ручная настройка SOCKS5 не нужны.

Root не нужен. WSIT использует Android VPN API, а собственные IMAP-соединения
исключает из туннеля, поэтому кольцевания нет. Секреты хранятся через Android
Keystore. Удаление выполняется стандартными средствами Android.

## Аккаунты и параллельные линии

- Клиент и VPS должны использовать одинаковый набор IMAP-аккаунтов, рабочие
  папки и `passphrase`.
- Каждый успешно подключённый аккаунт становится отдельной линией.
- Неработающий аккаунт исключается при запуске, остальные продолжают работу.
- Несколько устройств могут использовать те же аккаунты одновременно, но
  каждому нужен уникальный ID от 1 до 255.
- Код подключения содержит ключ транспорта, имена папок и DNS-настройку, но не
  содержит логины и пароли почты. Аккаунты добавляются отдельно.
- Код подключения является секретом: храните его как пароль.

## Проверка скорости

В клиентах есть встроенный тест. Для повторяемого CLI-замера:

```powershell
go run ./cmd/wsitbench `
  -proxy 127.0.0.1:1080 `
  -download-mib 32 `
  -upload-mib 32 `
  -parallel 4 `
  -latency-runs 5
```

Браузерные спидтесты используют разные серверы, маршруты и число потоков.
Сравнивайте изменения WSIT с одинаковыми параметрами `wsitbench`.

## Ручной запуск ядра

```powershell
go build -o wsit.exe ./cmd/wsit
.\wsit.exe -config .\config.yaml -mode client
```

Доступные режимы: `client`, `server` и `probe`.

## English quick start

WSIT is currently a **Proof of Concept**, not a stable release. It exposes a
local SOCKS5 endpoint and transports TCP streams through one or more IMAP
mailboxes to a VPS. Rambler is optional; arbitrary TLS-enabled IMAP servers are
supported.

1. Build the clients with Go 1.25+:

   ```powershell
   .\scripts\build-windows-client.ps1
   .\scripts\build-linux-client.ps1 -Arch amd64
   .\scripts\build-server-control.ps1 -Arch amd64
   .\scripts\build-android.ps1 -Variant Debug
   ```

2. Copy `config.example.yaml` to the gitignored `config.yaml`; set a real
   `passphrase`, the VPS `target`, and IMAP accounts.
3. Copy the Linux binary and config to the VPS, then run:

   ```bash
   chmod +x ./WSIT-VPS-Client-linux-amd64
   ./WSIT-VPS-Client-linux-amd64 -config ./config.yaml
   ```

4. Open `wsit` or `/wsit`, check the accounts, start the service, and copy the
   `WSIT1.` connection code.
5. Import the code in the Windows, Linux, or Android client, add the same IMAP
   accounts, assign a unique client ID, and start WSIT.
6. On Windows/Linux, point the application to `127.0.0.1:1080` via SOCKS5.
   Android routes application traffic automatically after VPN approval.

The connection code contains the transport key, folder names, and DNS setting,
but no IMAP usernames or passwords. See [WINDOWS-CLIENT.md](WINDOWS-CLIENT.md),
[LINUX-CLIENT.md](LINUX-CLIENT.md), [SERVER-CONTROL.md](SERVER-CONTROL.md), and
[android/README.md](android/README.md) for platform details.

## Known PoC limitations

- Configuration and pairing-code formats may change before a stable release.
- SOCKS5 TCP and UDP-based DNS requests are supported; arbitrary UDP is not.
- Performance depends on IMAP provider limits, VPS routing, and healthy lines.
- Providers may rate-limit, temporarily lock, or disconnect mailboxes during
  sustained high-volume use.
- Android builds currently target ARM64 and Android 8.0+.

## Частые вопросы

**Обязательно использовать Rambler?** Нет. Это только готовый пресет. Подходит
любой IMAP-сервер с TLS, папками и рабочими логином/паролем.

**Почему появляется `set a real passphrase`?** В конфигурации оставлено
`passphrase: change-me-long-secret`. Замени его на свою непустую строку и
используй ту же строку на VPS и клиентах.

**Можно подключить несколько устройств одними аккаунтами?** Да, но каждому
устройству нужен свой `client_id` от 1 до 255. Одновременная работа зависит от
лимитов IMAP-провайдера.

**Сколько аккаунтов добавлять?** Один аккаунт — одна почтовая линия. Жёсткого
малого лимита в конфигурации нет, но практическое число ограничивают квоты
почты, задержки IMAP и память VPS. Начинай с 2–4 рабочих линий.

**Как подключить приложения?** На Windows и Linux укажи SOCKS5
`127.0.0.1:1080`. На Android после нажатия **Включить** и подтверждения VPN
ручная настройка SOCKS5 и NekoBox не нужны.

**Где смотреть состояние?** В VPS-менеджере: **Обзор**, **Аккаунты**,
**Проверка** и **Журнал**. На Windows/Linux состояние видно в основном меню,
на Android — на главном экране и в уведомлении.

**Что делать после добавления аккаунта?** Сначала запусти проверку на VPS,
затем перезапусти сервис и только после этого запускай клиент. Клиент и VPS
должны использовать одинаковые аккаунты, папки и passphrase.

## Peaceful Use Request / Просьба о мирном использовании

### English

WSIT is published as an open-source project for civilian communications,
education, research, and personal use.

The author expressly and unequivocally asks that WSIT—including its source
code, binaries, forks, derivative works, documentation, protocol design, and
ideas—not be used, integrated, adapted, supplied, or deployed:

- by or for any armed force, ministry or department of defense, military or
  foreign intelligence service, or defense contractor acting in a military
  capacity;
- in the design, development, production, testing, training, deployment, or
  operation of weapons, weapon systems, unmanned aerial vehicles or drones,
  targeting systems, combat reconnaissance or surveillance, military command,
  control or communications, combat logistics, or combat-support systems;
- for the planning, preparation, conduct, or support of military or armed
  operations;
- for any action directed against the Russian Federation, its people,
  territory, armed forces, or civilian infrastructure.

If your intended use falls within any of these categories, please do not use
WSIT. Choose another technology and do not incorporate this project or its
ideas into that work.

This is an ethical and civic request and a statement of the author's intent.
It is not a legal restriction or an additional condition of the MIT License.
The MIT License in [LICENSE](LICENSE) remains the sole software license for
WSIT.

### Русский

WSIT опубликован как проект с открытым исходным кодом для гражданской связи,
образования, исследований и личного использования.

Автор прямо и недвусмысленно просит не использовать, не интегрировать, не
адаптировать, не поставлять и не развёртывать WSIT — включая исходный код,
бинарные файлы, форки, производные работы, документацию, устройство протокола
и заложенные в него идеи:

- вооружёнными силами или в их интересах, министерствами и ведомствами обороны,
  военной или внешней разведкой, а также оборонными подрядчиками при выполнении
  военных задач;
- при проектировании, разработке, производстве, испытаниях, обучении,
  развёртывании или эксплуатации оружия, систем вооружения, беспилотных
  летательных аппаратов и дронов, систем наведения и целеуказания, боевой
  разведки и наблюдения, военных систем управления и связи, боевой логистики и
  систем обеспечения боевых действий;
- для планирования, подготовки, проведения или поддержки военных и иных
  вооружённых операций;
- для любых действий, направленных против Российской Федерации, её жителей,
  территории, вооружённых сил или гражданской инфраструктуры.

Если предполагаемое применение относится хотя бы к одному из этих пунктов,
пожалуйста, не используйте WSIT. Выберите другую технологию и не включайте этот
проект или его идеи в такую работу.

Это этическая и гражданская просьба, а также публичное заявление о намерении
автора. Она не является юридическим ограничением или дополнительным условием
лицензии MIT. Единственной лицензией на WSIT остаётся MIT License из файла
[LICENSE](LICENSE).

## Contacts

- Telegram: [@peppeen](https://t.me/peppeen)
- Email: [whiteshieldd@hotmail.com](mailto:whiteshieldd@hotmail.com)
