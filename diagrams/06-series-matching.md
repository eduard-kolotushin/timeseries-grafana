# 6. Сопоставление рядов перед POST с times

Этот шаг выполняется только на ветке промаха / Retrain, после одного `queryTrainingFrames`.

Каждый видимый числовой ряд, которому нужен fit, получает свой POST с `times`. Обучающие фреймы сопоставляются с видимыми рядами по имени поля Grafana (`getFieldDisplayName`). Пустой результат обучения нельзя незаметно подменять видимым рядом.

`trainingForFit` возвращает `null`, если обучающих рядов нет или если при нескольких рядах имя не совпало. В этом случае видимый ряд пропускается без POST. `REASON_TRAIN_EMPTY` ставится только когда извлечённых точек обучения **нет вообще**.

`extractSeries` берёт поле времени и каждое числовое поле (широкие фреймы Postgres/Prom).

```mermaid
flowchart TD
  V["видимые ряды в need"] --> E1["extractSeries: time плюс каждое number"]
  T["фреймы обучения"] --> E2["extractSeries"]
  E2 --> Empty{"trained.length это 0?"}
  Empty -->|да| Stop["REASON_TRAIN_EMPTY только история без POST"]
  Empty -->|нет| Loop["для каждого видимого ряда в need"]
  E1 --> Loop
  Loop --> Match{"имя обучающего ряда совпадает с display?"}
  Match -->|да| Fit["взять этот обучающий ряд"]
  Match -->|нет| One{"ровно один обучающий ряд?"}
  One -->|да| Fit2["взять единственный обучающий ряд"]
  One -->|нет| Skip["пропустить ряд без POST"]
  Fit --> Post["POST forecast с times и cacheKey"]
  Fit2 --> Post
```
