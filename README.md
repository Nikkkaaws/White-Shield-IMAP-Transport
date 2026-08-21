# White-Shield-IMAP-Transport

> [!IMPORTANT]
> **Proof of Concept (PoC).** WSIT — экспериментальный транспорт в активной
> разработке. Протокол, конфигурация, интерфейс и производительность могут
> меняться между коммитами.

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
