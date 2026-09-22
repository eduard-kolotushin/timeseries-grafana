# 8. Тела запроса и ответа

Как панель накладывает полосы неопределённости на фреймы Grafana:

- поле значения прогноза, цвет warning;
- скрытые поля `{name} lower` и `{name} upper`;
- `custom.fillBelowTo` на upper → полоса интервала;
- `hideFrom.legend/tooltip` на полях границ.

Пробный запрос не отправляет `times`/`values`. Fit отправляет их вместе с тем же `cacheKey`. Бэкенд учитывает `retrain` только когда `times` нет.

```mermaid
flowchart TB
  subgraph req["Тело запроса"]
    T["times unix ms только при fit"] --> V["values число или null"]
    V --> M["model"]
    M --> W["from и to окна прогноза в ms"]
    W --> P["alpha beta period"]
    P --> S["season calendar"]
    S --> L["level 0 если showInterval выключен иначе interval по умолчанию 0.95"]
    L --> K["cacheKey SHA-256 hex 64"]
    K --> TS["trainSource: datasourceUid queries from to seriesName relative lookbackMs"]
    TS --> PR["провенанс: panelId panelTitle dashboardUid querySummary на каждом запросе оверлея, включая пробный"]
    PR --> R["retrain опционально"]
  end

  subgraph resp["Тело ответа"]
    OT["times"] --> OV["values null означает NaN"]
    OV --> OL["lower опционально"]
    OL --> OU["upper опционально"]
    OU --> NT["needTrain HTTP 200 при промахе"]
    NT --> CH["cached true после Restore"]
  end

  R --> OT
```
