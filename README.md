# horn-vpn-manager

`horn-vpn-manager` — OpenWrt-пакет для управления VPN-подписками sing-box и маршрутизацией трафика через dnsmasq/nftables.

## Что есть в репозитории

- `horn-vpn-manager` — Go-бинарник `vpn-manager` для обновления подписок, генерации `sing-box` конфига и управления domain/IP lists
- `horn-vpn-manager-luci` — LuCI UI поверх rpcd backend
- `Makefile`, `Dockerfile`, `docker/entrypoint.sh` — локальная сборка `.apk` через OpenWrt SNAPSHOT SDK в контейнере

## Что делает core-пакет

`vpn-manager subscriptions run`:

1. Читает подписки из `config.json`
2. Скачивает каждую подписку по URL (или берёт узлы из `nodes`, если они заданы inline)
3. Автоопределяет формат: raw `vless://`, base64, base64url, gzip
4. Фильтрует узлы по `include` / `exclude` и отбрасывает узлы-дубликаты (одинаковые по всем параметрам outbound)
5. Для multi-node подписок создаёт стабильные node tags, `urltest`-группу `<id>-auto` и selector `<id>-manual`
6. Для single-node подписок создаёт прямой outbound `<id>-single`
7. Для подписок с `fallback` создаёт группу `<id>-fallback` с цепочкой резервных подписок
8. Собирает route rules по `route.domains`, `route.ip_cidrs` и загруженным спискам
9. Генерирует `sing-box` config из шаблона
10. Проверяет конфиг через `sing-box check`, сохраняет backup и перезапускает `sing-box`

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
| `/usr/share/horn-vpn-manager/sing-box.template.json` | шаблон sing-box по умолчанию |
| `/usr/share/horn-vpn-manager/config.example.json` | пример конфига |
| `/etc/sing-box/config.json` | сгенерированный sing-box config |
| `/tmp/horn-vpn-manager-subscriptions.log` | лог subscriptions |
| `/tmp/horn-vpn-manager-routing.log` | лог routing |

## Зависимости

Для core-пакета нужны:

- `sing-box` или `sing-box-extended`
- `dnsmasq-full`

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
    "personal": {
      "name": "Personal VPS",
      "nodes": [
        "vless://11111111-2222-3333-4444-555555555555@203.0.113.10:443?type=tcp&security=reality&sni=example.com&fp=chrome&pbk=PUBLIC_KEY&sid=00112233#Personal"
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

#### `routing`

- `domains.url` — dnsmasq-ready список доменов (одна запись на строку)
- `subnets.urls` — список URL с CIDR/подсетями
- `subnets.manual_file` — путь к файлу с ручными IP/CIDR (default: `/etc/horn-vpn-manager/lists/manual-ip.lst`)

#### `subscriptions`

`subscriptions` — это объект с постоянными ключами. Ключи используются как префиксы тегов и должны быть стабильными.

Поля подписки:

- `name` — человекочитаемое имя
- `url` — URL подписки; **взаимоисключающий** с `nodes`
- `nodes` — список inline `vless://` URI для своего узла (personal provider), когда публиковать
  subscription-эндпоинт незачем. Ни одного HTTP-запроса такая подписка не делает; `include` / `exclude`,
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

### Схема тегов

- single-node: `<id>-single`
- multi-node auto (urltest): `<id>-auto`
- multi-node manual (selector): `<id>-manual`
- отдельные узлы: `<id>-node-<hash>`
- fallback-цепочка: `<id>-fallback`

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
- `--logs` — писать вывод в лог-файл параллельно со stderr (см. ниже)
- `--debug` — debug режим: конфиг/шаблон из директории бинарника, вывод в `./out`, без системных действий
- `--with-subscriptions` — для `routing run`: после routing скачать также списки для subscription route rules
- `--download-lists` — для `subscriptions run`: всегда скачивать свежие списки и кэшировать
- `--cached-lists` — для `subscriptions run`: использовать кэш (скачивать только при отсутвии кеша)

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

### Автоматическая установка

Скрипт проверит и установит все зависимости (`sing-box`, `dnsmasq-full`), скачает `horn-vpn-manager` и опционально LuCI плагин:

```sh
sh -c "$(curl -fsSL https://raw.githubusercontent.com/semsemyonoff/horn-openwrt-vpn-manager/main/install.sh)"
```

Или через `wget`:

```sh
sh -c "$(wget -qO- https://raw.githubusercontent.com/semsemyonoff/horn-openwrt-vpn-manager/main/install.sh)"
```

Предварительный просмотр (без изменений):

```sh
sh -c "$(curl -fsSL https://raw.githubusercontent.com/semsemyonoff/horn-openwrt-vpn-manager/main/install.sh)" -- --dry-run
```

Неинтерактивная установка с параметрами:

```sh
sh -c "$(curl -fsSL https://raw.githubusercontent.com/semsemyonoff/horn-openwrt-vpn-manager/main/install.sh)" -- --with-sing-box-extend --with-dnsmasq --with-luci
```

Доступные флаги:

| Флаг | Описание |
|---|---|
| `--dry-run` | Показать что будет сделано, без изменений |
| `--with-sing-box` | Установить sing-box из репозитория OpenWrt |
| `--with-sing-box-extend` | Установить sing-box-extended с GitHub |
| `--with-dnsmasq` | Установить dnsmasq-full (заменит dnsmasq) |
| `--with-luci` | Установить LuCI плагин |
| `--no-luci` | Не устанавливать LuCI плагин |

### Ручная установка

#### 1. Установить зависимости

Установите `dnsmasq-full` (если установлен обычный `dnsmasq`, сначала удалите его):

```sh
apk del dnsmasq
apk add dnsmasq-full
```

Установите `sing-box` из репозитория:

```sh
apk add sing-box
```

Или установите [sing-box-extended](https://github.com/shtorm-7/sing-box-extended) вручную, скачав бинарник для вашей архитектуры из [релизов](https://github.com/shtorm-7/sing-box-extended/releases/latest) и поместив его в `/usr/bin/sing-box`.

Если init script sing-box отсутствует (`/etc/init.d/sing-box`), создайте его — см. `install.sh` для содержимого.

#### 2. Скачать и установить пакеты

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

#### 3. Настроить конфиг

```sh
cp /usr/share/horn-vpn-manager/config.example.json /etc/horn-vpn-manager/config.json
```

Заполните реальные URL подписок и routing lists в `/etc/horn-vpn-manager/config.json`.

Опционально кастомизируйте шаблон sing-box:

```sh
cp /usr/share/horn-vpn-manager/sing-box.template.json /etc/horn-vpn-manager/sing-box.template.json
```

#### 4. Проверить и запустить

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

## LuCI

После установки `horn-vpn-manager-luci` в меню появится `Services → VPN management`.

Вкладки в порядке отображения:

1. **Subscriptions** — редактирование подписок (`include`, `exclude`, `route.*` и т.д.)
2. **Routing** — глобальные routing sources (`domains.url`, `subnets.urls`)
3. **Sing-box template config** — редактирование шаблона sing-box (JSON merging, без плейсхолдеров)
4. **Additional domains** — ручные домены и IP/CIDR списки
5. **Sing-box logs** — просмотр логов sing-box
6. **Test** — delay tests и проверка прокси
7. **Run** — запуск подписок и routing с выбором флагов (`--cached-lists`, `--download-lists`, `--with-subscriptions`), dry-run режим, live log

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
```

Готовые артефакты появятся в `bin/`.

Для установки на роутер вручную:

```sh
scp bin/horn-vpn-manager-*-linux-<arch>.apk root@192.168.1.1:/tmp/
ssh root@192.168.1.1 "apk add --allow-untrusted /tmp/horn-vpn-manager-*.apk"
```

### Команды разработки

```sh
make help
make lint
make shell
```

`make lint` выполняет:

- `gofmt` — проверка форматирования Go кода
- `golangci-lint run` — статический анализ Go кода

### Debug режим

Для отладки без роутера используйте `--debug`:

```sh
./vpn-manager subscriptions dry-run --debug -v
./vpn-manager routing run --debug -v
```

В debug режиме конфиг берётся из директории бинарника, вывод идёт в `./out`, системные действия (sing-box, dnsmasq, firewall) не выполняются.
