# horn-vpn-manager

`horn-vpn-manager` — OpenWrt-пакет для управления VPN-подписками sing-box и маршрутизацией трафика через dnsmasq/nftables. Написан на Go. Репозиторий содержит core-пакет, LuCI-интерфейс и Docker-based сборку пакетов.

## Что есть в репозитории

- `horn-vpn-manager` — Go-бинарник `vpn-manager` для обновления подписок, генерации `sing-box` конфига и управления domain/IP lists
- `horn-vpn-manager-luci` — LuCI UI поверх rpcd backend
- `Makefile`, `Dockerfile`, `docker/entrypoint.sh` — локальная сборка `.apk` и `.ipk` через OpenWrt SDK в контейнере

## Что делает core-пакет

`vpn-manager subscriptions run`:

1. Читает подписки из `config.json`
2. Скачивает каждую подписку по URL
3. Автоопределяет формат: raw `vless://`, base64, base64url, gzip
4. Фильтрует узлы по `include` / `exclude`
5. Для multi-node подписок создаёт стабильные node tags, `urltest`-группу `<id>-auto` и selector `<id>-manual`
6. Для single-node подписок создаёт прямой outbound `<id>-single`
7. Собирает route rules по `route.domains`, `route.ip_cidrs` и загруженным спискам
8. Генерирует `sing-box` config из шаблона
9. Проверяет конфиг через `sing-box check`, сохраняет backup и перезапускает `sing-box`

`vpn-manager routing run`:

1. Скачивает dnsmasq domain list и subnet lists из `config.json`
2. Кэширует их в `/etc/horn-vpn-manager/lists/`
3. Собирает итоговый IP list с учётом `manual-ip.lst`
4. Обновляет dnsmasq/firewall

`vpn-manager routing restore` восстанавливает domain/IP lists из кэша без скачивания (для boot при отсутствии сети).

Init script `/etc/init.d/horn-vpn-manager` ждёт доступ в интернет, затем запускает `routing run` и `subscriptions run`. Если сети нет, он восстанавливает domain/IP lists из кэша через `routing restore`.

## Пути на роутере

| Путь | Назначение |
|---|---|
| `/usr/bin/vpn-manager` | единый CLI |
| `/etc/horn-vpn-manager/config.json` | основной конфиг |
| `/etc/horn-vpn-manager/lists/` | кэш domain/subnet lists |
| `/etc/horn-vpn-manager/lists/manual-ip.lst` | ручной список IP/CIDR |
| `/usr/share/horn-vpn-manager/sing-box.template.default.json` | шаблон sing-box по умолчанию |
| `/usr/share/horn-vpn-manager/config.example.json` | пример конфига |
| `/etc/sing-box/config.json` | сгенерированный sing-box config |
| `/tmp/horn-vpn-manager-subscriptions.log` | лог subscriptions |
| `/tmp/horn-vpn-manager-routing.log` | лог routing |

## Зависимости

Для core-пакета нужны:

- `sing-box`
- `dnsmasq-full`

## Формат `config.json`

```json
{
  "singbox": {
    "log_level": "warn",
    "test_url": "https://www.gstatic.com/generate_204",
    "template": "/etc/horn-vpn-manager/sing-box.template.json"
  },
  "fetch": {
    "retries": 3,
    "timeout_seconds": 15,
    "parallelism": 2
  },
  "routing": {
    "domains": {
      "url": "https://raw.githubusercontent.com/itdoginfo/allow-domains/main/Russia/inside-dnsmasq-nfset.lst"
    },
    "subnets": {
      "urls": [
        "https://raw.githubusercontent.com/itdoginfo/allow-domains/main/Subnets/IPv4/telegram.lst",
        "https://raw.githubusercontent.com/itdoginfo/allow-domains/main/Subnets/IPv4/discord.lst"
      ],
      "manual_file": "/etc/horn-vpn-manager/lists/manual-ip.lst"
    }
  },
  "subscriptions": {
    "default": {
      "name": "Default",
      "url": "https://example.com/sub",
      "default": true,
      "enabled": true,
      "exclude": ["Россия", "traffic", "expire"],
      "interval": "5m",
      "tolerance": 100
    },
    "work": {
      "name": "Work",
      "url": "https://example.com/work-sub",
      "route": {
        "domains": ["jira.example.com", "confluence.example.com"],
        "domain_urls": [
          "https://raw.githubusercontent.com/itdoginfo/allow-domains/main/Services/discord.lst"
        ],
        "ip_cidrs": ["203.0.113.0/24"],
        "ip_urls": [
          "https://example.com/work-ips.lst"
        ]
      }
    }
  }
}
```

