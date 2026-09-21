# 3. Последовательность оверлея

К моменту монтирования `ForecastPanel` Grafana уже выполнила запрос видимого диапазона панели. Панель не переобучает модель при каждом обновлении: сначала отправляется POST без `times` (`cacheKey` + окно прогноза). Обучающий запрос к источнику данных — один на панель и только если пришёл `needTrain` или нажат Retrain; `needTrain` приходит и тогда, когда у строки расписания этой панели подошёл срок (её переобучит либо планировщик, либо сама панель — см. [13-retrain-scheduler.md](13-retrain-scheduler.md)).

Источник: `src/forecast-panel/overlayLoad.ts`, `ForecastPanel.tsx`.

## Попадание в снимок (без обучения)

```mermaid
sequenceDiagram
  autonumber
  actor User as Пользователь
  participant Grafana
  participant DS as Источник данных
  participant Panel as ForecastPanel
  participant BE as gpx_forecast
  participant Lib as timeseries-forecast

  User->>Grafana: Открыть дашборд, сменить время или опции
  Grafana->>DS: Запрос видимого диапазона панели
  DS-->>Grafana: data.series фреймы истории
  Grafana->>Panel: PanelProps data timeRange options
  Panel->>Panel: extractSeries resolveForecastWindow cacheKey
  Panel->>BE: POST cacheKey from to model без times
  BE->>Lib: Restore затем ForecastRange
  Lib-->>BE: сетка times и values
  BE-->>Panel: ForecastResponse cached true
  Panel->>Grafana: история плюс прогноз, Using saved model
```

Пробный запрос отправляется для каждого видимого ряда. Если попали все ряды, `queryTrainingFrames` не вызывается.

## Промах или Retrain (один train на панель)

Retrain пропускает пробный запрос (иначе ряды с `needTrain` накапливались бы); затем выполняется один `queryTrainingFrames`.

```mermaid
sequenceDiagram
  autonumber
  actor User as Пользователь
  participant Grafana
  participant DS as Источник данных
  participant Panel as ForecastPanel
  participant BE as gpx_forecast
  participant Lib as timeseries-forecast

  Grafana->>Panel: PanelProps после видимого запроса
  Panel->>BE: POST cacheKey from to без times
  BE-->>Panel: needTrain true HTTP 200
  Panel->>DS: queryTrainingFrames train from/to
  DS-->>Panel: фреймы обучения
  Panel->>Panel: trainingForFit
  Panel->>BE: POST times values cacheKey from to
  BE->>Lib: New затем Fit затем SnapshotOf
  BE->>Lib: ForecastRange
  Lib-->>BE: сетка times и values
  BE-->>Panel: ForecastResponse
  Panel->>Grafana: история плюс прогноз
```

HTTP 200 при промахе нужен, чтобы `getBackendSrv` не бросал исключение. Пустое обучение — `REASON_TRAIN_EMPTY`, без подстановки видимого ряда.

Пробный запрос без `times` заодно переносит провенанс панели в уже существующую строку расписания (`Identify`), а успешный fit перезаписывает её целиком через upsert: спека = `trainSource` + модель, cron и enabled администратора не сбрасываются. Ни одна из этих записей в `forecast.retrain` не становится ошибкой оверлея — только записью в логе.

## Отмена устаревшей загрузки

Каждый `load` получает `AbortController` из `OverlayLoadGate` (опция `maxInflightLoads`, по умолчанию 1). Новое обновление или смена опций отменяют самый старый запрос. Отмена — не просто флаг: `postResource` и `queryTrainingFrames` идут через `abortableLastValue`, который отписывается от Observable у `getBackendSrv().fetch` / `ds.query`; RxJS `fromFetch` при этом обрывает HTTP-запрос, бэкенд видит `context.Canceled` и освобождает слот лимитера.

Источник: `src/forecast-panel/abortable.ts`, `overlayInflight.ts`.

```mermaid
sequenceDiagram
  autonumber
  participant Grafana
  participant Panel as ForecastPanel
  participant Gate as OverlayLoadGate
  participant BE as gpx_forecast

  Grafana->>Panel: PanelProps refresh 1
  Panel->>Gate: start maxInflightLoads
  Gate-->>Panel: AbortController A
  Panel->>BE: POST forecast (fetch Observable, signal A)
  Grafana->>Panel: PanelProps refresh 2, пока A в полёте
  Panel->>Gate: start
  Gate->>Gate: abort A (лимит достигнут)
  Gate-->>Panel: AbortController B
  Note over Panel,BE: отписка от Observable A → HTTP A обрывается → context.Canceled в Go
  Panel->>BE: POST forecast (signal B)
  BE-->>Panel: ForecastResponse для B
  Panel->>Grafana: фреймы, результат A отброшен как AbortError
```
