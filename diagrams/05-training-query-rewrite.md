# 5. Переписывание обучающего запроса

`queryTrainingFrames` клонирует те же targets панели, фиксирует границы времени на окно обучения и группирует запросы по UID источника. Переписывание зависит от типа источника (`rewriteTrainTargets`), поэтому в `pkg/` нет ни одного клиента источника данных: бэкенд повторяет то, что панель уже отправила.

Функция вызывается из `loadOverlayForecasts` **только** если пробный запрос вернул `needTrain` хотя бы для одного ряда или пользователь нажал Retrain. При обновлении с актуальным снимком этот путь не выполняется.

Типы источников и переписывание:

| Тип источника | Что делает переписывание |
| --- | --- |
| `prometheus` | выставляет `range: true`, `instant: false`, `exemplar: false` и `interval` по окну обучения; instant-цель отбрасывается с указанием причины |
| `grafana-opensearch-datasource` | шаг `date_histogram` по окну обучения; логи, raw, traces и PPL без `time_series` отбрасываются с указанием причины |
| `postgres` и `grafana-postgresql-datasource` | `format: time_series`; ISO-дата видимого диапазона заменяется на ISO окна обучения, если в SQL нет макросов `$__timeFilter` |
| остальные (Druid, TestData) | `applyLookbackRange`: шаблоны `__from` / `__to` / ISO / ms и `builder.intervals` |

Те же targets уезжают в бэкенд в `trainSource.queries` без изменений, а однострочная сводка (`summarizeTrainTargets`: `PromQL:`, `PPL:`, `Lucene:`, `SQL:`, `Druid SQL:` или `Druid:` с таблицей) попадает в строку расписания как `querySummary`.

Druid SQL часто подставляет видимый диапазон литералами прямо в тексте запроса, поэтому в `applyLookbackRange` нужна перезапись через JSON. `maxDataPoints` считается по длине окна обучения, а не по диапазону дашборда.

Источник: `src/forecast-panel/overlayLoad.ts`, `trainQuery.ts`, `trainRewrite.ts`, `lookback.ts`.

```mermaid
flowchart TD
  A["data.request.targets hide is false"] --> B{"есть request и toMs больше fromMs?"}
  B -->|нет| N["вернуть null"]
  B -->|да| C["range от trainFrom до trainTo"]
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
  H --> I["склеить фреймы из resp.data"]
  I --> J{"есть фреймы?"}
  J -->|нет| N
  J -->|да| K["вернуть фреймы"]
```
