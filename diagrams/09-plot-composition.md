# 9. Сборка графика и граница `to`

Лишние точки обучения не рисуются. Если окно прогноза выходит за диапазон дашборда (типичный Auto `now → now+duration`), `to` графика растягивается, чтобы оверлей был виден.

Пустой `data.series` сразу даёт `PanelDataErrorView` (нужны time и number) и не вызывает `/forecast`.

При `cached` панель показывает «Using saved model». Кнопка Retrain ставит флаг и перезапускает `load` (опции панели — Retrain для всех оверлеев этого поколения).

```mermaid
flowchart LR
  H["фреймы истории из видимого extractSeries"] --> Merge["setFrames история плюс прогнозы"]
  F["фреймы прогноза из ответов POST"] --> Merge
  Merge --> OV["applyFieldOverrides"]
  OV --> TS["grafana ui TimeSeries"]
  TR["timeRange"] --> To["to графика это max из to панели окна прогноза и последней точки"]
  To --> TS
  Saved["Using saved model если usedSaved"] --> TS
  Retrain["Retrain queueRetrain"] --> TS
  Err["Alert при любой причине ошибки"] --> TS
```