### Глобальные секции

#### `singbox`

- `log_level` — уровень логирования sing-box, по умолчанию `warn`
- `test_url` — URL для `urltest`, по умолчанию `https://www.gstatic.com/generate_204`
- `template` — путь к шаблону sing-box; если не указан, используется embedded шаблон из пакета

#### `fetch`

- `retries` — число повторов при ошибке скачивания (default: 3)
- `timeout_seconds` — timeout HTTP-запроса (default: 15)
- `parallelism` — максимум параллельных скачиваний (default: 2)

#### `routing`

- `domains.url` — dnsmasq-ready список доменов (одна запись на строку)
- `subnets.urls` — список URL с CIDR/подсетями
- `subnets.manual_file` — путь к файлу с ручными IP/CIDR (default: `/etc/horn-vpn-manager/lists/manual-ip.lst`)

#### `subscriptions`

`subscriptions` — это объект с постоянными ключами. Ключи используются как префиксы тегов и должны быть стабильными.

Поля подписки:

- `name` — человекочитаемое имя
- `url` — URL подписки
- `default` — ровно одна подписка должна иметь `true`; её outbound попадёт в `route.final`
- `enabled` — использовать ли подписку (default: `true`); дефолтная подписка не может быть отключена
- `include` — подстроки для включения узлов по имени (если задан, остальные фильтруются)
- `exclude` — подстроки для исключения узлов по имени
- `interval` — период `urltest` для multi-node подписки (default: `5m`)
- `tolerance` — tolerance `urltest` в мс (default: `100`)
- `retries` — override числа повторов для конкретной подписки
- `route` — routing policy этой подписки:
  - `domains` — список `domain_suffix` для route rule
  - `domain_urls` — URL-ы со списками доменов (по одному на строку); мерджатся с `domains`, дедуплицируются, валидируются
  - `ip_cidrs` — список CIDR для `ip_cidr` route rule
  - `ip_urls` — URL-ы со списками IP/CIDR; аналогично `domain_urls`

### Схема тегов

- single-node: `<id>-single`
- multi-node auto (urltest): `<id>-auto`
- multi-node manual (selector): `<id>-manual`
- отдельные узлы: `<id>-node-<hash>`

## Шаблон sing-box

Пакет поставляет шаблон по умолчанию `/usr/share/horn-vpn-manager/sing-box.template.default.json`.

Скопируйте его и кастомизируйте под себя:

```sh
cp /usr/share/horn-vpn-manager/sing-box.template.default.json /etc/horn-vpn-manager/sing-box.template.json
```

Укажите путь в `config.json` (`singbox.template`).

Шаблон по умолчанию содержит:

- inbound `tun0`
- `route.final` с outbound дефолтной подписки
- `experimental.clash_api` на `127.0.0.1:9090` (используется LuCI)
- `experimental.cache_file` для persist urltest results

## CLI

```sh
# Общая справка
vpn-manager help

# Подписки
vpn-manager subscriptions run [-c config] [-v] [--no-color]
vpn-manager subscriptions dry-run [-c config] [-v] [--no-color]

# Routing
vpn-manager routing run [-c config] [-v] [--no-color] [--with-subscriptions]
vpn-manager routing restore [-c config] [--no-color]

# Валидация конфига
vpn-manager check [-c config]

# Bootstrap (routing + subscriptions)
vpn-manager run [-c config]
```

