# 5. Перепись запроса обучения

`queryTrainingFrames` клонирует те же targets панели, фиксирует границы времени на окно обучения и группирует по UID источника.

Вызывается из `loadOverlayForecasts` **только** если проба вернула `needTrain` хотя бы для одного ряда или пользователь нажал Retrain. Refresh с живым снимком этот путь не проходит.

Druid SQL часто вшивает видимый диапазон литералами, поэтому нужна JSON-перепись в `applyLookbackRange`. `maxDataPoints` считается по длине окна обучения, не по диапазону дашборда.

Источник: `src/forecast-panel/overlayLoad.ts`, `trainQuery.ts`, `lookback.ts`.

```mermaid
flowchart TD
  A["data.request.targets hide is false"] --> B{"есть request и toMs больше fromMs?"}
  B -->|нет| N["вернуть null"]
  B -->|да| C["range это trainFrom до trainTo"]
  C --> D["maxDataPoints это min из 100000 и ceil span/intervalMs плюс 1"]
  D --> E["scopedVars __from и __to это ms обучения"]
  E --> F["applyLookbackRange"]
  F --> F1["заменить шаблоны __from и __to и ISO"]
  F1 --> F2["заменить ISO и ms видимого диапазона если они отличаются от обучения"]
  F2 --> F3["Druid builder.intervals на fromIso/toIso"]
  F3 --> G["сгруппировать targets по uid источника"]
  G --> H["getDataSourceSrv.get затем ds.query"]
  H --> I["склеить resp.data фреймы"]
  I --> J{"есть фреймы?"}
  J -->|нет| N
  J -->|да| K["вернуть фреймы"]
```
