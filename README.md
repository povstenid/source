# PNAT — Proxmox NAT Manager

Minimal web and TUI tool to manage NAT, port forwards, DHCP, and internal bridges on a Proxmox VE host.

## Overview (English)

### Features

- **NAT (masquerade)** — toggle NAT per internal bridge via nftables.
- **Port forwards** — DNAT rules to map external ports to VM/LXC targets.
- **DHCP** — dnsmasq-backed pools, gateway, DNS, lease time.
- **Proxmox networks (API)** — create/attach bridges and reload networking.
- **No service restarts** — nftables and dnsmasq are applied immediately.
- **Auth** — PAM (system users) or local bcrypt password, cookie sessions.

### Architecture

- Single Go binary (HTML/CSS embedded via `embed.FS`).
- Runs as a systemd service on Proxmox.
- nftables rules live in a dedicated `ip pnat` table to avoid firewall conflicts.
- DHCP runs in a dedicated dnsmasq unit `pnat-dnsmasq.service`.
- One JSON config file at `/etc/pnat/pnat.json`.
- PAM auth uses CGO with `libpam`.

### CLI

`pnat` auto-detects the mode:

- No args in an interactive terminal: starts the TUI.
- No args in non-interactive context (systemd/cron): starts the web server.
- `-config /path/to/file` overrides the config location (default `/etc/pnat/pnat.json`).
- `pnat init` runs the interactive config generator (creates `session_secret`, auth mode, bridges).
- `pnat serve` or `pnat web` forces HTTP mode (used by `deploy/pnat.service`).
- `pnat tui` forces the TUI.
- `pnat version` prints the build version.

Config changes (NAT, forwards, DHCP, bridges) apply immediately: nftables and dnsmasq are updated without restarting `pnat`.

### Proxmox Network Management

PNAT manages bridges via the Proxmox API (`/nodes/<node>/network`):

- **Create Bridge** creates a new bridge (e.g. `vmbr1`) with IPv4/CIDR.
- **Bridge Ports (uplink)** can be empty (internal-only) or a real uplink (eth/bond/vlan without IP).
- **Attach Existing Bridge** brings an existing bridge under PNAT management and enables NAT/DHCP.

Detach simply stops PNAT from managing the bridge; it does not delete the bridge in Proxmox.
From the Dashboard VM table you can reassign `net0` (or add it for QEMU) to a PNAT bridge via the API.

### Build

Requires Go 1.18+.

```bash
# PAM (Linux auth): needs CGO + compiler + PAM headers
apt-get install -y build-essential libpam0g-dev
CGO_ENABLED=1 go build -ldflags "-s -w" -o pnat .

# Local auth only (no PAM): can build statically
CGO_ENABLED=0 go build -ldflags "-s -w" -o pnat .

# Windows cross-compile
build.bat
```

### Install on Proxmox

```bash
# 1. Copy binary
scp pnat root@proxmox:/usr/local/bin/
ssh root@proxmox "chmod +x /usr/local/bin/pnat"

# 2. Copy systemd units
scp deploy/pnat.service deploy/pnat-dnsmasq.service root@proxmox:/etc/systemd/system/

# 3. PAM config (for auth_mode=pam)
scp deploy/pnat.pam root@proxmox:/etc/pam.d/pnat

# 4. Create dirs
ssh root@proxmox "mkdir -p /etc/pnat /var/lib/pnat"

# 5. Create Proxmox API token
ssh root@proxmox "pveum user token add root@pam pnat --privsep=0"

# 6. Initialize config (interactive)
ssh root@proxmox "/usr/local/bin/pnat init"

# 7. Start
ssh root@proxmox "systemctl daemon-reload && systemctl enable --now pnat"
```

### Web UI

- **Dashboard** shows PNAT bridges, NAT toggles, DHCP links, Create/Attach forms, Proxmox bridge list, VM/NIC table with bridge reassignment, used IPs, and current nftables rules.
- **Port Forwards** adds DNAT rules with IP suggestions from VM leases; you can toggle or delete rules.
- **DHCP** edits pool range, lease time, and DNS per bridge.

### API

All endpoints require the authenticated session cookie:

- `GET /api/vms` — VM/LXC list (`vmid`, `name`, `status`, `type`).
- `GET /api/nft-status` — output of `nft list table ip pnat`.
- `GET /api/dhcp-leases` — current leases from `/var/lib/pnat/dnsmasq.leases`.

### TUI

F-keys map to the same data and actions as the web UI:

- **F1 Dashboard**, **F2 Forwards**, **F3 DHCP**, **F4 Bridges**, **F5 VMs**, **F6 Web**
- `Ctrl+R` refreshes config/leases/VMs, `Esc` exits.

