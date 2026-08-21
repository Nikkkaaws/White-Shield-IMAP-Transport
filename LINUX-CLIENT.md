# WSIT Client for Linux

`WSIT-Client-Linux-*` — настольная Linux-версия единого установщика и клиента.
Она использует тот же терминальный интерфейс, формат кода подключения и
локальный SOCKS5, что и Windows-клиент.

## Сборка

```powershell
.\scripts\build-linux-client.ps1 -Arch amd64
# либо
.\scripts\build-linux-client.ps1 -Arch arm64
```

Готовые файлы: `build/WSIT-Client-Linux-amd64` и
`build/WSIT-Client-Linux-arm64`.

## Установка

```bash
chmod +x ./WSIT-Client-Linux-amd64
./WSIT-Client-Linux-amd64
```

Первый запуск:

- копирует клиент в `~/.local/lib/wsit/wsit-client`;
- создаёт команду `~/.local/bin/wsit-client`;
- добавляет **WSIT Control** в меню приложений;
- открывает основной интерфейс в текущем терминале.

После установки клиент запускается командой:

```bash
wsit-client
```

Если `~/.local/bin` отсутствует в `PATH`, запускайте
`~/.local/bin/wsit-client` либо добавьте каталог в `PATH`.

Состояние хранится в `${XDG_CONFIG_HOME:-~/.config}/wsit/client.dat` с правами
`0600`. Автозапуск создаёт desktop-файл в каталоге XDG autostart. Пункт
**Удалить WSIT** убирает бинарник, команду, desktop-файлы и локальное состояние.

## Настройка

1. Вставьте VPS-код в **Настройки → Код подключения**.
2. Добавьте те же IMAP-аккаунты, что настроены на VPS.
3. Выберите уникальный ID клиента от 1 до 255.
4. Нажмите **Включить** и дождитесь состояния **Работает**.
5. Укажите `127.0.0.1:1080` как SOCKS5 в нужном приложении.
