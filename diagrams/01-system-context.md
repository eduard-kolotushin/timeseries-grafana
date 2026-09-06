# 1. Контекст системы

Кто общается с плагином и что он не должен хостить. Лендинг приложения — только текст. Configuration хранит DSN снимка и не вызывает `/forecast`. Видимый запрос панели идёт всегда. Второй запрос источника (обучение) — только при `needTrain` или Retrain.

Grafana alerting ходит не в панель, а во вложенный источник **Forecast** (`QueryData`): он только `Restore` + `ForecastRange` по `cacheKey`, никогда не обучает. Оба процесса `gpx_forecast` (app и datasource) читают одну таблицу снимков в Postgres.

Рантайм (Compose, Helm) — в `../timeseries-grafana-sandbox/diagrams/`.

```mermaid
flowchart LR
  User["Пользователь дашборда"]
  Grafana["Grafana OSS"]
  Panel["Панель оверлея прогноза"]
  AppBE["app gpx_forecast"]
  DSBE["Forecast datasource gpx_forecast"]
  Alert["Grafana alerting"]
  DS["Источник данных дашборда"]
  PG["overlay-postgres schema forecast"]
  TS["модуль timeseries"]
  FC["модуль timeseries-forecast"]
  Worker["timeseries-baselines не этот процесс"]

  User --> Grafana
  Grafana --> Panel
  Grafana --> DS
  Panel -->|"видимые фреймы запроса"| Grafana
  Panel -->|"проба POST без times"| AppBE
  Panel -->|"при needTrain: запрос обучения"| DS
  Alert -->|"QueryData refId cacheKey kind"| DSBE
  AppBE --> TS
  AppBE --> FC
  DSBE --> FC
  AppBE -->|"pgx Put и Get"| PG
  DSBE -->|"pgx Get"| PG
  Worker -.->|"пишет baselines в Kafka"| DS
```
