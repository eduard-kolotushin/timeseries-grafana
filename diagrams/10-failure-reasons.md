# 10. Пути ошибок и причины

Панель всегда оставляет историю на экране. Она никогда не подгоняет модель молча по видимому ряду, если обучающий запрос пуст.

Побеждает первая ошибка оверлея (`overlayError ?? next`). Успешные ряды всё равно накладываются.

Промах снимка — не ошибка: HTTP 200 и `needTrain`. Ошибка store (DSN задан, Postgres недоступен) — HTTP 500 → `REASON_BACKEND`, без тихого `needTrain`. Store не «умирает» навсегда: следующая загрузка повторит подключение (не чаще раза в 5 с), так что после подъёма Postgres оверлей восстанавливается сам.

Лимиты нагрузки: HTTP 413 → `REASON_OVERSIZE` / `REASON_TRAIN_TOO_LONG`, HTTP 429 → `REASON_BUSY`. Оба останавливают дальнейшие POST по рядам этой загрузки; авто-повтора нет, следующий refresh пробует снова. Отменённая загрузка (`AbortError`) причиной не считается — её результат просто не рисуется.

400 против 500 на бэкенде — `httpStatusFor` в `forecast.go` (неизвестная модель, неверный cacheKey, пусто, нет частоты, неверные alpha/period/season/calendar/level/range, снимок, несортированные или дублирующиеся времена и т.д. → 400).

Источник: `src/forecast-panel/reasons.ts`, `overlayLoad.ts`, `ForecastPanel.tsx`.

```mermaid
flowchart TD
  Start["load"] --> Inv{"окно прогноза некорректно?"}
  Inv -->|да| R1["REASON_INVALID_RANGE инвертировано или неверно"]
  Inv -->|нет| Probe["проба POST без times если не Retrain"]
  Probe -->|HTTP ошибка| R2["reasonFromUnknown: 413 OVERSIZE, 429 BUSY, 5xx BACKEND"]
  Probe -->|AbortError| Drop["ничего: загрузка отменена новой"]
  Probe --> Need{"есть ряды с needTrain или Retrain?"}
  Need -->|нет| Draw["фреймы прогноза из снимка"]
  Need -->|да| TQ["queryTrainingFrames"]
  TQ -->|throw| R3["reasonFromUnknown"]
  TQ --> Empty{"нет извлечённых точек обучения?"}
  Empty -->|да| R4["REASON_TRAIN_EMPTY обучающий запрос без точек"]
  Empty -->|нет| Loop["POST с times на каждый ряд в need"]
  Loop -->|ошибка HTTP| R5["reasonFromUnknown из тела; 413 или 429 останавливают остальные ряды"]
  Loop -->|длина times 0| R6["REASON_EMPTY_WINDOW нет точек прогноза в диапазоне"]
  Loop -->|все пропущены| R7["REASON_ALL_NAN все значения прогноза отсутствуют"]
  Loop -->|ok| Draw2["добавить фрейм прогноза"]
  R1 --> Hist["фреймы только история"]
  R2 --> Partial["история плюс удавшиеся прогнозы побеждает первая ошибка"]
  R3 --> Partial
  R4 --> Hist
  R5 --> Partial
  R6 --> Partial
  R7 --> Partial
```