### Config Structure

```json
{
  "listen_addr": "127.0.0.1:9090",
  "auth_mode": "pam",
  "auth_pam_service": "pnat",
  "auth_allow_users": ["root"],
  "session_secret": "64-hex-chars",
  "proxmox_url": "https://127.0.0.1:8006",
  "proxmox_token_id": "root@pam!pnat",
  "proxmox_secret": "uuid-token",
  "proxmox_node": "pve",
  "wan_interface": "vmbr0",
  "bridges": [
    {
      "name": "vmbr1",
      "subnet": "10.10.10.0/24",
      "gateway_ip": "10.10.10.1",
      "nat_enabled": true,
      "dhcp": {
        "range_start": "10.10.10.100",
        "range_end": "10.10.10.200",
        "lease_time": "12h",
        "dns1": "1.1.1.1",
        "dns2": "8.8.8.8"
      },
      "forwards": [
        {
          "id": "abc123",
          "protocol": "tcp",
          "ext_port": 2222,
          "int_ip": "10.10.10.101",
          "int_port": 22,
          "comment": "VM SSH",
          "enabled": true
        }
      ]
    }
  ]
}
```

For local auth (no PAM):

```json
{
  "auth_mode": "local",
  "admin_user": "admin",
  "admin_pass_hash": "$2a$10$..."
}
```

### Security Notes

- The config contains API tokens; keep it `chmod 600` and owned by root.
- PNAT listens on `127.0.0.1` by default. Access via SSH tunnel.
- Session cookies are `HttpOnly` and `SameSite=Strict`.

### Host Files

| Path | Purpose |
|------|---------|
| `/usr/local/bin/pnat` | binary |
| `/etc/pnat/pnat.json` | config (chmod 600) |
| `/etc/pnat/dnsmasq.conf` | generated dnsmasq config |
| `/run/pnat/rules.nft` | generated nftables rules |
| `/var/lib/pnat/dnsmasq.leases` | DHCP leases |
| `/etc/sysctl.d/90-pnat.conf` | ip_forward persistence |

Минимальный веб-инструмент для управления NAT, пробросом портов, DHCP и внутренними bridge-интерфейсами на хосте Proxmox VE.

## Возможности

- **NAT (masquerade)** — включение/выключение NAT на внутренних бриджах через nftables
- **Проброс портов** — DNAT правила для перенаправления внешних портов на VM/LXC
- **DHCP** — управление dnsmasq: пул адресов, gateway, DNS, lease time
- **Сети Proxmox (API)** — создание bridge, подключение существующих bridge в PNAT, reload сети
- **Без перезапуска сервиса** — изменения применяются сразу (nftables и dnsmasq), `pnat` перезапускать не нужно
- **Авторизация** — PAM (системная аутентификация Linux) или локальный bcrypt-пароль, cookie-сессии

## Архитектура

- Один бинарник на Go (все HTML/CSS встроено через `embed.FS`)
- Работает как systemd-сервис на хосте Proxmox
- Все правила nftables в изолированной таблице `ip pnat` — не конфликтует с proxmox-firewall
- DHCP через отдельный экземпляр dnsmasq (`pnat-dnsmasq.service`)
- Конфиг — один JSON файл `/etc/pnat/pnat.json`
- HTML шаблоны и CSS встроены в бинарник через `embed.FS`
- Авторизация PAM использует CGO и системную библиотеку PAM (через `libpam`)

## Командная строка

`pnat` сам определяет режим работы по аргументам и среде:

- Без аргументов в интерактивном терминале запускается текстовый TUI, не нужно перезапускать сервис вручную.
- Без аргументов в неинтерактивном контексте (systemd, cron, контейнер) службу поднимает веб-сервер.
- `-config /путь/к/файлу` позволяет указать нестандартное расположение конфигурации (по умолчанию `/etc/pnat/pnat.json`).
- `pnat init` запускает пошаговый генератор конфигурации, создаёт `session_secret`, настраивает авторизацию (PAM или локальный bcrypt-пароль) и заполняет секцию `bridges`.
- `pnat serve` или `pnat web` явно запускает HTTP-сервер (именно это делает `deploy/pnat.service`).
- `pnat tui` принудительно открывает консольный интерфейс из любого окружения.
- `pnat version` печатает версию бинарника.

Изменения конфигурации (порт-форварды, DHCP, NAT, bridges) применяются сразу — `nftables` и `dnsmasq` перезапускаются автоматически, `pnat` переинициализировать не нужно.

## Управление сетями Proxmox

PNAT может управлять bridge-интерфейсами через Proxmox API (`/nodes/<node>/network`):

