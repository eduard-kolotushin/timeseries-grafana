# 9. Сборка графика и граница `to`

Лишние точки обучения не рисуются. Если окно прогноза выходит за диапазон дашборда (типичный Auto `now → now+duration`), граница `to` графика растягивается, чтобы оверлей оставался видимым.

Пустой `data.series` сразу даёт `PanelDataErrorView` (нужны поля time и number) и не вызывает `/forecast`.

При `cached` панель показывает «Using saved model». Кнопка Retrain поднимает флаг и перезапускает `load` (в опциях панели есть Retrain для всех оверлеев этой панели).

```mermaid
flowchart LR
  H["фреймы истории из видимого extractSeries"] --> Merge["setFrames: история плюс прогнозы"]
  F["фреймы прогноза из ответов POST"] --> Merge
  Merge --> OV["applyFieldOverrides"]
  OV --> TS["grafana ui TimeSeries"]
  TR["timeRange"] --> To["to графика это max из to панели окна прогноза и последней точки"]
  To --> TS
  Saved["Using saved model если usedSaved"] --> TS
  Retrain["Retrain queueRetrain"] --> TS
  Err["Alert при любой ошибке"] --> TS
```
