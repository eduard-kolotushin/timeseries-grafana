# Forecast overlay cannot train from the `sbt-datasource-abyss` datasource

> **RESOLVED** in this repo, commit `e3d179f`: `queryTrainingFrames` now runs every reply item
> through `toTrainingFrames` (`src/forecast-panel/trainFrames.ts`) — Grafana's own
> `getProcessedDataFrames` plus `ensureTimeField` for a `time` field whose values are still
> strings — before `extractSeries`, so the fit `POST /forecast` goes out. The hand-built
> normalizer proposed in *Recommended fix* below was superseded by that smaller change, and the
> `frames.push(...resp.data)` line it cites is now `frames.push(...toTrainingFrames(resp.data))`.

Report for the plugin developer. Self-contained: explains the observed failure, the
root cause, how the Abyss datasource actually produces its response, and the exact fix
that is needed in this repo (`timeseries-grafana`).

## TL;DR

The Forecast overlay panel **does** run the training query and the query **does** return
data (168 rows). But the overlay never sends the fit `POST /forecast`. The reason is a
frame-shape mismatch:

- The Abyss datasource (`sbt-datasource-abyss`) is a **frontend** datasource. For a
  `format: "table"` query (the default, and what the panel uses) its `query()` returns a
  **legacy table object** — `{ columns, rows, type: "table" }` — **not** a `DataFrame`.
- This repo's training path calls `ds.query()` directly and hands the result to
  `extractSeries()`, which requires a `DataFrame` whose time field is `FieldType.time`.
  A table object has no `fields` array at all, so `extractSeries()` yields nothing.
- The **visible** series renders fine only because the panel's scene query runner
  converts that table object into a real `DataFrame` (time → `FieldType.time`) before the
  Timeseries panel draws it. The training path bypasses that runner.

Result: `trained.length === 0` → the overlay reports "Training query returned no points"
(and never POSTs a fit). The datasource and the data are fine.