- **Create Bridge**: создаёт новый bridge (например `vmbr1`) и задаёт ему IPv4 (CIDR).
- **Bridge Ports (uplink)**: можно оставить пустым (внутренний bridge без портов) или выбрать существующий порт (eth/bond/vlan без IP), который будет подключён к bridge.
- **Attach Existing Bridge**: подключает уже существующий bridge (с настроенным IPv4/CIDR) в PNAT и позволяет сразу включить NAT и/или DHCP.

С любой таблицей bridge вы можете работать из Dashboard: в списке Proxmox bridges под кнопкой Detach bridge выводится форма «Detach», которая просто прекращает управление и не удаляет bridge из Proxmox. Любой bridge тоже можно переопределить через Dashboard/VMs — у таблицы виртуальных машин есть выпадающий список мостов PNAT, чтобы переназначить `net0` (или добавить новый `net0` для QEMU) на PNAT bridge через API. Таким образом PNAT помогает держать VM сетевые интерфейсы и NAT/forward правила синхронизированными.

Ограничения:

- Имена интерфейсов в Proxmox должны быть валидными (например `vmbr1`, `lan1_nat`). Символ `-` запрещён.
- `bridge_ports` должен быть реальным интерфейсом. Значение `none` в API использовать нельзя.
- Для применения изменений Proxmox использует `ifreload` (ifupdown2); это штатный способ Proxmox для применения сетевых изменений.

## Сборка

Требуется Go 1.18+.

```bash
# PAM (Linux auth): нужен CGO + компилятор + заголовки PAM
apt-get install -y build-essential libpam0g-dev
CGO_ENABLED=1 go build -ldflags "-s -w" -o pnat .

# Только локальная auth_mode=local (без PAM): можно собрать статически
CGO_ENABLED=0 go build -ldflags "-s -w" -o pnat .

# Кросс-компиляция с Windows
build.bat
```

## Установка на Proxmox

```bash
# 1. Скопировать бинарник
scp pnat root@proxmox:/usr/local/bin/
ssh root@proxmox "chmod +x /usr/local/bin/pnat"

# 2. Скопировать systemd юниты
scp deploy/pnat.service deploy/pnat-dnsmasq.service root@proxmox:/etc/systemd/system/

# 3. PAM-конфиг (нужно для auth_mode=pam)
scp deploy/pnat.pam root@proxmox:/etc/pam.d/pnat

# 4. Создать директории
ssh root@proxmox "mkdir -p /etc/pnat /var/lib/pnat"

# 5. Создать API токен в Proxmox
ssh root@proxmox "pveum user token add root@pam pnat --privsep=0"

# 6. Инициализация конфига (интерактивно)
# Выберите auth_mode=pam чтобы логиниться Linux-пользователем (например root) без отдельного пароля PNAT.
ssh root@proxmox "/usr/local/bin/pnat init"

# 7. Запуск
ssh root@proxmox "systemctl daemon-reload && systemctl enable --now pnat"
```

## Доступ к веб-интерфейсу

По умолчанию PNAT слушает `127.0.0.1:9090`. Доступ через SSH туннель:

```bash
ssh -L 9090:127.0.0.1:9090 root@proxmox
# Открыть http://localhost:9090
```

## Веб-интерфейс

После входа в браузере открывается одностраничный интерфейс:

- **Dashboard** показывает PNAT-managed bridges (NAT-выключатели, ссылки на DHCP-контент), форму создания моста (имя, uplink, CIDR, NAT, DHCP/диапазон/DNS), список всех bridge-интерфейсов Proxmox с кнопками Attach/Detach, форму Attach для уже существующих мостов, таблицу VM/NIC с выпадающим списком bridge-опций (можно добавить `net0` для QEMU и переназначить существующие NICs), таблицу используемых IP (DHCP-аренды, NAT-цели, VM IP) и текущие правила `nftables`.
- **Port Forwards** позволяет добавлять DNAT-правила (протокол, внешний/внутренний порт, комментарий) с подсказками по IP (сборка из VM leases), переключать состояние и удалять их в один клик.
- **DHCP** показывает состояния пулов, а форма `/dhcp/edit/<bridge>` позволяет включать/выключать DHCP, менять диапазон, время аренды и DNS-серверы; изменения применяются через `pnat-dnsmasq.service`.
- **Bridges** (включая формы Create/Attach) использует Proxmox API: создание моста вызывает `POST /nodes/<node>/network`, а затем `PUT` (ifreload) через `ReloadNetwork`. Detach просто перестаёт управлять bridge без удаления из Proxmox.

Все формы используют защищённые POST-эндпойнты (`/nat/toggle`, `/forwards/*`, `/bridges/*`, `/vms/net/update`, `/dhcp/*`). Отображение связано с `/api/vms`, `/api/nft-status` и `/api/dhcp-leases`, которые тоже доступны как JSON.

