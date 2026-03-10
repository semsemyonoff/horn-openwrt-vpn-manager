# vpnsub — обновление подписок sing-box на OpenWrt

Скрипт для автоматического обновления конфигурации [sing-box](https://sing-box.sagernet.org/) на роутере OpenWrt.

## Что делает скрипт

Для каждой подписки из `subs.json`:
1. Скачивает данные, автоматически определяет формат (raw VLESS или base64)
2. Фильтрует серверы по списку `exclude` (по подстроке в имени, регистронезависимо)
3. Первый URI подписки с `"default": true` становится дефолтным outbound-ом (fallback)
4. Для подписок с `domains` — генерирует `urltest`-группу и правило маршрутизации
5. Подставляет всё в шаблон `config.template.json`
6. Валидирует через `sing-box check`, бэкапит старый конфиг и применяет новый
7. Перезапускает sing-box

## Зависимости

OpenWrt 25.12 и выше.

Пакеты:

- `jq`
- `curl`
- `coreutils-base64`

```sh
apk add jq curl coreutils-base64
```

## Файлы

Все файлы должны лежать в `/etc/sing-box/` рядом со скриптом — пути не захардкожены, `subs.sh` определяет своё местоположение автоматически.

| Файл | Назначение |
|---|---|
| `subs.sh` | скрипт обновления |
| `subs.json` | конфигурация подписок (создаётся на роутере, не хранится в репозитории) |
| `subs.example.json` | пример конфигурации подписок |
| `config.template.json` | шаблон конфига sing-box |
| `config.json` | итоговый конфиг (генерируется) |
| `config.json.bak` | бэкап предыдущего конфига |
| `/tmp/sing-box-sub.log` | лог последнего запуска |

## Конфигурация subs.json

```json
{
  "subscriptions": [
    {
      "name": "vless-default",
      "url": "https://example.com/my-default-vless",
      "default": true
    },
    {
      "name": "Blanc",
      "url": "https://example.com/blanc/sub",
      "domains": [
        "chatgpt.com",
        "openai.com"
      ],
      "exclude": ["Россия", "traffic", "expire"]
    }
  ]
}
```

### Поля

**`log_level`** — уровень логирования sing-box: `trace`, `debug`, `info`, `warn`, `error`, `fatal`, `panic`. По умолчанию `warn`.

**`subscriptions`** — список подписок.

- `name` — имя подписки, используется как префикс тегов (`Blanc-1`, `Blanc-2`, ...)
- `url` — URL подписки; скрипт автоматически определяет формат ответа: сырые `vless://` URI или base64-encoded список
- `default` — если `true`, первый сервер подписки становится дефолтным outbound-ом (fallback для трафика, не попавшего под правила других подписок); поле `domains` не обязательно
- `domains` — домены, трафик к которым пойдёт через серверы этой подписки
- `exclude` — подстроки для фильтрации серверов по имени (регистронезависимо)

## Шаблон конфига

`config.template.json` — обычный конфиг sing-box, в котором три плейсхолдера:

| Плейсхолдер | Что подставляется |
|---|---|
| `"__LOG_LEVEL__"` | уровень логирования из поля `log_level` в `subs.json` |
| `"__DEFAULT_OUTBOUND__"` | VLESS outbound дефолтного сервера |
| `"__VLESS_OUTBOUNDS__"` | VLESS outbound-блоки всех серверов из подписок |
| `"__URLTEST_OUTBOUNDS__"` | urltest-группы (по одной на подписку) |
| `"__ROUTE_RULES__"` | правила маршрутизации по доменам |
| `"__DEFAULT_TAG__"` | тег дефолтного outbound-а (в поле `route.final`) |

Каждый плейсхолдер должен стоять на отдельной строке как строковое значение JSON.

## Флаги запуска

```sh
subs.sh [--dry-run|-n] [-v|-vv|-vvv]
```

| Флаг | Эффект |
|---|---|
| `--dry-run`, `-n` | Скачивает данные, генерирует конфиг и выводит его в stdout, но не применяет и не перезапускает sing-box |
| `-v` | Verbose: формат ответа (raw/base64), пропущенные серверы (SKIP), HTTP-коды ошибок |
| `-vv` | Verbose: + список принятых серверов (KEEP) с тегами |
| `-vvv` | Debug: + HTTP-код и размер ответа для каждой подписки, параметры каждого URI (addr, security, sni, fp, flow) |

Флаги комбинируются: `subs.sh --dry-run -vv`

## Установка

```sh
# Скопировать файлы на роутер
scp subs.sh config.template.json root@192.168.1.1:/etc/sing-box/

# Создать subs.json на основе примера и заполнить своими URL
cp subs.example.json /etc/sing-box/subs.json

# Сделать скрипт исполняемым
chmod +x /etc/sing-box/subs.sh

# Установить зависимости
apk add jq curl coreutils-base64

# Проверить без применения
/etc/sing-box/subs.sh --dry-run -v

# Запустить
/etc/sing-box/subs.sh
```

## Добавление в cron

Открыть редактор crontab:

```sh
crontab -e
```

Примеры расписания:

```
# Каждые 6 часов
0 */6 * * * /etc/sing-box/subs.sh

# Раз в сутки в 4:00
0 4 * * * /etc/sing-box/subs.sh

# Каждый час
0 * * * * /etc/sing-box/subs.sh
```

Убедиться, что cron запущен:

```sh
service cron enable
service cron start
```

Проверить лог после первого запуска по расписанию:

```sh
cat /tmp/sing-box-sub.log
```