The **unattended** retrain path — the backend scheduler replaying a stored `trainSource`
through Grafana's `/api/ds/query` — is a *different* code path and does **not** have this
bug: Abyss's Go backend returns a proper `DataFrame` there. It is covered in
[The unattended retrain path](#the-unattended-retrain-path) below; the interactive fix above
is that path's prerequisite (a `panel` row only exists once an interactive fit has trained).

## Environment

| Item | Value |
|---|---|
| Grafana | 12.2.10, base path `/view_st_vm01`, host `platform-st.opsmon.sbt` |
| Dashboard / panel | `bfxcl9n` / panel id 1, switched to the **Forecast overlay** visualization |
| Datasource | `ABYSS-VM`, type `sbt-datasource-abyss`, uid `efcxpd6lsf3lsf` |
| Query (format `table`) | `SELECT $time as "time", avg("value")/10000000 as "avg" FROM "corax_generator_g4" WHERE $defaultFilter and "name" like '%jvm_%' GROUP BY $time` |
| Forecast plugin | `eduardkolotushin-forecast-app` (this repo) |

The `$time` / `$defaultFilter` are **Abyss macros** (not standard Grafana macros). The
datasource expands `$time` to `TIME_FLOOR("__time",'<granularity>')` and `$defaultFilter`
to a `(TIME_FLOOR(...) > TIMESTAMP '…' AND TIME_FLOOR(...) < TIMESTAMP '…')` clause, where
the granularity is derived from the request time range (15m for a 6h window, 1h for a 7d
window).

## Observed behavior (network-verified)

When the panel is the Forecast overlay and the time range is `now-6h`:

1. Visible queries (proxy) — return 22 rows (15-min). **These render.**
2. `POST /api/plugins/eduardkolotushin-forecast-app/resources/forecast` (probe) →
   `{"needTrain":true}`.
3. Training query (proxy) with the 7-day / 1h window — **returns 168 rows.**
4. **No second `POST …/resources/forecast` (the fit) is ever sent.**

Step 4 is the bug. The training data exists (step 3) but is never turned into a fit.

### Important correction to an earlier hypothesis

An earlier pass concluded "the training query returns empty frames." That was wrong and
came from hitting `/api/ds/query` (the **server-side** endpoint). The Abyss datasource is
a **frontend** datasource, so the server-side `/api/ds/query` cannot execute it and returns
`frames: []`. The real query path is the **datasource proxy**:

```
POST /api/datasources/proxy/uid/efcxpd6lsf3lsf/index/analytical/task/project/abyss/query
body: { "sqlQuery": "SELECT …" }
```

Through that correct path the training query returns 168 rows. So the datasource is not the
problem — the frame-shape handling in this repo is.

## How the `sbt-datasource-abyss` datasource works

Source: local clone at `/Users/kolotushin.e.v/GolandProjects/pvv_datasource_pvm`
(version 2.0.0, matches the deployed plugin).

It is declared `"backend": true` in `plugin.json`, **but** it extends
`DataSourceWithBackend` and **overrides `query()` with a frontend implementation**, so the
panel data path (and any `ds.query()` call from another plugin) runs **in the browser**,
not in the Go executable. There are therefore two different query paths that produce
**different frame shapes**:

### Path A — frontend (browser) — what `ds.query()` returns  ← the one this bug hits

`src/DataSource.ts`:

- `query(request)` (line 245) → `this.frontQuery(requestOptions)` → `from(data)`.
- `frontQuery(request)` (line 185) runs each target through `doProxyRequest()` (a proxy
  POST to the Abyss analytical backend), then parses the JSON rows:

```ts
// src/DataSource.ts, frontQuery(), lines 230-239
let parsedData;
if (format === 'singlestat' || format === 'series' || format === 'wide_table') {
  parsedData = SqlParser.parseSqlToSeries(data, range, granularity, filtredData);
  ...
} else {
  parsedData = SqlParser.parseSqlToTable(data, filtredData);   // <-- format: "table" lands here
}
return { data: parsedData };
```

`src/SqlParser.ts`, `parseSqlToTable()` (line 205) returns an **array of table objects**:

```ts
// src/SqlParser.ts, parseSqlToTable(), lines 205-246
return data.map((response, idx) => {
  const responseData = response.data;                 // [{ time: "…", avg: 13319.2 }, …]
  const allColumnNames = Object.keys(responseData[0]); // ["time", "avg"]
  const columns = allColumnNames.map((key) => {
    const column: any = { text: key };
    if (key === 'time') column.type = 'time';          // the ONLY column typed "time"
    return column;
  });
  const rows = responseData.map((row) =>
    allColumnNames.map((key) =>
      key === 'time' ? toUtc(row[key]).local() : row[key]   // time rendered as a LOCAL date STRING
    ),
  );
  return {
    columns,                                            // [{text:"time",type:"time"},{text:"avg"}]
    rows,                                               // [["2026-10-07 18:00:00", 13319.2], …]
    meta: { executedQueryString: targets[idx].rawSql },
    refId: targets[idx].refId,
    type: 'table',
  };
});
```

So the **exact** object `ds.query()` emits for a `format: "table"` query is:

```jsonc
{
  "columns": [
    { "text": "time", "type": "time" },
    { "text": "avg" }
  ],
  "rows": [
    ["2026-10-07 18:00:00", 13319.2099],
    ["2026-10-07 19:00:00", 13319.2136],
    "…"
  ],
  "meta": { "executedQueryString": "SELECT …" },
  "refId": "A",
  "type": "table"
}
```

Key properties for the fix:

- It is **not** a `DataFrame`. There is **no `fields` array.**
- The time column is marked `type: "time"` in `columns`.
- The time **values are local date strings** (`toUtc(iso).local()`), e.g.
  `"2026-10-07 18:00:00"` — **not** epoch milliseconds and **not** ISO.
- The value columns (`avg`) are plain numbers.

### Path B — Go backend (server-side) — used by alerting, NOT by `ds.query()`

`pkg/query-handler.go` `parseSqlToTable()` (line 279) and `pkg/series.go` `converToFrame()`
build a real `*data.Frame` with `data.NewField("time", nil, []time.Time)`. So the
**server-side** (alerting) path returns a proper `DataFrame` with a `FieldType.time`
field. This path is **not** what the overlay's training uses, so it does not help here —
but it confirms the datasource *can* produce a `DataFrame`; the frontend path simply
doesn't.

## Why the visible series works but training fails

Both the visible series and the training data come from the same `frontQuery()` and the
same table object. The difference is what happens **after** `ds.query()`:

- **Visible / panel display:** the panel's scene query runner post-processes the datasource
  response and converts the legacy table object into a `DataFrame`, honoring the
  `type: "time"` column (time strings → `FieldType.time`). The Timeseries panel then draws
  it. (The Panel Inspector → Data tab shows `time` as type `time`, `avg` as `number`.)
- **Overlay training:** this repo's `queryTrainingFrames()` calls `ds.query()` and pushes the
  raw response straight into `train.frames`, with no table→DataFrame conversion.
  `extractSeries()` then receives a table object.

## Where it breaks in this repo

`src/forecast-panel/extract.ts`, `extractSeries()` (line 22):

```ts
export function extractSeries(frame: DataFrame, allFrames?: DataFrame[]): SeriesPoints[] {
  if (isNonTimeseriesFrame(frame)) return [];
  const timeField = frame.fields.find((f) => f.type === FieldType.time); // line 26
  if (!timeField) return [];
  ...
}
```

For a table object, `frame.fields` is `undefined`. Depending on runtime behaviour this
either throws (`Cannot read properties of undefined (reading 'find')`) or — if the frame has
been partially coerced to a `DataFrame` whose time field is still a **string** — simply finds
no `FieldType.time` field. Either way it returns no series.

`src/forecast-panel/overlayLoad.ts` (lines 114-117):

```ts
const trained = (train.frames ?? []).flatMap((frame: DataFrame) => extractSeries(frame, train.frames ?? []));
if (trained.length === 0) {
  return { forecasts, error: overlayError ?? REASON_TRAIN_EMPTY, usedSaved }; // "Training query returned no points"
}
```

So the overlay stops with no fit. The training frames are produced in
`src/forecast-panel/trainQuery.ts`, `queryTrainingFrames()` (line 101); the relevant lines:

- `ds.query({...})` call — lines ~164-177.
- `frames.push(...resp.data);` — line 205.  ← raw table objects land here.

## Recommended fix

Normalize each element of the datasource response into a real `DataFrame` in
`queryTrainingFrames()` (src/forecast-panel/trainQuery.ts) **before** pushing it into
`frames`, so `extractSeries()` always sees a `DataFrame` with a `FieldType.time` field.
This keeps the fix at the training boundary and does not change the visible/fit paths.

A normalizer should handle, per element of `resp.data`:

1. **Already a `DataFrame`** (has `fields`) → keep as-is (Prometheus/Postgres/etc.).
2. **Legacy table object** (has `columns` + `rows`, `type: "table"`, no `fields`) → build a
   `DataFrame`:
   - one field per `columns[i]`;
   - the column with `type: "time"` becomes `FieldType.time`, parsing each row value (a
     local date string such as `"2026-10-07 18:00:00"`) to epoch **milliseconds**. Parse in
     the dashboard timezone so it round-trips the datasource's `toUtc(iso).local()`;
   - numeric columns become `FieldType.number`; string columns `FieldType.string`.
3. **Legacy graphite series** (has `datapoints: [[value, tsMs], …]`, as returned for
   `format: "series"`) → build a `DataFrame` with a `FieldType.time` field from the second
   element of each datapoint and a `FieldType.number` field from the first. (Optional, but
   makes the overlay robust if a panel trains from a `series`-format target.)

After normalization, `extractSeries()` needs no changes and works for every datasource,
including Abyss.

A table-driven Jest test should cover: a `format: "table"` Abyss-shaped object (time as
local date string + `type: "time"` column) → a `DataFrame` whose time field is
`FieldType.time` with correct ms values and whose `avg` field is `FieldType.number`; plus a
passthrough case for an already-valid `DataFrame`.

### Why not just change the query's `format`?

Setting the training target to `format: "series"` would make the datasource return
graphite-style series (`{ target, datapoints }`) instead of a table object — but that is
still **not** a `DataFrame`, so `extractSeries()` would fail the same way. The fix must be
normalization, not a format change.

## The unattended retrain path

The overlay trains interactively (browser) and the backend retrains unattended (scheduler).
They fetch training data through **two different mechanisms**, and only the interactive one
is broken for Abyss. This section documents the unattended path so an agent can confirm —
and, if needed, extend — Abyss support there.

### How the unattended retrain works (this repo, `pkg/plugin/retrain.go`)

The retrain ticker claims a `forecast.retrain` row (v12 claim queue, v15 lease). For a
`panel` row, `grafanaPoster.fetchFrames` (retrain.go:401):

1. **Resolves the training window** (retrain.go:406-414): `spec.Relative && spec.LookbackMs > 0`
   → `[now - LookbackMs, now]` resolved against the claim clock; otherwise the stored absolute
   `[spec.From, spec.To]`.
2. **Posts the stored queries verbatim** (retrain.go:416-435) to
   `POST {GrafanaURL}/api/ds/query` with body
   `{ "queries": <spec.Queries>, "from": "<unix-ms string>", "to": "<unix-ms string>" }` and
   `Authorization: Bearer <token>`.
3. **Reads `results[*].frames`** (retrain.go:458-472) through the SDK's `data.Frames`
   unmarshaller; it errors if the reply has no frames or a result carries an error.

`seriesFromFrames` → `pickSeries` → `frameFields` (retrain.go:481, 526, 742) then extract the
series: `frameFields` takes the first `f.Type().Time()` field as time and every
`f.Type().Numeric()` field as a candidate; `pickSeries` matches `spec.SeriesName` (by field
name or its Grafana `fieldDisplayName`), and accepts the sole candidate when the reply is
unambiguous.

**`pkg/` is datasource-agnostic.** `grafanaPoster` "never inspects" the queries
(retrain.go:313-314) — it replays the stored `trainSource.queries` through Grafana's own query
API. No datasource field name or type name lives in `pkg/`. So "adding Abyss support" for the
unattended path is **not** a `pkg/` change at all.

### How Abyss behaves on this path (its Go backend)

Because `spec.Queries` carries `datasource: {type: "sbt-datasource-abyss", uid}`, Grafana's
`/api/ds/query` routes the query to the Abyss **Go backend** (Abyss is `"backend": true`; its
`WorkMode` is hardcoded to `"abyss"` at `pkg/plugin-impl.go:276`, so it always uses
`executeSql_abyss`):

- `initializeQueryModel` (query-handler.go:138) reads `rawSql` (→ `QueryText`) and `format`
  (→ `FormatType`) from each target's JSON. **The SQL field is `rawSql`, not `sqlQuery`** —
  a direct `/api/ds/query` probe that sends the SQL under any other key returns `frames: []`
  because `QueryText` is empty (query-handler.go:106).
- `computeTokens`/`applyTokens` (query-handler.go:199-221) expand the **same Abyss macros**
  (`$time`, `$defaultFilter`, `$from`, `$to`, `$where`, …) using the request `TimeRange` that
  Grafana sets from the `from`/`to` the scheduler sent. `computeGranularity`
  (query-handler.go:163) derives the rollup from the window width — a 7d window → `PT1H`,
  matching the browser's 7d/1h training.
- `executeSql_abyss` (client_abyss.go:152) POSTs `{ "sqlQuery": <expanded SQL> }` to
  `{url}/index/analytical/task/project/{project}/query`. The `url`, `project`, and auth
  (`auth_token`, or `abyss_user`+`password` / `X-Abyss-Use-Cert-Auth`) come from the
  **datasource's `jsonData`/`secureJSONData`** (injected by Grafana into the plugin instance),
  **not** the query target. It refuses with `project (in ds setings) not setted` if `project`
  is empty.
- `parseResult` → `parseSqlToTable` (query-handler.go:279) builds a **real `*data.Frame`**: a
  `time` field (`[]*time.Time`, marked time by `getTypeArray`'s `name == "time"` at
  query-handler.go:46) plus one numeric field per value column, **named after the SQL alias**
  (e.g. `avg`).
- The frame returns via `/api/ds/query` → `results[<refId>].frames`, which `frameFields` /
  `pickSeries` consume directly.

### The unattended path does NOT have the frame-shape bug

The interactive bug is that the **frontend** `ds.query()` returns a legacy table object with no
`fields`. The unattended path goes through the **Go backend**, which returns a proper
`*data.Frame` with a `FieldType.time` field — exactly what `frameFields`/`pickSeries` look for.
So the unattended path would retrain an Abyss row correctly **if a row existed**.

### Ordering: the interactive fix is the prerequisite

A `panel` retrain row is created only by an interactive fit (`retrain: true`), which needs
training points. Today the interactive path yields no points for Abyss (table objects →
`extractSeries` finds nothing → no fit → no row). So **Abyss cannot reach the unattended path
until the interactive fix in `src/forecast-panel/trainQuery.ts` lands.** Once that fix is in,
the row is created on first fit and the Go-backend retrain works with no further `pkg/` change.

### What "Abyss support for the unattended path" requires

No `pkg/` change. The concrete requirements:

1. **The stored `trainSource.queries` must be the full metric targets**, each carrying
   `datasource: {type, uid}`, `rawSql` (the SQL **with the macros intact** — `$time` /
   `$defaultFilter`), and `format`. The interactive path already stores exactly this:
   `source.queries = rewritten.targets` (trainQuery.ts:194), a clone of the panel targets, and
   `applyLookbackRange` (lookback.ts:341) rewrites only `${__from}` / `${__to}` / ISO literals —
   it does **not** touch the Abyss macros, so `rawSql` keeps them for the Go backend to
   re-expand against the moving retrain window. (Contrast Postgres, where `rewritePostgresSql`
   substitutes visible-bound ISO timestamps into `rawSql`.) This macro-preserving behavior is
   *why* the unattended path works for Abyss with no `pkg/` change.
2. **The datasource must be configured** with `project` (`jsonData`), `url`, and auth
   (`auth_token`, or `abyss_user` + `password`). This is a datasource-level / deployment
   requirement, independent of the forecast plugin.
3. **`spec.SeriesName` must be resolvable.** The Go backend names the value field after the SQL
   alias (e.g. `avg`). A single value column means exactly one numeric candidate, so
   `pickSeries` accepts it even if the stored `seriesName` (the browser's display name) differs.
   A **multi**-value-column Abyss query needs `seriesName` to match the intended field, or the
   retrain fails with `no series named …`.
4. **The time column must be named `time`** (or `__time`) so the Go backend marks it
   `FieldType.time`. The panel's query aliases it `as "time"`, so this holds. An Abyss query
   that aliased the time column to anything else would yield a frame `frameFields` cannot read
   → `no time series in /api/ds/query reply`.

### Verification (after the interactive fix)

1. With the fix deployed, load the Abyss overlay panel and confirm a `panel` row appears on the
   Retrain schedules page (`?page=schedules`) with a `source` object naming the Abyss
   dashboard/panel and a query summary.
2. Force (or await) a retrain tick and confirm `last_status` is `ok` (not an error); the
   scheduler log reports claimed/retrained/failed counts. If it fails, the plugin log names the
   cause — `frameFetchTimeout`, `/api/ds/query status …`, `no frames`, `no series named …` —
   which map to requirements 1-4 above (routing/auth, the `rawSql`/`datasource` shape, and the
   `seriesName`/time-column matching respectively).

## Verification steps (after the fix)

1. Rebuild `dist/` and deploy to `/opt/grafana/public/plugins/forecaster/` (per
   `docs/INSTALL.md`).
2. Open dashboard `bfxcl9n`, panel 1 as **Forecast overlay**, time range `now-6h`.
3. Watch the network. Expected sequence: visible proxy query → probe `POST /forecast`
   (`{"needTrain":true}`) → **training proxy query (168 rows)** → **fit `POST /forecast`**
   (this one was missing before) → forecast points drawn on the panel.
4. Confirm the overlay shows the forecast line/band, not "Training query returned no points".

## Appendix — exact code references

Forecast plugin (this repo, `timeseries-grafana`):
- `src/forecast-panel/trainQuery.ts` — `queryTrainingFrames()` line 101; `ds.query()`
  ~164-177; `frames.push(...resp.data)` line 205; `source.queries = rewritten.targets` line
  194. **Interactive fix goes here.**
- `src/forecast-panel/extract.ts` — `extractSeries()` line 22; `FieldType.time` gate line 26.
- `src/forecast-panel/overlayLoad.ts` — line 114 `extractSeries` over `train.frames`;
  115-116 `trained.length === 0` → `REASON_TRAIN_EMPTY`.
- `src/forecast-panel/reasons.ts` — `REASON_TRAIN_EMPTY` = "Training query returned no points".
- `src/forecast-panel/trainRewrite.ts` — `rewriteTrainTargets()` line 55; Abyss (unknown type)
  falls to `applyLookbackRange` (line 71).
- `src/forecast-panel/lookback.ts` — `applyLookbackRange()` line 341 (clones targets; rewrites
  only `${__from}`/`${__to}`/ISO literals, not the Abyss macros).
- `pkg/plugin/retrain.go` — unattended retrain: `grafanaPoster.fetchFrames()` line 401 (posts
  the stored `spec.Queries` to `/api/ds/query`); `seriesFromFrames()` line 481; `pickSeries()`
  line 526; `frameFields()` line 742; `retrainSpec` (embeds `TrainSource`) line 254.
- `pkg/plugin/forecast.go` — `TrainSource` struct line 28 (`Queries json.RawMessage`,
  `SeriesName`, `Relative`/`LookbackMs`).

Abyss datasource (`pvv_datasource_pvm`, v2.0.0):
- `src/DataSource.ts` — `query()` line 245; `frontQuery()` line 185; format dispatch 230-239
  (frontend path, returns the table object).
- `src/SqlParser.ts` — `parseSqlToTable()` line 205 (returns the table object);
  `parseSqlToSeries()` line 142; `processRow()` line 26.
- `src/ProxyParams/abyssParams.ts` — proxy URL `/index/analytical/task/project/<project>/query`.
- `pkg/plugin-impl.go` — `QueryData()` line 53; `query()` line 85 (empty `QueryText` → no
  frames, line 106); `WorkMode` hardcoded `"abyss"` line 276; `DataSourceOptions` /
  `instanceSettings` (project/url/auth from `jsonData`/`secureJSONData`) line 193-333.
- `pkg/query-handler.go` — `queryModel` struct line 110 (`QueryText json:"rawSql"`,
  `FormatType json:"format"`); `initializeQueryModel()` line 138; `computeGranularity()` line
  163; `computeTokens()`/`applyTokens()` line 199-221 (Abyss macro expansion); `parseResult()`
  line 231; `parseSqlToTable()` line 279; `getTypeArray()` line 42 (a `time` column is the time
  field).
- `pkg/client_abyss.go` — `executeSql_abyss()` line 152; `abyss_executeSql2()` line 126 (POST
  `{ "sqlQuery": … }`); `project`/auth from `instanceSettings`.
