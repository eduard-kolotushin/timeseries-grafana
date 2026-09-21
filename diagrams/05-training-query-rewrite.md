# 5. Перепись запроса обучения

`queryTrainingFrames` клонирует те же targets панели, фиксирует границы времени на окно обучения и группирует по UID источника. Перепись идёт по типу источника (`rewriteTrainTargets`), поэтому в `pkg/` нет ни одного клиента источника данных: бэкенд повторяет то, что панель уже отправила.

Вызывается из `loadOverlayForecasts` **только** если проба вернула `needTrain` хотя бы для одного ряда или пользователь нажал Retrain. Refresh с живым снимком этот путь не проходит.

Типы и их перепись:

| Тип источника | Что делает перепись |
| --- | --- |
| `prometheus` | `range: true`, `instant: false`, `exemplar: false` и `interval` по окну обучения; instant-цель отбрасывается с причиной |
| `grafana-opensearch-datasource` | шаг `date_histogram` по окну обучения; логи, raw, traces и PPL без `time_series` отбрасываются с причиной |
| `postgres` и `grafana-postgresql-datasource` | `format: time_series`; ISO видимого диапазона заменяется на ISO обучения, если в SQL нет макросов `$__timeFilter` |
| остальные (Druid, TestData) | `applyLookbackRange`: шаблоны `__from` / `__to` / ISO / ms и `builder.intervals` |

Те же targets уезжают в бэкенд в `trainSource.queries` как есть, а однострочная сводка (`summarizeTrainTargets`: `PromQL:`, `PPL:`, `Lucene:`, `SQL:`, `Druid SQL:` или `Druid:` с таблицей) попадает в строку расписания как `querySummary`.

Druid SQL часто вшивает видимый диапазон литералами, поэтому нужна JSON-перепись в `applyLookbackRange`. `maxDataPoints` считается по длине окна обучения, не по диапазону дашборда.

Источник: `src/forecast-panel/overlayLoad.ts`, `trainQuery.ts`, `trainRewrite.ts`, `lookback.ts`.

```mermaid
flowchart TD
  A["data.request.targets hide is false"] --> B{"есть request и toMs больше fromMs?"}
  B -->|нет| N["вернуть null"]
  B -->|да| C["range это trainFrom до trainTo"]
  C --> D["maxDataPoints это min из 100000 и ceil span/intervalMs плюс 1"]
  D --> E["scopedVars __from и __to это ms обучения"]
  E --> F{"тип источника"}
  F -->|prometheus| F1["range true interval по окну"]
  F -->|opensearch| F2["date_histogram по окну"]
  F -->|postgres| F3["format time_series и ISO обучения"]
  F -->|"остальные"| F4["applyLookbackRange шаблоны ISO и builder.intervals"]
  F1 --> Rsn{"все цели отброшены?"}
  F2 --> Rsn
  F3 --> Rsn
  Rsn -->|да| R1["REASON_UNSUPPORTED_* и только история"]
  Rsn -->|нет| G["сгруппировать targets по uid источника"]
  F4 --> G
  G --> H["getDataSourceSrv.get затем ds.query"]
  H --> I["склеить resp.data фреймы"]
  I --> J{"есть фреймы?"}
  J -->|нет| N
  J -->|да| K["вернуть фреймы"]
```
