# Диаграммы взаимодействий и потоков данных плагина

Локальные заметки для `eduardkolotushin-forecast-app`, вложенной панели `eduardkolotushin-forecast-panel` и вложенного источника `eduardkolotushin-forecast-datasource`.

Источник истины по поведению — `docs/ARCHITECTURE.md` и код в `src/forecast-panel/`, `src/forecast-datasource/` и `pkg/plugin/`.

| Файл | Диаграмма |
| --- | --- |
| [01-system-context.md](01-system-context.md) | Плагин, Grafana, источники, снимок в Postgres, планировщик, общая таблица `forecast.retrain` со worker, alerting через Forecast datasource, соседние библиотеки |
| [02-plugin-internals.md](02-plugin-internals.md) | App, вложенная панель, два процесса `gpx_forecast`, планировщик, ресурс `/schedules`, лимитер, кэш SnapshotStore |
| [03-overlay-sequence.md](03-overlay-sequence.md) | Оверлей: проба снимка затем обучение только при miss, Retrain или должном расписании; отмена устаревшей загрузки |
| [04-three-clocks.md](04-three-clocks.md) | Видимое окно, обучение и прогноз; ключ кэша по строкам trainRange |
| [05-training-query-rewrite.md](05-training-query-rewrite.md) | Перепись запроса обучения по типу источника (только если понадобился train) |
| [06-series-matching.md](06-series-matching.md) | Сопоставление обучающих фреймов с видимыми рядами |
| [07-post-forecast.md](07-post-forecast.md) | `POST /forecast`: лимиты 413/429, проба Restore, апсерт строки расписания, fit под лимитером, Put снаружи |
| [08-request-response.md](08-request-response.md) | JSON: `cacheKey` `needTrain` `trainSource` `provenance` `retrain` `cached` |
| [09-plot-composition.md](09-plot-composition.md) | Фреймы истории и прогноза, Retrain, граница `to` |
| [10-failure-reasons.md](10-failure-reasons.md) | Пути ошибок оверлея, включая 413 / 429 и недоступный store |
| [11-app-pages.md](11-app-pages.md) | Лендинг, Configuration (DSN снимка) и Retrain schedules (таблица `forecast.retrain`) |
| [12-snapshot-cache.md](12-snapshot-cache.md) | Отпечаток `cacheKey` и что в него не входит, кэш снимков с TTL в каждом процессе, ленивое подключение к Postgres |
| [13-retrain-scheduler.md](13-retrain-scheduler.md) | Автономный пересчёт по cron: claim, `/api/ds/query`, fit, Finish, org в ключе, отключение по 401/403 |

Диаграммы рантайма (Compose, Helm, порты, seed-джобы) лежат в `../timeseries-grafana-sandbox/diagrams/`.

Оверлей не читает Kafka. Базовые линии minute-of-week — соседний процесс `timeseries-baselines`, не этот; в `forecast.retrain` он пишет строки `scope=baseline` с `org_id = 0`. Снимки — schema `forecast` на overlay-postgres, не метаданные Druid.
