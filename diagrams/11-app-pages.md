# 11. Страницы app-плагина

Лендинг — только текст. Configuration сохраняет DSN снимка (`storeHost` / port / database / user / ssl / password) через `POST /api/plugins/.../settings`. Оверлей `/forecast` с этих страниц не вызывается. `GET /ping` есть (`{"message":"ok"}`), но оверлей его не вызывает.

Env `FORECAST_STORE_*` в процессе Grafana перекрывает сохранённые поля.

```mermaid
sequenceDiagram
  autonumber
  actor Admin as Админ
  participant Grafana
  participant App as Фронтенд приложения
  participant BE as gpx_forecast

  Admin->>Grafana: Открыть Configuration
  Grafana->>App: AppConfig Snapshot store
  Admin->>App: Save jsonData storeHost
  App->>Grafana: POST plugins settings
  Admin->>Grafana: Открыть лендинг приложения
  Grafana->>App: Home текст добавить панель оверлея
  Grafana->>BE: CheckHealth
  BE-->>Grafana: HealthStatusOk
  Note over App,BE: Landing и Config не делают POST forecast
```
