# horn-vpn-manager / vpnsub

`horn-vpn-manager` — набор OpenWrt-пакетов для работы с VPN-подписками sing-box и списками доменов/IP для маршрутизации. Репозиторий содержит core-пакет, LuCI-интерфейс и Docker-based сборку пакетов.

## Что есть в репозитории

- `horn-vpn-manager` — CLI и shell-скрипты для обновления подписок, генерации `sing-box` конфига и загрузки domain/subnet lists
- `horn-vpn-manager-luci` — LuCI UI поверх rpcd backend
- `Makefile`, `Dockerfile`, `docker/entrypoint.sh` — локальная сборка `.apk` и `.ipk` через OpenWrt SDK в контейнере

## Что делает core-пакет

`vpn-manager subscriptions`:

1. Скачивает каждую подписку из `subs.json`
2. Автоопределяет формат ответа: raw `vless://` или base64
3. Фильтрует узлы по `exclude` без учёта регистра
4. Для multi-node подписок создаёт стабильные node tags, `urltest`-группу `<id>-auto` и selector `<id>-manual`
5. Для single-node подписок создаёт прямой outbound `<id>-single`
6. Собирает route rules по `domains` и `ip`
7. Подставляет данные в `config.template.json`
8. Проверяет конфиг через `sing-box check`, сохраняет backup и перезапускает `sing-box`

`vpn-manager domains`:

1. Читает `domains.json`
2. Скачивает dnsmasq domain list и один или несколько subnet lists
3. Кэширует их в `/etc/horn-vpn-manager/lists/`
4. Собирает итоговый IP list с учётом `manual-ip.lst`
5. Обновляет dnsmasq/firewall
6. Умеет делать `restore` из кэша на boot

Init script `/etc/init.d/horn-vpn-manager` ждёт доступ в интернет, затем запускает `domains run` и `subscriptions run`. Если сети нет, он восстанавливает domain/IP lists из кэша.

## Пути на роутере

| Путь | Назначение |
|---|---|
| `/usr/bin/vpn-manager` | единый CLI |
| `/usr/libexec/horn-vpn-manager/subs.sh` | обновление подписок |
| `/usr/libexec/horn-vpn-manager/getdomains.sh` | загрузка domain/IP lists |
| `/etc/horn-vpn-manager/subs.json` | конфиг подписок |
| `/etc/horn-vpn-manager/domains.json` | конфиг domain/subnet lists |
| `/etc/horn-vpn-manager/config.template.json` | рабочий шаблон sing-box |
| `/usr/share/horn-vpn-manager/config.template.default.json` | шаблон по умолчанию из пакета |
| `/etc/horn-vpn-manager/lists/` | кэш списков и `manual-ip.lst` |
| `/etc/horn-vpn-manager/subs-tags.json` | tag-to-name mapping для LuCI |
| `/etc/sing-box/config.json` | сгенерированный sing-box config |
| `/tmp/horn-vpn-manager-sub.log` | лог subscriptions |
| `/tmp/horn-vpn-manager-domains.log` | лог domains |

## Зависимости

Для core-пакета нужны:

- `jq`
- `curl`
- `coreutils-base64`
- `sing-box`
- `dnsmasq-full`

Для LuCI-пакета дополнительно нужен `luci-base`.

Установка runtime-зависимостей зависит от вашего OpenWrt image и package manager. Сам пакет `horn-vpn-manager` их не подтягивает автоматически на этапе сборки.

## Формат `subs.json`

`subscriptions` здесь не массив, а объект с постоянными ключами. Эти ключи используются как префиксы тегов и должны быть стабильными.

```json
{
  "log_level": "warn",
  "test_url": "https://www.gstatic.com/generate_204",
  "retries": 3,
  "subscriptions": {
    "default": {
      "name": "vless-default",
      "url": "https://example.com/my-default-vless",
      "default": true,
      "exclude": ["Россия", "traffic", "expire"]
    },
    "blanc": {
      "name": "Blanc",
      "url": "https://example.com/blanc/sub",
      "domains": [
        "chatgpt.com",
        "openai.com",
        "oaiusercontent.com"
      ],
      "exclude": ["Россия", "traffic", "expire"]
    },
    "corpnet": {
      "name": "CorpNet",
      "url": "https://example.com/corpnet/sub",
      "ip": [
        "10.0.0.0/8",
        "172.16.0.0/12",
        "192.168.0.0/16"
      ]
    }
  }
}
```

### Глобальные поля

- `log_level` — уровень логирования sing-box, по умолчанию `warn`
- `test_url` — URL для `urltest`, по умолчанию `https://www.gstatic.com/generate_204`
- `retries` — глобальное число повторов при скачивании подписки
- `subscriptions` — map подписок по stable id

### Поля подписки

- `name` — человекочитаемое имя
- `url` — URL подписки
- `default` — ровно одна подписка должна иметь `true`; её outbound попадёт в `route.final`
- `domains` — список `domain_suffix` для route rule
- `ip` — список CIDR для `ip_cidr` route rule
- `exclude` — подстроки для фильтрации имён узлов
- `interval` — период `urltest` для multi-node подписки, по умолчанию `5m`
- `tolerance` — tolerance `urltest`, по умолчанию `100`
- `retries` — override для конкретной подписки

### Схема тегов

