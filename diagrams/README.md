# Диаграммы взаимодействий и потоков данных плагина

Локальные заметки о работе app-плагина `eduardkolotushin-forecast-app`, вложенной панели `eduardkolotushin-forecast-panel` и вложенного источника данных `eduardkolotushin-forecast-datasource`.

Источник истины по поведению — `docs/ARCHITECTURE.md` и код в `src/forecast-panel/`, `src/forecast-datasource/` и `pkg/plugin/`.

| Файл | Диаграмма |
| --- | --- |
| [01-system-context.md](01-system-context.md) | Плагин, Grafana, источники данных, снимки в Postgres, планировщик, общая таблица `forecast.retrain` с воркером, alerting через Forecast datasource, соседние библиотеки |
| [02-plugin-internals.md](02-plugin-internals.md) | App, вложенная панель, два процесса `gpx_forecast`, планировщик, ресурс `/schedules`, лимитер, кэш SnapshotStore |
| [03-overlay-sequence.md](03-overlay-sequence.md) | Оверлей: пробный запрос снимка; обучение — только при промахе, нажатии Retrain или наступившем сроке расписания; отмена устаревшей загрузки |
| [04-three-clocks.md](04-three-clocks.md) | Видимое окно, обучение и прогноз; ключ кэша из сырых строк trainRange |
| [05-training-query-rewrite.md](05-training-query-rewrite.md) | Переписывание обучающего запроса по типу источника (только когда понадобился train) |
| [06-series-matching.md](06-series-matching.md) | Сопоставление обучающих фреймов с видимыми рядами |
| [07-post-forecast.md](07-post-forecast.md) | `POST /forecast`: лимиты 413/429, пробный запрос с Restore, upsert строки расписания, fit под семафором, Put — снаружи |
| [08-request-response.md](08-request-response.md) | JSON: `cacheKey` `needTrain` `trainSource` `provenance` `retrain` `cached` |
| [09-plot-composition.md](09-plot-composition.md) | Фреймы истории и прогноза, Retrain, граница `to` |
| [10-failure-reasons.md](10-failure-reasons.md) | Пути ошибок оверлея, включая 413 / 429 и недоступный store |
| [11-app-pages.md](11-app-pages.md) | Лендинг, Configuration (DSN снимков) и Retrain schedules (таблица `forecast.retrain`) |
| [12-snapshot-cache.md](12-snapshot-cache.md) | Отпечаток `cacheKey` и что в него не входит; кэш снимков с TTL в каждом процессе; ленивое подключение к Postgres |
| [13-retrain-scheduler.md](13-retrain-scheduler.md) | Автономный пересчёт по cron: claim, `/api/ds/query`, fit, Finish, организация в ключе, отключение по 401/403 |

Диаграммы рантайма (Compose, Helm, порты, seed-джобы) лежат в `../timeseries-grafana-sandbox/diagrams/`.

Оверлей не читает Kafka. Базовые линии minute-of-week генерирует соседний процесс `timeseries-baselines`, а не этот; в `forecast.retrain` он пишет строки `scope=baseline` с `org_id = 0`. Снимки хранятся в схеме `forecast` на overlay-postgres, а не в метаданных Druid.