Флаги:

- `-c / --config` — путь к конфигу (default: `/etc/horn-vpn-manager/config.json`)
- `-t / --template` — путь к шаблону sing-box (только для subscriptions)
- `-v / -vv / -vvv` — уровень детализации логов
- `--no-color` — отключить цвет (для cron)
- `--debug` — debug режим: конфиг/шаблон из директории бинарника, вывод в `./out`, без системных действий
- `--with-subscriptions` — для `routing run`: после routing скачать также списки для subscription route rules
- `--download-lists` — для `subscriptions run`: всегда скачивать свежие списки и кэшировать
- `--cached-lists` — для `subscriptions run`: использовать кэш (скачивать только при отсутвии кеша)

## Установка на роутер

### 1. Собрать пакеты

Для OpenWrt SNAPSHOT / APK:

```sh
make build
```

Для release SDK / IPK:

```sh
make build-ipk OPENWRT_RELEASE=23.05.5
```

Готовые артефакты будут в `bin/`.

### 2. Установить пакеты

Если вы собрали `.apk`:

```sh
scp bin/horn-vpn-manager-[0-9]*.apk root@192.168.1.1:/tmp/
ssh root@192.168.1.1 "apk add --allow-untrusted /tmp/horn-vpn-manager-[0-9]*.apk"
```

Если вы собрали `.ipk`:

```sh
scp bin/horn-vpn-manager_[0-9]*.ipk root@192.168.1.1:/tmp/
ssh root@192.168.1.1 "opkg install /tmp/horn-vpn-manager_[0-9]*.ipk"
```

Если нужен LuCI-пакет, установите его отдельно:

```sh
scp bin/horn-vpn-manager-luci-*.apk root@192.168.1.1:/tmp/
ssh root@192.168.1.1 "apk add --allow-untrusted /tmp/horn-vpn-manager-luci-*.apk"
```

### 3. Подготовить конфиг

```sh
ssh root@192.168.1.1 "cp /usr/share/horn-vpn-manager/config.example.json /etc/horn-vpn-manager/config.json"
```

Заполните реальные URL подписок и routing lists в `/etc/horn-vpn-manager/config.json`.

Опционально кастомизируйте шаблон sing-box:

```sh
ssh root@192.168.1.1 "cp /usr/share/horn-vpn-manager/sing-box.template.default.json /etc/horn-vpn-manager/sing-box.template.json"
```

### 4. Проверить без применения

```sh
ssh root@192.168.1.1 "vpn-manager subscriptions dry-run -v"
```

### 5. Применить

```sh
ssh root@192.168.1.1 "vpn-manager routing run -v"
ssh root@192.168.1.1 "vpn-manager subscriptions run -v"
```

## Автозапуск и cron

Встроенный init script:

```sh
/etc/init.d/horn-vpn-manager enable
/etc/init.d/horn-vpn-manager start
```

Пример cron для раздельного обновления:

```cron
# Подписки каждые 6 часов
0 */6 * * * /usr/bin/vpn-manager subscriptions run --no-color

# Routing lists раз в сутки
15 4 * * * /usr/bin/vpn-manager routing run --no-color
```

## LuCI

После установки `horn-vpn-manager-luci` в меню появится `Services → VPN management`.

Через LuCI можно:

- редактировать `config.json`
- запускать subscriptions/routing jobs и смотреть live log
- смотреть proxies из Clash API
- переключать manual selector для multi-node подписок
- запускать delay tests
- редактировать manual IPs и manual domains

## Локальная разработка

```sh
make help
make lint
make shell
make shell-ipk
```

`make lint` выполняет:

- `golangci-lint run` для Go кода
- `sh -n` / `shellcheck` для shell-скриптов

Для отладки без роутера используйте `--debug`:

```sh
./vpn-manager subscriptions dry-run --debug -v
./vpn-manager routing run --debug -v
```

В debug режиме конфиг берётся из директории бинарника, вывод идёт в `./out`, системные действия (sing-box, dnsmasq, firewall) не выполняются.