- single-node: `<id>-single`
- multi-node auto: `<id>-auto`
- multi-node manual selector: `<id>-manual`
- отдельные узлы: `<id>-node-<hash>`

## Формат `domains.json`

```json
{
  "domains_url": "https://raw.githubusercontent.com/itdoginfo/allow-domains/main/Russia/inside-dnsmasq-nfset.lst",
  "subnet_urls": [
    "https://raw.githubusercontent.com/itdoginfo/allow-domains/refs/heads/main/Subnets/IPv4/telegram.lst",
    "https://raw.githubusercontent.com/itdoginfo/allow-domains/refs/heads/main/Subnets/IPv4/discord.lst"
  ]
}
```

- `domains_url` — dnsmasq-ready список доменов
- `subnet_urls` — список URL с CIDR/подсетями
- `manual-ip.lst` в `/etc/horn-vpn-manager/lists/` дописывается в итоговый `vpn-ip-list.lst`
- LuCI также умеет редактировать manual domains через `/etc/config/dhcp` (`config ipset` с именем `vpn_domains`)

## Шаблон `config.template.json`

Поддерживаются плейсхолдеры:

- `__LOG_LEVEL__`
- `__VLESS_OUTBOUNDS__`
- `__GROUP_OUTBOUNDS__`
- `__ROUTE_RULES__`
- `__DEFAULT_TAG__`

Плейсхолдеры должны оставаться строковыми JSON-значениями на отдельных строках. Старые имена вроде `__DEFAULT_OUTBOUND__` или `__URLTEST_OUTBOUNDS__` больше не используются.

Шаблон по умолчанию включает:

- inbound `tun0`
- `route.final` с подстановкой дефолтного outbound tag
- `experimental.clash_api` на `127.0.0.1:9090`

LuCI использует Clash API для показа статуса прокси, измерения задержек и ручного переключения узлов, поэтому при изменении шаблона это нужно сохранить.

## CLI

Общая справка:

```sh
vpn-manager help
```

Подписки:

```sh
vpn-manager subscriptions run [--no-color] [-v|-vv|-vvv]
vpn-manager subscriptions dry-run [--no-color] [-v|-vv|-vvv]
vpn-manager subscriptions help
```

Domain/IP lists:

```sh
vpn-manager domains run [--no-color] [-v|-vv|-vvv]
vpn-manager domains restore [--no-color] [-v|-vv|-vvv]
vpn-manager domains help
```

Логи:

- subscriptions: `/tmp/horn-vpn-manager-sub.log`
- domains: `/tmp/horn-vpn-manager-domains.log`

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
scp bin/horn-vpn-manager-luci-* root@192.168.1.1:/tmp/
ssh root@192.168.1.1 "apk add --allow-untrusted /tmp/horn-vpn-manager-luci-*.apk"
```

или для `.ipk`:

```sh
scp bin/horn-vpn-manager-luci_* root@192.168.1.1:/tmp/
ssh root@192.168.1.1 "opkg install /tmp/horn-vpn-manager-luci_*.ipk"
```

### 3. Подготовить конфиги

```sh
ssh root@192.168.1.1 "
  cp /usr/share/horn-vpn-manager/subs.example.json /etc/horn-vpn-manager/subs.json
  cp /usr/share/horn-vpn-manager/domains.example.json /etc/horn-vpn-manager/domains.json
"
```

После этого заполните реальные URL и списки в `/etc/horn-vpn-manager/subs.json` и `/etc/horn-vpn-manager/domains.json`.

### 4. Проверить без применения

```sh
ssh root@192.168.1.1 "vpn-manager subscriptions dry-run -v"
```

### 5. Применить

```sh
ssh root@192.168.1.1 "vpn-manager domains run -v"
ssh root@192.168.1.1 "vpn-manager subscriptions run -v"
```

## Автозапуск и cron

Встроенный init script:

```sh
/etc/init.d/horn-vpn-manager enable
/etc/init.d/horn-vpn-manager start
```

Если нужен периодический refresh, используйте cron:

```sh
crontab -e
```

Пример:

```cron
# Подписки каждые 6 часов
0 */6 * * * /usr/bin/vpn-manager subscriptions run --no-color

# Domain/IP lists раз в сутки
15 4 * * * /usr/bin/vpn-manager domains run --no-color
```

Проверка логов:

```sh
cat /tmp/horn-vpn-manager-sub.log
cat /tmp/horn-vpn-manager-domains.log
```

## LuCI

После установки `horn-vpn-manager-luci` в меню появится:

- `Services -> VPN management`

Через LuCI можно:

- редактировать `subs.json` и `domains.json`
- запускать subscriptions/domains jobs и смотреть live log
- править `config.template.json`
- смотреть proxies из Clash API
- переключать manual selector для multi-node подписок
- запускать delay tests
- редактировать manual IPs и manual domains

## Локальная разработка

Полезные команды:

```sh
make help
make lint
make shell
make shell-ipk
```

`make lint` выполняет:

- `sh -n` для shell-скриптов
- `shellcheck`, если он установлен
- `jq`-валидацию package JSON files

Не запускайте `subs.sh` и `getdomains.sh` напрямую на обычном macOS/Linux host: они ожидают OpenWrt paths, dnsmasq, sing-box и init scripts.
