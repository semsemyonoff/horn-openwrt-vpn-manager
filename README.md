# vpnsub — обновление подписок sing-box на OpenWrt

Скрипт для автоматического обновления конфигурации [sing-box](https://sing-box.sagernet.org/) на роутере OpenWrt.

## Что делает скрипт

1. Скачивает «дефолтный» VLESS-сервер по прямой ссылке (без base64)
2. Для каждой подписки из `subs.json`:
   - скачивает список серверов (base64-encoded VLESS URI)
   - фильтрует серверы по списку `exclude` (по подстроке в имени, регистронезависимо)
   - генерирует VLESS outbound-блоки для sing-box
   - создаёт `urltest`-группу для автовыбора лучшего сервера в подписке
   - создаёт правило маршрутизации — указанные домены идут через эту группу
3. Подставляет всё в шаблон `config.template.json`
4. Валидирует через `sing-box check`, бэкапит старый конфиг и применяет новый
5. Перезапускает sing-box

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

| Файл | Путь на роутере | Назначение |
|---|---|---|
| `subs.sh` | `/etc/sing-box/subs.sh` | скрипт обновления |
| `subs.json` | `/etc/sing-box/subs.json` | конфигурация подписок |
| `config.template.json` | `/etc/sing-box/config.template.json` | шаблон конфига sing-box |
| — | `/etc/sing-box/config.json` | итоговый конфиг (генерируется) |
| — | `/etc/sing-box/config.json.bak` | бэкап предыдущего конфига |
| — | `/tmp/sing-box-sub.log` | лог последнего запуска |

## Конфигурация subs.json

```json
{
  "default": {
    "name": "vless-default",
    "url": "https://example.com/my-default-vless"
  },
  "subscriptions": [
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

**`default`** — постоянный резервный сервер, используется как fallback для всего трафика, не попавшего под правила подписок.
- `name` — тег outbound-а в конфиге sing-box
- `url` — URL, возвращающий один сырой `vless://...` URI (без base64)

**`subscriptions`** — список подписок (опционально, можно не указывать).
- `name` — имя подписки, используется как префикс тегов (`Blanc-1`, `Blanc-2`, ...)
- `url` — URL подписки, возвращающий base64-encoded список VLESS URI
- `domains` — домены, трафик к которым пойдёт через серверы этой подписки
- `exclude` — подстроки для фильтрации серверов по имени (регистронезависимо)

## Шаблон конфига

`config.template.json` — обычный конфиг sing-box, в котором три плейсхолдера:

| Плейсхолдер | Что подставляется |
|---|---|
| `"__DEFAULT_OUTBOUND__"` | VLESS outbound дефолтного сервера |
| `"__VLESS_OUTBOUNDS__"` | VLESS outbound-блоки всех серверов из подписок |
| `"__URLTEST_OUTBOUNDS__"` | urltest-группы (по одной на подписку) |
| `"__ROUTE_RULES__"` | правила маршрутизации по доменам |
| `"__DEFAULT_TAG__"` | тег дефолтного outbound-а (в поле `route.final`) |

Каждый плейсхолдер должен стоять на отдельной строке как строковое значение JSON.

## Установка

```sh
# Скопировать файлы на роутер
scp subs.sh root@192.168.1.1:/etc/sing-box/subs.sh
scp subs.json root@192.168.1.1:/etc/sing-box/subs.json
scp config.template.json root@192.168.1.1:/etc/sing-box/config.template.json

# Сделать скрипт исполняемым
chmod +x /etc/sing-box/subs.sh

# Установить jq
apk add jq

# Запустить вручную для проверки
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
