# 8. Тела запроса и ответа

Как панель кладёт полосы на фреймы Grafana:

- Поле значения прогноза, цвет warning
- Скрытые `{name} lower` и `{name} upper`
- `custom.fillBelowTo` на upper → полоса интервала
- `hideFrom.legend/tooltip` на полях границ

Проба не шлёт `times`/`values`. Fit шлёт их вместе с тем же `cacheKey`. `retrain` на бэкенде важен только когда `times` нет.

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
    K --> R["retrain опционально"]
  end

  subgraph resp["Тело ответа"]
    OT["times"] --> OV["values null значит NaN"]
    OV --> OL["lower опционально"]
    OL --> OU["upper опционально"]
    OU --> NT["needTrain HTTP 200 на промахе"]
    NT --> CH["cached true после Restore"]
  end

  R --> OT
```
