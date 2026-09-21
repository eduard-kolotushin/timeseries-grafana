# 10. Пути ошибок и причины

Панель всегда оставляет историю на экране и никогда незаметно для пользователя не подгоняет модель по видимому ряду, если обучающий запрос пуст.

Показывается первая ошибка оверлея (`overlayError ?? next`), а успешные ряды по-прежнему накладываются.

Промах снимка — не ошибка: HTTP 200 и `needTrain`. Ошибка store (DSN задан, но Postgres недоступен) — HTTP 500 → `REASON_BACKEND`, без «тихого» `needTrain`. Store не выходит из строя навсегда: следующая загрузка повторит подключение (не чаще раза в 5 с), поэтому после восстановления Postgres оверлей возвращается сам.

Лимиты нагрузки: HTTP 413 → `REASON_OVERSIZE` / `REASON_TRAIN_TOO_LONG`, HTTP 429 → `REASON_BUSY`. Оба останавливают дальнейшие POST по рядам этой загрузки; автоматического повтора нет — следующее обновление попробует снова. Отменённая загрузка (`AbortError`) причиной ошибки не считается: её результат просто не рисуется.

Что на бэкенде даёт 400, а что 500 — решает `httpStatusFor` в `forecast.go` (неизвестная модель, неверный cacheKey, пустые данные, нет частоты, неверные alpha/period/season/calendar/level/range, повреждённый снимок, несортированные или дублирующиеся времена и т.д. → 400).

Источник: `src/forecast-panel/reasons.ts`, `overlayLoad.ts`, `ForecastPanel.tsx`.

```mermaid
flowchart TD
  Start["load"] --> Inv{"окно прогноза некорректно?"}
  Inv -->|да| R1["REASON_INVALID_RANGE инвертировано или неверно"]
  Inv -->|нет| Probe["пробный POST без times если не Retrain"]
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
  R2 --> Partial["история плюс удавшиеся прогнозы показывается первая ошибка"]
  R3 --> Partial
  R4 --> Hist
  R5 --> Partial
  R6 --> Partial
  R7 --> Partial
```