## API

PNAT выставляет те же данные, что и веб-интерфейс, в виде JSON-эндпоинтов за той же сессией:

- `GET /api/vms` — список виртуальных машин и контейнеров (`vmid`, `name`, `status`, `type`).
- `GET /api/nft-status` — вывод `nft list table ip pnat`, полезен для внешних проверок и логов.
- `GET /api/dhcp-leases` — текущие DHCP-аренды из `/var/lib/pnat/dnsmasq.leases`.

Все три требуют аутентифицированной cookie (авторизация через `/login`/`/logout`) и могут быть переиспользованы для скриптов мониторинга.

## Консольный интерфейс (TUI)

Без аргументов на интерактивном терминале `pnat` запускает `tview`/`tcell` интерфейс, который отображает те же данные, что и веб-UI:

- **F1 Dashboard** — список bridge-интерфейсов с кнопками включения NAT, ссылки на DHCP и кнопки создания/присоединения мостов, таблица VM/NIC с выпадающим списком bridge-опций, список используемых IP и текущее состояние `nftables`.
- **F2 Forwards** — список порт-форвардов, форма добавления с подсказками по IP и возможности включить/отключить/удалить правило.
- **F3 DHCP** — отдельная вкладка для редактирования диапазонов и DNS (настроенные значения сохраняются в конфиге и применяются через `pnat-dnsmasq.service`).
- **F4 Bridges** — дублирует формы создания/attach/detach мостов через Proxmox API и показывает список доступных uplink-портов.
- **F5 VMs** — повторяет Web-таблицу виртуальных машин, позволяет переназначать `net0` и смотреть состояние `net*`.
- **F6 Web** — показывает состояние systemd-сервиса `pnat` (`systemctl is-active`) и слушаемый адрес.

`Ctrl+R` делает принудительное обновление (перечитывается конфиг, текущие DHCP-аренды, VM-информация), `Esc` выходит из UI.

Явные режимы:

```bash
pnat        # интерактивный TUI (входит по умолчанию на tty)
pnat tui    # принудительно открыть TUI
pnat serve  # запустить веб-сервер (аналог systemd режима)
```

## Структура конфига

```json
{
  "listen_addr": "127.0.0.1:9090",
  "auth_mode": "pam",
  "auth_pam_service": "pnat",
  "auth_allow_users": ["root"],
  "session_secret": "64-hex-chars",
  "proxmox_url": "https://127.0.0.1:8006",
  "proxmox_token_id": "root@pam!pnat",
  "proxmox_secret": "uuid-token",
  "proxmox_node": "pve",
  "wan_interface": "vmbr0",
  "bridges": [
    {
      "name": "vmbr1",
      "subnet": "10.10.10.0/24",
      "gateway_ip": "10.10.10.1",
      "nat_enabled": true,
      "dhcp": {
        "range_start": "10.10.10.100",
        "range_end": "10.10.10.200",
        "lease_time": "12h",
        "dns1": "1.1.1.1",
        "dns2": "8.8.8.8"
      },
      "forwards": [
        {
          "id": "abc123",
          "protocol": "tcp",
          "ext_port": 2222,
          "int_ip": "10.10.10.101",
          "int_port": 22,
          "comment": "VM SSH",
          "enabled": true
        }
      ]
    }
  ]
}
```

Для локального пароля (без PAM) используйте:

```json
{
  "auth_mode": "local",
  "admin_user": "admin",
  "admin_pass_hash": "$2a$10$..."
}
```

## Файлы на хосте

| Путь | Назначение |
|------|-----------|
| `/usr/local/bin/pnat` | Бинарник |
| `/etc/pnat/pnat.json` | Конфиг (chmod 600) |
| `/etc/pnat/dnsmasq.conf` | Генерируемый конфиг dnsmasq |
| `/run/pnat/rules.nft` | Генерируемые правила nftables |
| `/var/lib/pnat/dnsmasq.leases` | Файл аренд DHCP |
| `/etc/sysctl.d/90-pnat.conf` | Автоматический ip_forward |

## Безопасность

- Конфиг содержит API токен — должен быть `chmod 600 root:root`
- По умолчанию слушает только localhost
- Сессионные cookie: HttpOnly, SameSite=Strict
- nftables правила генерируются из валидированных данных, без shell injection
- Proxmox API токен рекомендуется создавать с минимальными правами
  - Для просмотра VM/LXC: достаточно `VM.Audit`.
  - Для управления сетями (создание/изменение bridge через API): требуется `Sys.Modify` на узле (`/nodes/<node>`). Это высокие права; выдавайте их только если вы реально используете функции управления сетью из UI.
