# horn-vpn-manager

`horn-vpn-manager` — OpenWrt-пакет для управления VPN-подписками sing-box и маршрутизацией трафика через dnsmasq/nftables.

## Что есть в репозитории

- `horn-vpn-manager` — Go-бинарник `vpn-manager` для обновления подписок, генерации `sing-box` конфига и управления domain/IP lists
- `horn-vpn-manager-luci` — LuCI UI поверх rpcd backend
- `Makefile` + `scripts/` — локальная сборка `.apk` / `.ipk`: Go кросс-компиляция и упаковка без OpenWrt SDK
- `Dockerfile`, `docker/entrypoint.sh` — образ OpenWrt SNAPSHOT SDK; нужен только для `make shell`

## Что делает core-пакет

`vpn-manager subscriptions run`:

1. Читает подписки из `config.json`
2. Скачивает каждую подписку по URL (или берёт узлы из `nodes`, если они заданы inline)
3. Автоопределяет формат: raw-строки узлов, base64, base64url, gzip; строки схем, которых нет
   в [поддерживаемых протоколах](#поддерживаемые-протоколы-узлов), молча пропускаются
4. Фильтрует узлы по `include` / `exclude` и отбрасывает узлы-дубликаты (одинаковые по всем параметрам outbound)
5. Для multi-node подписок создаёт стабильные node tags, `urltest`-группу `<id>-auto` и selector `<id>-manual`
6. Для single-node подписок создаёт прямой outbound `<id>-single`
7. Для подписок с `fallback` создаёт группу `<id>-fallback` с цепочкой резервных подписок
8. Собирает route rules по `route.domains`, `route.ip_cidrs` и загруженным спискам
9. Генерирует `sing-box` config из шаблона
10. Проверяет конфиг через `sing-box check`; если результат совпадает с уже применённым и сервис
    запущен — перезапуск пропускается, иначе сохраняет backup и перезапускает `sing-box`
    (при неудачном рестарте откатывает предыдущий конфиг и поднимает сервис на нём)

`vpn-manager routing run`:

1. Скачивает dnsmasq domain list и subnet lists из `config.json`
2. Кэширует их в `/etc/horn-vpn-manager/lists/`; subnet-кэш перезаписывается только если скачались
   **все** URL — частичная выкачка не сужает уже применённый список
3. Собирает итоговый IP list с учётом `manual-ip.lst`
4. Обновляет dnsmasq/firewall (dnsmasq не перезапускается, если drop-in не изменился)

`vpn-manager routing restore` восстанавливает domain/IP lists из кэша без скачивания (для boot при отсутствии сети).

Init script `/etc/init.d/horn-vpn-manager` ждёт доступ в интернет до 60 с, затем запускает
`routing run --with-subscriptions` и `subscriptions run --cached-lists`. Если сети нет, он
восстанавливает domain/IP lists из кэша через `routing restore`. При отсутствии `config.json` (свежая
установка) скрипт молча завершается.

## Пути на роутере

| Путь | Назначение |
|---|---|
| `/usr/bin/vpn-manager` | единый CLI |
| `/etc/horn-vpn-manager/config.json` | основной конфиг |
| `/etc/horn-vpn-manager/subs-tags.json` | карта подписка → теги outbound (читает LuCI) |
| `/etc/horn-vpn-manager/lists/` | кэш domain/subnet lists |
| `/etc/horn-vpn-manager/lists/manual-ip.lst` | ручной список IP/CIDR |
| `/etc/horn-vpn-manager/lists/subscriptions/` | кэш route-списков подписок (`<kind>-<hash>.lst` + `.meta.json`) |
| `/etc/horn-vpn-manager/.run.lock` | лок одновременных запусков |
| `/usr/share/horn-vpn-manager/sing-box.template.json` | шаблон sing-box по умолчанию |
| `/usr/share/horn-vpn-manager/config.example.json` | пример конфига |
| `/etc/sing-box/config.json` | сгенерированный sing-box config |
| `/tmp/horn-vpn-manager-subscriptions.log` | лог subscriptions |
| `/tmp/horn-vpn-manager-routing.log` | лог routing |

## Зависимости

Для core-пакета нужны:

- `sing-box` или `sing-box-extended` вместе с рабочим `/etc/init.d/sing-box`
- `dnsmasq-full`
- `curl` — им init script проверяет наличие интернета при загрузке

LuCI-пакету дополнительно нужен `jq` (rpcd backend читает и пишет `config.json` через него).

`vpn-manager` спрашивает у init-скрипта `/etc/init.d/sing-box running`, чтобы не перезапускать сервис
при неизменившемся конфиге. Скрипт без действия `running` отвечает ошибкой, это читается как «сервис
не запущен», и перезапуск происходит всегда — то есть поведение просто откатывается к
безусловному, ничего не ломается.

Для использования `xhttp` нужно использовать [sing-box-extended](https://github.com/shtorm-7/sing-box-extended) вместо обычного.

`sing-box-extended` также обязателен, если хотя бы одна подписка использует `fallback`: outbound типа
`fallback` **отсутствует в upstream sing-box** и существует только в extended-сборке (проверено на
`sing-box-extended 1.13.18-extended-2.6.5`). Это осознанное отступление от правила «источник истины —
[официальная документация sing-box](https://sing-box.sagernet.org/configuration/)». На обычной сборке
`sing-box check` отклонит конфиг с `unknown outbound type: fallback`, `vpn-manager` не станет применять
такой конфиг и подскажет, что нужна extended-сборка; текущий рабочий конфиг при этом не меняется.

Пакет не объявляет `DEPENDS` на `sing-box`: выбор между `sing-box` и `sing-box-extended` остаётся за
пользователем (пакеты конфликтуют, а extended обычно ставится вручную), поэтому жёсткая зависимость
ломала бы установку. Требование extended-сборки проверяется в рантайме через `sing-box check`.

## Формат `config.json`

```json
{
  "singbox": {
    "log_level": "warn",
    "test_url": "https://www.gstatic.com/generate_204",
    "template": "/etc/horn-vpn-manager/sing-box.template.json",
    "connect_timeout": "3s"
  },
  "fetch": {
    "retries": 3,
    "timeout_seconds": 15,
    "parallelism": 2,
    "list_cache_ttl": "6h"
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
    "personal": {
      "name": "Personal VPS",
      "nodes": [
        "vless://11111111-2222-3333-4444-555555555555@203.0.113.10:443?type=tcp&security=reality&sni=example.com&fp=chrome&pbk=PUBLIC_KEY&sid=00112233#Personal",
        "hysteria2://AUTH_PASSWORD@203.0.113.10:8443?sni=example.com&obfs=salamander&obfs-password=OBFS_PASSWORD#Personal+HY2"
      ],
      "default": true,
      "enabled": true,
      "fallback": {
        "subscriptions": ["provider"],
        "blacklist_timeout": "1m"
      }
    },
    "provider": {
      "name": "Provider",
      "url": "https://example.com/sub",
      "exclude": ["Россия", "traffic", "expire"],
      "interval": "5m",
      "tolerance": 300
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
- `connect_timeout` — таймаут установки соединения (`time.ParseDuration`, например `3s`, должен быть
  положительным), проставляется
  на каждый node outbound; если пусто, поле не выводится и действует дефолт sing-box. Полезен вместе с
  `fallback`: без него зависший узел отдаёт `i/o timeout` через ~5 с, и ровно на столько же
  откладывается переключение на резерв

#### `fetch`

- `retries` — число повторов при ошибке скачивания (default: 3)
- `timeout_seconds` — timeout HTTP-запроса (default: 15)
- `parallelism` — максимум параллельных скачиваний (default: 2)
- `list_cache_ttl` — сколько кэшированный route-список (`route.domain_urls` / `route.ip_urls`)
  отдаётся из кэша без проверки на сервере при `subscriptions run --cached-lists`
  (`time.ParseDuration`, default: `6h`). Старше TTL — список **перепроверяется** условным запросом
  (`If-None-Match` / `If-Modified-Since`): 304 оставляет сохранённые байты и не качает тело, 200
  обновляет кэш. `"0"` — перепроверять на каждом запуске. Кэш остаётся fallback'ом, если запрос
  не прошёл

#### `routing`

- `domains.url` — dnsmasq-ready список доменов (одна запись на строку)
- `subnets.urls` — список URL с CIDR/подсетями
- `subnets.manual_file` — путь к файлу с ручными IP/CIDR (default: `/etc/horn-vpn-manager/lists/manual-ip.lst`)

#### `subscriptions`

`subscriptions` — это объект с постоянными ключами. Ключи используются как префиксы тегов и должны быть стабильными.

Поля подписки:

- `name` — человекочитаемое имя
- `url` — URL подписки; **взаимоисключающий** с `nodes`
- `nodes` — список inline URI узлов любой [поддерживаемой схемы](#поддерживаемые-протоколы-узлов)
  для своего узла (personal provider), когда публиковать subscription-эндпоинт незачем. Список может
  смешивать протоколы. Ни одного HTTP-запроса такая подписка не делает; `include` / `exclude`,
  `route`, `default` и `fallback` работают так же, как у подписки с `url`. У включённой подписки должно
  быть ровно одно из `url` / `nodes` (пустая строка в `url` считается отсутствием)
- `default` — ровно одна подписка должна иметь `true`; её outbound попадёт в `route.final`
- `enabled` — использовать ли подписку (default: `true`); дефолтная подписка не может быть отключена
- `include` — подстроки для включения узлов по имени (если задан, остальные фильтруются)
- `exclude` — подстроки для исключения узлов по имени
- `interval` — период `urltest` для multi-node подписки (`time.ParseDuration`, должен быть
  положительным; default: `5m`)
- `tolerance` — tolerance `urltest` в мс (default: `100`). Группы `urltest` и `selector` теперь
  генерируются с `interrupt_exist_connections: true`: при пересоборе выбора активные соединения со
  старым узлом рвутся, а не висят до таймаута. Для `urltest` это касается и «безобидных»
  перевыборов по задержке, поэтому **рекомендуется поднять `tolerance` примерно до `300`** — при
  дефолтных `100` и типичном разбросе узлов 100–230 мс `urltest` будет переключаться на шуме и рвать
  живые загрузки, стримы и WebSocket-соединения. С бо́льшим tolerance перевыбор происходит только при
  реальной деградации, где обрыв как раз и нужен
- `retries` — override числа повторов для конкретной подписки
- `fallback` — цепочка резервных подписок, см. [Fallback-цепочки](#fallback-цепочки)
- `route` — routing policy этой подписки:
  - `domains` — список `domain_suffix` для route rule
  - `domain_urls` — URL-ы со списками доменов (по одному на строку); мерджатся с `domains`, дедуплицируются, валидируются
  - `ip_cidrs` — список CIDR для `ip_cidr` route rule
  - `ip_urls` — URL-ы со списками IP/CIDR; аналогично `domain_urls`

### Поддерживаемые протоколы узлов

Узлы приходят либо из подписки, либо из inline-списка `nodes`, и в обоих случаях разбираются одним
диспетчером схем. Сопоставление схемы **регистрозависимое**: `VLESS://` — неизвестная схема.

| Схема | Протокол | Заметки |
| --- | --- | --- |
| `vless://` | VLESS | TLS / Reality, транспорты tcp, ws, grpc, http (h2), xhttp |
| `hysteria2://`, `hy2://` | Hysteria2 | обе схемы официальные и равнозначны |

Особенности hysteria2 URI:

- **порт необязателен и по умолчанию равен `443`**
- auth — это **весь** userinfo-компонент, он может содержать двоеточие (`user:password`)
- параметры: `sni`, `insecure`, `alpn`, `obfs`, `obfs-password`, `upmbps`, `downmbps`
  (последние три — клиентские расширения, а не часть URI-спеки)
- если не задать ни `upmbps`, ни `downmbps`, sing-box использует BBR вместо Brutal
- поддерживается только обфускация `salamander`: любое другое значение `obfs` (включая `gecko` из
  спеки), а также `salamander` с пустым паролем — узел отбрасывается с предупреждением, вся подписка
  при этом не падает
- port hopping (`mport` / `server_ports` / `hop_interval`), `pinSHA256` и `ech` не поддерживаются

Узел неизвестной схемы в скачанной подписке пропускается молча — провайдеры регулярно отдают
протоколы, которых тут нет, и ронять из-за этого всю подписку было бы хуже. В inline-списке `nodes`
такой URI, наоборот, отвергается на этапе валидации конфига.

### Схема тегов

- single-node: `<id>-single`
- multi-node auto (urltest): `<id>-auto`
- multi-node manual (selector): `<id>-manual`
- отдельные узлы: `<id>-node-<hash>`
- fallback-цепочка: `<id>-fallback`

> **Обновление.** С добавлением новых схем подписка, в которой раньше распознавался ровно один
> `vless://` узел, а теперь распознаётся ещё и hysteria2, становится multi-node: её итоговый тег
> меняется с `<id>-single` на `<id>-manual`, сохранённый выбор в selector и запись в `clash.db`
> перестают резолвиться, и узел нужно один раз выбрать заново в LuCI. `vpn-manager` пишет об этом
> предупреждение в лог с именем подписки. Это разовое событие на подписку, а не ошибка.

### Fallback-цепочки

`fallback` объявляется на **любой** подписке (не только на дефолтной) и содержит упорядоченный список
id резервных подписок:

```json
"fallback": {
  "subscriptions": ["backup1", "backup2"],
  "blacklist_timeout": "1m"
}
```

Генерируется группа `<id>-fallback`, в которой первым идёт собственный итоговый тег подписки
(`<id>-single` или `<id>-manual`), затем итоговые теги резервных подписок в объявленном порядке. Если
резервная подписка сама объявляет цепочку, в группу попадает её `<backup>-fallback`.

Что меняет цепочка:

- у **дефолтной** подписки — `route.final` начинает указывать на `<id>-fallback`;
- у **недефолтной** — её route rules начинают указывать на `<id>-fallback`, `route.final` не трогается.
  Если у такой подписки нет ни `route`, ни ссылки на неё из чужой цепочки, в неё ничего не
  направляется и цепочка ни на что не влияет — `vpn-manager` пишет об этом предупреждение в лог.

Поведение:

- новое соединение сначала идёт через основную подписку;
- при ошибке дозвона основной outbound помечается недоступным на `blacklist_timeout` (если поле не
  задано, действует дефолт sing-box), соединение повторяется через следующий в цепочке;
- пока действует blacklist, новые соединения идут через резерв;
- после истечения таймаута следующее соединение снова пробует основной outbound;
- **уже установленные соединения между провайдерами не переносятся**, а переключение на резерв
  **меняет публичный исходящий IP** — сессии, привязанные к IP (банк-клиенты, авторизации), придётся
  переустанавливать.

Ограничения (проверяются `vpn-manager check` и при сохранении из LuCI):

- каждый id из `subscriptions` должен существовать и быть включённым, ссылки на саму себя запрещены;
- дубликаты внутри одной цепочки и пустой список запрещены;
- циклы любой длины (`a → b → a`, `a → b → c → a`) запрещены;
- `blacklist_timeout`, если задан, разбирается через `time.ParseDuration` и должен быть
  положительным (`0` и отрицательные значения отклоняются: запись в blacklist истекала бы раньше,
  чем добавляется).

Если резервная подписка не дала ни одного узла (например, не скачалась), она молча выбывает из цепочки
с предупреждением в логе — регенерация конфига из-за резерва не падает. Если выбыли все резервы,
группа не создаётся и подписка остаётся со своим обычным тегом.

Требуется `sing-box-extended` — см. [Зависимости](#зависимости).

## Шаблон sing-box

Пакет поставляет шаблон по умолчанию `/usr/share/horn-vpn-manager/sing-box.template.json`.

Скопируйте его и кастомизируйте под себя:

```sh
cp /usr/share/horn-vpn-manager/sing-box.template.json /etc/horn-vpn-manager/sing-box.template.json
```

Укажите путь в `config.json` (`singbox.template`).

Шаблон по умолчанию содержит:

- inbound `tun0`
- `route.final` с outbound дефолтной подписки
- `experimental.clash_api` на `127.0.0.1:9090` (используется LuCI)
- `experimental.cache_file` для persist urltest results

## CLI

```sh
# Справка (общая и по каждой команде)
vpn-manager help
vpn-manager subscriptions --help

# Подписки
vpn-manager subscriptions run [-c config] [-t template] [-v] [--no-color] [--logs] [--debug]
                             [--cached-lists | --download-lists]
vpn-manager subscriptions dry-run [те же флаги]

# Routing
vpn-manager routing run [-c config] [-v] [--no-color] [--logs] [--debug] [--with-subscriptions]
vpn-manager routing restore [-c config] [-v] [--no-color] [--logs] [--debug]

# Валидация конфига
vpn-manager check [-c config] [-v] [--no-color]

# Bootstrap (routing + subscriptions)
vpn-manager run [-c config] [-t template] [-v] [--no-color] [--logs] [--debug]

# Версия
vpn-manager version
```

Общие флаги:

- `-c / --config` — путь к конфигу (default: `/etc/horn-vpn-manager/config.json`)
- `-v / -vv / -vvv` — уровень детализации логов
- `--no-color` — отключить цвет (для cron)
- `--logs` — писать вывод в лог-файл параллельно со stderr (см. [Логи](#логи))
- `--debug` — debug режим: конфиг/шаблон из директории бинарника, вывод в `./out`, без системных действий

Флаги отдельных команд:

- `-t / --template` — путь к шаблону sing-box (`subscriptions`, `run`)
- `--with-subscriptions` — для `routing run`: после routing скачать также списки для subscription route rules
- `--download-lists` — для `subscriptions`: всегда скачивать свежие списки и кэшировать
- `--cached-lists` — для `subscriptions`: предпочитать кэш. Копия моложе
  [`fetch.list_cache_ttl`](#fetch) отдаётся как есть, более старая — перепроверяется условным
  запросом, так что изменившийся список подхватывается сразу, а не на следующем прогоне

`vpn-manager check` из общих флагов принимает только `-c`, `-v` и `--no-color`.

### Одновременные запуски

Каждая команда, которая пишет состояние (`routing run`, `routing restore`, `subscriptions run`,
`subscriptions dry-run`), берёт эксклюзивный flock на `<config dir>/.run.lock`. `routing` и
`subscriptions` делят кэш route-списков и обе трогают системные сервисы: если они пересекутся,
подписки соберут конфиг из копии списка, которую routing прямо сейчас заменяет, и применённый
конфиг окажется на ревизию позади — без единой строчки в логах. Если лок занят, команда ждёт до
пяти минут и затем завершается с ошибкой (`another vpn-manager run is in progress`), а не портит
состояние параллельно. `vpn-manager check` лок не берёт.

## Логи

При передаче флага `--logs` бинарник дублирует весь вывод в файл на диске (вывод при этом также идёт в stderr):

| Команда | Лог-файл |
|---|---|
| `subscriptions run` / `subscriptions dry-run` | `/tmp/horn-vpn-manager-subscriptions.log` |
| `routing run` / `routing restore` | `/tmp/horn-vpn-manager-routing.log` |

Файл усекается при каждом запуске с `--logs`, то есть хранит только последний запуск.

LuCI всегда передаёт `--logs` при запуске команд из интерфейса — отключить это поведение через UI нельзя. Вкладка **Run** отображает содержимое этих файлов в реальном времени.

При запуске из cron рекомендуется также передавать `--logs`, чтобы последний прогон всегда был доступен в LuCI (см. пример в разделе [Автозапуск и cron](#автозапуск-и-cron)).

## Установка на роутер

### 1. Установить зависимости

Установите `dnsmasq-full` (если установлен обычный `dnsmasq`, сначала удалите его):

```sh
apk del dnsmasq
apk add dnsmasq-full
```

Установите `sing-box` из репозитория:

```sh
apk add sing-box
```

Пакет из репозитория приносит с собой `/etc/init.d/sing-box`, который нужен `vpn-manager` для
перезапуска и проверки состояния сервиса.

Если нужен `xhttp` или `fallback`, поверх поставьте
[sing-box-extended](https://github.com/shtorm-7/sing-box-extended): скачайте бинарник для вашей
архитектуры из [релизов](https://github.com/shtorm-7/sing-box-extended/releases/latest) и положите его
в `/usr/bin/sing-box` вместо штатного. Init script при этом остаётся от пакета из репозитория.

### 2. Скачать и установить пакеты

Скачайте `.apk` файлы для вашей архитектуры из [релизов](https://github.com/semsemyonoff/horn-openwrt-vpn-manager/releases/latest).

Доступные архитектуры: `amd64`, `arm64`, `armv7`, `mips-softfloat`, `mipsle-softfloat`.

Установите core-пакет:

```sh
# скачайте horn-vpn-manager-*-linux-<arch>.apk на роутер
apk add --allow-untrusted /tmp/horn-vpn-manager-*.apk
```

Опционально установите LuCI плагин (архитектуронезависимый):

```sh
# скачайте horn-vpn-manager-luci-*.apk на роутер
apk add --allow-untrusted /tmp/horn-vpn-manager-luci-*.apk
```

### 3. Настроить конфиг

```sh
cp /usr/share/horn-vpn-manager/config.example.json /etc/horn-vpn-manager/config.json
```

Заполните реальные URL подписок и routing lists в `/etc/horn-vpn-manager/config.json`.

Опционально кастомизируйте шаблон sing-box:

```sh
cp /usr/share/horn-vpn-manager/sing-box.template.json /etc/horn-vpn-manager/sing-box.template.json
```

### 4. Проверить и запустить

```sh
vpn-manager check
vpn-manager subscriptions dry-run -v
vpn-manager run -v
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
0 */6 * * * /usr/bin/vpn-manager subscriptions run --no-color --logs

# Routing lists раз в сутки
15 4 * * * /usr/bin/vpn-manager routing run --no-color --logs
```

Не ставьте `routing run --with-subscriptions` и `subscriptions run` на одну и ту же минуту. Легко
сделать это случайно: `0 */6` и `0 */12` совпадают каждые 12 часов. Они делят кэш route-списков,
поэтому второй запуск упрётся в [лок](#одновременные-запуски) и будет ждать, пока первый закончит —
конфиг он соберёт из уже обновлённого кэша, то есть корректно, но прогон растянется. Разведите их по
времени (как в примере выше) либо запускайте `vpn-manager run`, который выполняет обе фазы
последовательно в одном процессе.

## LuCI

После установки `horn-vpn-manager-luci` в меню появится `Services → VPN management`.

Вкладки в порядке отображения:

1. **Subscriptions** — редактирование подписок (`include`, `exclude`, `route.*` и т.д.)
2. **Routing** — глобальные routing sources (`domains.url`, `subnets.urls`)
3. **Run** — запуск подписок и routing с выбором флагов (`--cached-lists`, `--download-lists`, `--with-subscriptions`), dry-run режим, live log
4. **Sing-box template config** — редактирование шаблона sing-box (JSON merging, без плейсхолдеров)
5. **Additional domains** — ручные домены и IP/CIDR списки
6. **Sing-box logs** — просмотр логов sing-box
7. **Test** — delay tests и проверка прокси

Через LuCI можно:

- редактировать `config.json` (subscriptions с полями `include` и `exclude`, routing, singbox settings)
- переключать подписку между источниками URL и inline `nodes`, задавать `fallback`-цепочку с
  `blacklist_timeout` и глобальный `singbox.connect_timeout`
- экспортировать и импортировать конфиг (кнопки "Export config" / "Import config" на любой вкладке)
- запускать подписки из вкладки **Run**: выбор `--cached-lists` / `--download-lists`, dry-run режим, live log
- запускать routing из вкладки **Run**: флаг `--with-subscriptions`, live log
- смотреть proxies из Clash API
- переключать manual selector для multi-node подписок
- запускать delay tests
- редактировать manual IPs и manual domains

## Локальная разработка

### Сборка пакетов

```sh
make build          # собрать .apk для текущей платформы
make build-all      # собрать .apk для всех платформ
make build-core-all # собрать только core для всех платформ
make build-ipk-all  # то же в .ipk для OpenWrt < 25 с opkg
```

Готовые артефакты появятся в `bin/`. OpenWrt SDK не нужен: core кросс-компилируется локальным Go, а
упаковкой занимаются скрипты в `scripts/` — им нужен запущенный Docker, потому что `apk mkpkg` и
GNU `ar`/`tar` вызываются внутри `alpine:latest`. Целевая платформа выводится из `TARGET`
(`make build-core TARGET=ath79/generic`), список платформ для `*-all` — в `ALL_PLATFORMS`.

Для установки на роутер вручную:

```sh
scp bin/horn-vpn-manager-*-linux-<arch>.apk root@192.168.1.1:/tmp/
ssh root@192.168.1.1 "apk add --allow-untrusted /tmp/horn-vpn-manager-*.apk"
```

### Команды разработки

```sh
make help
make lint       # gofmt -l + golangci-lint run
make go-test    # go test ./... -count=1
make luci-test
make shell      # интерактивный shell внутри контейнера с OpenWrt SDK
```

`make luci-test` выполняет тесты LuCI-вьюхи (`node --test`) и rpcd-бэкенда (`dash`) плюс
синтаксические гейты `node --check` / `dash -n`; нужны `node` и `dash`.

### Релизы

Релиз собирает GitHub Actions по пушу тега:

```sh
# 1. поднять PKG_VERSION в обоих Makefile'ах (horn-vpn-manager и horn-vpn-manager-luci)
# 2. при необходимости написать docs/release-notes/v2.3.0.md — текст попадёт над ченджлогом
git commit -am "build: bump version to 2.3.0"
git tag v2.3.0 && git push --follow-tags
```

Workflow прогоняет lint и тесты, проверяет, что тег совпадает с `PKG_VERSION`, собирает `.apk` и
`.ipk` для всех пяти платформ плюс оба LuCI-пакета, генерирует ченджлог из коммитов (git-cliff,
конфиг — `cliff.toml`) и создаёт **черновик** релиза с артефактами и `SHA256SUMS`. Опубликовать
черновик нужно руками.

### Debug режим

Для отладки без роутера используйте `--debug`:

```sh
./vpn-manager subscriptions dry-run --debug -v
./vpn-manager routing run --debug -v
```

В debug режиме конфиг берётся из директории бинарника, вывод идёт в `./out`, системные действия (sing-box, dnsmasq, firewall) не выполняются.
