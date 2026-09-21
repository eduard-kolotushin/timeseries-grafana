# 1. Контекст системы

Кто общается с плагином и что он не должен хостить. Страниц три: лендинг — только текст, Configuration хранит DSN снимка и `/forecast` не вызывает, Retrain schedules показывает и правит строки расписаний. Видимый запрос панели идёт всегда. Второй запрос источника (обучение) — только при `needTrain` или Retrain.

Grafana alerting ходит не в панель, а во вложенный источник **Forecast** (`QueryData`): он только `Restore` + `ForecastRange` по `cacheKey`, никогда не обучает. Оба процесса `gpx_forecast` (app и datasource) читают одну таблицу снимков в Postgres.

Третий участник — автономный пересчёт по cron внутри процесса app (`FORECAST_RETRAIN_CRON`, тик 30 с): он берёт **свои** строки `forecast.retrain` (`scope=panel`, своя org) через `FOR UPDATE SKIP LOCKED`, тянет фреймы через `/api/ds/query` самой Grafana по сохранённому `trainSource`, обучает, пишет снимок и закрывает строку. Строки `scope=baseline` (`org_id = 0`, видны во всех org, опознаются только по `metric_hash`) пишет и закрывает соседний `timeseries-baselines`; DDL таблицы делает только плагин. Ошибка планировщика не влияет ни на запрос панели, ни на alerting.

Рантайм (Compose, Helm) — в `../timeseries-grafana-sandbox/diagrams/`; планировщик подробнее — в [13-retrain-scheduler.md](13-retrain-scheduler.md).

```mermaid
flowchart LR
  User["Пользователь дашборда"]
  Grafana["Grafana OSS"]
  Panel["Панель оверлея прогноза"]
  Pages["Лендинг Configuration Retrain schedules"]
  AppBE["app gpx_forecast"]
  Sched["планировщик cron 30 с"]
  DSBE["Forecast datasource gpx_forecast"]
  Alert["Grafana alerting"]
  DS["Источник данных дашборда"]
  PG["overlay-postgres schema forecast"]
  RT[("forecast.retrain")]
  TS["модуль timeseries"]
  FC["модуль timeseries-forecast"]
  Worker["timeseries-baselines не этот процесс"]

  User --> Grafana
  Grafana --> Panel
  Grafana --> DS
  Grafana --> Pages
  Panel -->|"видимые фреймы запроса"| Grafana
  Panel -->|"проба POST без times"| AppBE
  Panel -->|"при needTrain: запрос обучения"| DS
  Alert -->|"QueryData refId cacheKey kind"| DSBE
  Pages -->|"schedules только Admin"| AppBE
  AppBE --> TS
  AppBE --> FC
  DSBE --> FC
  AppBE -->|"pgx Put и Get"| PG
  DSBE -->|"pgx Get"| PG
  AppBE -->|"апсерт строки панели и провенанс"| RT
  Sched -->|"claim и Finish"| RT
  Sched -->|"POST api/ds/query с trainSource"| Grafana
  Sched -->|"Fit и Put снимка"| PG
  Worker -.->|"строки baseline org 0"| RT
  Worker -.->|"пишет baselines в Kafka"| DS
```
