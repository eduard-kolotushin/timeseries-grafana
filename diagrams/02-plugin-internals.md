# 2. Устройство плагина

Grafana загружает один app-плагин с вложенной панелью, вложенным источником данных и Go-процессами ресурсов. Бинарник один (`gpx_forecast`), но Grafana запускает его дважды: как бэкенд app (`CallResource /forecast` и `/schedules`) и как бэкенд Forecast datasource (`QueryData`). Общего состояния в памяти между ними нет — общие только таблицы в Postgres.

Страницы живут в процессе Grafana: лендинг и Configuration не на пути оверлея, Retrain schedules ходит в ресурс `/schedules`. Configuration пишет DSN снимка (`jsonData` / `secureJsonData`); env `FORECAST_STORE_*` перекрывает поля в процессе `gpx_forecast`.

Планировщик — горутина процесса app. Она не мешает запросам панели: `/api/ds/query` и `Put` снимка идут **вне** семафора вычислений, под ним только сам `fitRequest`.

| Часть | ID / путь | Роль |
| --- | --- | --- |
| App | `eduardkolotushin-forecast-app` | Метаданные, навигация, Go-бэкенд, планировщик |
| Вложенная панель | `eduardkolotushin-forecast-panel` | Визуализация оверлея; проба затем опциональный train |
| Вложенный источник | `eduardkolotushin-forecast-datasource` | `QueryData` для alerting: Restore по `cacheKey`, промах — ошибка |
| Ресурсы | `POST /forecast`, `GET\|PUT\|DELETE /schedules`, `POST /schedules/default`, `GET ping` | `/schedules*` под Admin, остальное без ограничений |
| Планировщик | `retrain.go`: тик 30 с, батч 4, lease 5 мин | `Claim` → `/api/ds/query` → `fitRequest` → `Put` → `Finish` |
| Расписания | `forecast.retrain`, PK `(scope, org_id, key)` | Спека панели = `trainSource` + модель; `spec` наружу не отдаётся, наружу идёт производный `source` |
| Лимиты | `workLimiter` (4 по умолчанию), `MAX_TRAIN_POINTS` 100k, тело 16 MiB, тело `/api/ds/query` 64 KiB | 429 при занятости, 413 при переполнении; только CPU-работа под семафором |
| Снимок | `SnapshotStore` = TTL-кэш над `forecast.snapshots` (pgx) | Org-scoped JSONB; в процессе ≤ 256 записей, TTL 30 с |
| Бинарник бэкенда | `gpx_forecast` | `pkg/main.go` → `plugin.NewApp` или `plugin.NewDatasource` |

```mermaid
flowchart LR
  subgraph grafana["Процесс Grafana"]
    Dash["Таймпикер дашборда"] --> PQ["Исполнитель запросов панели"]
    PQ -->|"data.series это видимая история"| Overlay["ForecastPanel"]
    Opts["Опции панели"] --> Overlay
    Overlay --> HTTP["resources/forecast"]
    Rule["Правило alerting"] --> QD["QueryData refId"]
    Page["Retrain schedules Admin"] --> HTTP2["resources/schedules"]
    DSAPI["api/ds/query"]
  end

  subgraph app["app gpx_forecast"]
    Mux["httpadapter mux"] --> Ping["GET ping"]
    Mux --> Fcast["POST forecast"]
    Mux --> Res["GET PUT DELETE schedules и default"]
    Fcast --> Dispatch["dispatchForecast"]
    Dispatch --> Lim1["workLimiter: Fit SnapshotOf ForecastRange"]
    Dispatch --> C1["cachedStore TTL 30 с не больше 256"]
    Res --> Rows["List Upsert Delete Due Identify"]
    Tick["тик 30 с"] --> Claim["Claim 4 FOR UPDATE SKIP LOCKED своя org"]
    Claim --> Fetch["fetchFrames по trainSource вне лимитера"]
    Fetch --> Fit2["fitRequest под workLimiter"]
    Fit2 --> Put2["SnapshotOf затем Put снимка"]
    Put2 --> Finish["Finish next status по владельцу"]
  end

  subgraph dsproc["Forecast datasource gpx_forecast"]
    Q["queryOne kind cacheKey"] --> C2["cachedStore TTL 30 с не больше 256"]
    Q --> Lim2["workLimiter: Restore ForecastRange"]
  end

  subgraph store["Postgres overlay"]
    Snap["forecast.snapshots"]
    Ret["forecast.retrain"]
  end

  HTTP -->|"CallResource"| Mux
  HTTP2 -->|"CallResource"| Res
  QD --> Q
  C1 -->|"Put write-through, Get при промахе или после TTL"| Snap
  C2 -->|"Get при промахе или после TTL"| Snap
  Dispatch -->|"апсерт строки панели и провенанс"| Ret
  Rows --> Ret
  Claim --> Ret
  Finish --> Ret
  Fetch -->|"тот же HTTP API"| DSAPI
  DSAPI -->|"прокси к источнику"| PQ
```

Get/Put в Postgres выполняются **вне** семафора: медленная база не занимает слоты вычислений и не превращается в 429. Так же снаружи стоит и `fetchFrames`: слот Fit достаётся только вычислению.
