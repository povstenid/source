# PNAT — Proxmox NAT Manager

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

## Управление сетями Proxmox

PNAT может управлять bridge-интерфейсами через Proxmox API (`/nodes/<node>/network`):

- **Create Bridge**: создаёт новый bridge (например `vmbr1`) и задаёт ему IPv4 (CIDR).
- **Bridge Ports (uplink)**: можно оставить пустым (внутренний bridge без портов) или выбрать существующий порт (eth/bond/vlan без IP), который будет подключён к bridge.
- **Attach Existing Bridge**: подключает уже существующий bridge (с настроенным IPv4/CIDR) в PNAT и позволяет сразу включить NAT и/или DHCP.

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

## Консольный интерфейс (TUI)

Если запустить `pnat` в интерактивном терминале без аргументов, откроется псевдографический интерфейс.

```bash
pnat
```

Явные режимы:

```bash
pnat tui     # принудительно открыть TUI
pnat serve   # запустить веб-сервер (аналог systemd режима)
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
