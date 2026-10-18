# Brief: make the unattended retrain fetch training data through PVV `/gse/data/v1/DataByRefId`

Report for the plugin developer (AI agent). Self-contained. It explains why the backend
retrain cannot run an `sbt-datasource-abyss` query headless today, documents the real PVV
endpoint that solves it (the custom Grafana plugin `pvv_plugin`, which owns `/gse/data`), and
the exact change to make in this repo (`timeseries-grafana`).

Read with [ABYSS_TRAINING_BUG_REPORT.md](ABYSS_TRAINING_BUG_REPORT.md). This brief is now
grounded in the **server source** (`/Users/kolotushin.e.v/GolandProjects/pvv_plugin`), which
corrects the client-side inference made earlier — see
[Reconciling](#reconciling-corrects-the-earlier-inference).

## TL;DR — the task

The retrain scheduler fetches training frames by posting to Grafana's
`POST /api/ds/query` with `Authorization: Bearer <token>` (`grafanaPoster.fetchFrames`,
`pkg/plugin/retrain.go:401`). That works for datasources whose credential lives in the
datasource config (Prometheus, Postgres, …). It does **not** work for Abyss in our PVV
deployment: on a panel Abyss authenticates to its analytical backend with the **logged-in
user's** credential that Grafana forwards — and a headless retrain has no user.

PVV already exposes a b2b endpoint that removes the user requirement. The plugin must
authenticate to it **by mTLS client certificate** (a CN on PVV's allowlist) and pass the
**panel identity**; PVV then resolves the query itself and supplies the datasource credential.

```
POST {PVV_URL}/gse/data/v1/DataByRefId        # auth: mTLS client cert (allowlisted CN)
body: { "org_id": <int64>, "dashboardUID": "<str>", "panelId": <int>,
        "refId": "<str>", "from": <int64 ms>, "to": <int64 ms>,
        "queryHash": "<optional sha256 hex>" }
201: { "frames": [ { "schema": {…fields…}, "data": { "values": [[…ts…],[…num…]] } } ],
       "status": 200 }                        # a single DataResponse (NOT a {results:…} envelope)
```

PVV reads the **panel's own query by `refId` from the live dashboard**, injects the Abyss
org/panel params, mints a **per-org Grafana service-account token**, calls Grafana's own
`/api/ds/query` with it, and returns that one `refId`'s `DataResponse`. So the plugin
provides **no datasource credential at all** — it only needs its client-cert CN allowlisted.

**The change:** add a DataByRefId fetch mode to the poster; capture `refId` per series; keep
each request's window under PVV's max range (default **24h**) by chunking; parse the returned
frames with the SDK; leave the existing `/api/ds/query` mode available when no PVV client
certificate is configured. Everything from `seriesFromFrames` down (`pickSeries`,
`frameFields`, fit, retention, leases) is unchanged.

## Implementation decisions

These supersede the earlier inference where they differ:

- **The transport is selected by configuration, not by datasource type.** PVV mode is on when
  `FORECAST_PVV_CLIENT_CERT` and `FORECAST_PVV_CLIENT_KEY` are configured; otherwise the
  existing Grafana `/api/ds/query` mode runs unchanged. `pkg/` contains no datasource type or
  field name.
- **PVV mode does not call `/orgs` or `/api/org`.** It claims every due `scope='panel'` row
  regardless of org and sends that row's `org_id` with the request. There is no per-row
  `/api/ds/query` fallback: a PVV failure is the row's failure.
- **Windows chunk at `24h`**, not `4h`; `FORECAST_RETRAIN_WINDOW_CHUNK` can lower the slice but
  a value above the 24h server cap falls back to 24h.
- **`queryHash` is not sent in v1.** The live panel query is retrained as-is; drift pinning is
  deferred.
- **`refId` is captured per series at fit time.** A row written by an older frontend derives it
  only when exactly one query is stored; otherwise the row asks for a panel reload.
- The endpoint and its response are PVV's, not Grafana's own `/api/ds/query`: the request is
  `POST {FORECAST_PVV_URL}/gse/data/v1/DataByRefId` with mTLS and no bearer token.

## What `DataByRefId` actually does (server flow)

`GetDataByRefId` (`pvv_plugin/internal/infrastructure/usecase/metro/api.go:28`):

1. **CN-check middleware** (`CnCheckerMiddleware`) gates the route — client certificate, CN
   allowlisted (see [Auth](#auth-server-truth)).
2. Decode + **validate** the body (`models.Panel.Validate`, `pkg/models/metro.go:31`): all of
   `org_id, dashboardUID, panelId, refId, from, to` required and **non-zero**, and
   `to - from ≤ max_time_range_sec` (config, default **86400s = 24h**) → else `400`.
3. **Dashboard-block check** — an admin block on the dashboard → `403`.
4. **Resolve the query by `refId` from the live dashboard** (`GetDashboardQueries` →
   `GetTargetByRefID`, `dashboard.go:20,48`; dashboard read from the Grafana bus, cached). No
   panel → `404 ErrDashboardNotFound`; no such `refId`/panel → `404 ErrNoTargets`.
5. **Mint a credential** — `tokenService.GetToken(org_id)` (`token/token_service.go:167`) → a
   per-org Grafana service-account token PVV holds.
6. **Optional `queryHash` pin** — if sent, PVV computes `sha256(targetQuery)` and `400`s on
   mismatch (`api.go:87-94`). This is the drift guard for a changed panel query.
7. **`InjectAbyssParams`** (`utils.go:38`) — only for `sbt-datasource-abyss` targets: stamps
   `orgId/dashboardId/dashboardUId/panelId` onto the query so the Abyss Go backend adds its
   b2b headers. Other datasource types pass through unchanged.
8. **Call Grafana's own `/api/ds/query`** (`queryDashboardData`, `api.go:176`) with
   `{queries:[<query>], from:"<ms>", to:"<ms>"}` and `Authorization: Bearer <per-org token>`,
   `Host` pinned to `GrafanaDomain`.
9. Require exactly **one** result whose key equals `refId`, and return it:
   **`201 Created`** with that single `DataResponse`.

The sibling route `POST /gse/data/v1/RefIdList` (`{org_id, dashboardUID, panelId}` →
`[{ref_id, query, hash}]`, `api.go:131`) lets a client **discover a panel's refIds and their
query hashes** — how the plugin can learn the `refId`/`queryHash` to pin.

## Request contract (exact — `models.Panel`, `pkg/models/metro.go:21`)

| Field | JSON key | Type | Notes |
|---|---|---|---|
| Org | `org_id` | int64 | required, non-zero. The datasource/dash resolves **in this org** |
| Dashboard | `dashboardUID` | string | required |
| Panel | `panelId` | int | required, **non-zero** (`panelId==0` is rejected as "missing") |
| Query | `refId` | string | required. PVV resolves this query from the panel |
| Window | `from` | int64 | required, **unix-ms integer** (non-zero) |
| Window | `to` | int64 | required, unix-ms integer; `to-from ≤ max_time_range_sec` (default 24h) |
| Pin | `queryHash` | string | optional; `sha256hex(targetQuery)` — rejects (`400`) if the panel query changed |

Note the casing is exactly `org_id`, `dashboardUID`, `panelId`, `refId`, `from`, `to`,
`queryHash`, and `from`/`to` are **integers** (PVV re-stringifies them for the inner
`/api/ds/query`; the caller does not).

## Response contract (exact)

- Success: **HTTP `201`**, body is a single `models.DataResponse` (`pkg/models/metro.go:149`) —
  **not** the `/api/ds/query` `{results:{refId:…}}` envelope:

  ```jsonc
  { "frames": [ {
      "schema": { "name":"…", "refId":"A",
                  "fields": [ {"name":"time","type":"time"}, {"name":"avg","type":"number"} ],
                  "meta": { "executedQueryString":"…" } },
      "data": { "values": [ [1720656000000, …], [13319.2, …] ] }   // columnar: values[i] ↔ fields[i]
    } ],
    "status": 200, "error": "", "errorSource": "" }
  ```

  `data.values` is the standard Grafana frame columnar form (`values[i]` is field `i`'s
  column). The Grafana SDK's `data.Frame` unmarshaller reads this directly — do **not**
  hand-parse `values[0]/values[1]`.
- Also check the body's own `error`/`status`: a datasource-level failure can arrive as `201`
  with `DataResponse.error` set (PVV only 5xx/4xx-reflects the *transport* status from
  Grafana, `api.go:104-112`).
- Errors: `400/401/403/404/500` with `{"message":"…"}` (`utils.go:14`). Common:
  `401` bad/missing client cert (CN), `403` dashboard blocked or CN not allowlisted,
  `404` dashboard/panel/refId not found, `400` missing field / range exceeded / `queryHash`
  mismatch / `refId` mismatch.

## Auth (server truth)

`CnCheckerMiddleware` (`middleware/cn_checker.go:82`) authenticates **by client certificate**;
there is **no bearer/api-key path** on this route:

- **Direct mTLS**: the request arrives over TLS with a client cert verified against PVV's
  client CA (`r.TLS.VerifiedChains`); its **CN** must be in `AllowedCNs`.
- **Behind an mTLS-terminating proxy**: when `UseHeaderXForwarderClientCert` is set, PVV reads
  the `X-Forwarded-Client-Cert` (XFCC) header (works with `noSSL=true`), verifies the chain
  against `CnCaCertPath`, and checks the CN.
- `AllowedCNs` containing `*` disables the CN check.

So the plugin must **present a client certificate whose CN PVV allowlists** (or ride an
mTLS-terminating proxy that forwards it as XFCC). The datasource credential the panel used to
get from the user is supplied by PVV (`tokenService.GetToken(org)`), never by the plugin.

## Metric identity → what the plugin must store

A retrain is fetched per **series**, and the endpoint keys on `{org_id, dashboardUID, panelId,
refId}` (+ optional `queryHash`). This mirrors how `aiom` keys a metric
(`org_id + dashboard_uid + panel_id + refId + rawSql`, `service/baselines/baselines.py:41`).

| Needed by DataByRefId | Where the plugin has it |
|---|---|
| `org_id` | the schedule row's org — already known at fetch time |
| `dashboardUID`, `panelId` | already in `TrainSource` / `PanelProvenance` (`forecast.go:28,83`) |
| `refId` | **not stored** — only inside the opaque `Queries`. Must be captured into `TrainSource` and the per-series identity |
| `queryHash` | **not stored** — capture the `sha256` of the query the row trained on; discover via `/gse/data/v1/RefIdList` |

## Mapping the `aiom` client to this repo

`aiom` (`reworked/datasources/pvv.py`) is one caller of the same endpoint — its
`_build_metric_query_json`/`_execute_query` send this body and parse `frames[].data.values`,
and it authenticates by **mTLS client cert** (its `X-PVM-API-KEY`/`Bearer` lines are commented
out, because the route is CN-checked, not token-checked). Chunking (`window_data_period`, 4h)
keeps each slice under the 24h server cap.

| `aiom` (`pvv.py`) | this repo |
|---|---|
| `_execute_query` → `{PVV}/gse/data/v1/DataByRefId` | new `fetchSeries` strategy (alongside `/api/ds/query`) |
| builds `{org_id,dashboardUID,panelId,refId,from,to}` (int ms) | same builder, fed from `TrainSource` + resolved window |
| auth = mTLS client cert | **client cert with allowlisted CN** (not a bearer token) |
| parses `frames[].data.values` | unmarshal the bare `DataResponse` frames via the SDK `data.Frame` |
| metric key `org_id+dashboard_uid+panel_id+refId+rawSql` | `TrainSource` must gain `refId` (`queryHash` deferred) |
| chunks long windows (4h) | chunk to ≤ PVV `max_time_range_sec` (default 24h), sequentially |

## Concrete requirements

1. **Add a DataByRefId fetch strategy** on the poster (`pkg/plugin/retrain.go`), posting the
   exact `models.Panel` body to `{PVV_URL}/gse/data/v1/DataByRefId` over mTLS. The mode is
   selected by configuration — PVV client certificate/key present → DataByRefId, absent →
   `/api/ds/query` — not by datasource type. This stays **datasource-agnostic in `pkg/`**: the
   call is to PVV, a b2b service, and the Abyss-specific work (param injection, credential,
   SQL/macro expansion) is done inside PVV/Abyss.
2. **Capture `refId` per series** into `TrainSource` and the fit's series identity
   (`forecast.go:28`). `org_id` is the row's org; `dashboardUID`/`panelId` already ship. A row
   written by an older frontend derives `refId` from its single stored query.
3. **A `DataResponse` parser**: unmarshal the `201` body and feed its `frames` to the SDK
   `data.Frame` unmarshaller so `seriesFromFrames`/`pickSeries`/`frameFields`
   (`retrain.go:481/526/742`) work unchanged. Treat a non-empty `DataResponse.error` as a
   fetch failure.
4. **Chunk every window to ≤ `max_time_range_sec` (default 24h).** Add
   `FORECAST_RETRAIN_WINDOW_CHUNK` (default and maximum `24h`) and issue one DataByRefId call
   per slice, merging the chunk series and applying the existing reply and train-point bounds.
   Without this, a 7d/14d/56d lookback is rejected `400 panel time range exceeded`.
5. **Do not send `queryHash` in v1.** Deferred; the live panel query is fetched as-is.
6. **One call per `refId`/series.** DataByRefId returns exactly one `refId`'s result; the
   overlay already fits one series per request, so map each fitted series to its query's
   `refId`.
7. **Config, same precedence as the store** (v7/v16): `FORECAST_*` → `GF_PLUGIN_*` → grafana.ini
   → jsonData for the plugin; **process env only** for the compute service. Add
   `FORECAST_PVV_URL` (defaulting to `FORECAST_GRAFANA_URL`), `FORECAST_RETRAIN_WINDOW_CHUNK`,
   and mTLS material `FORECAST_PVV_CLIENT_CERT` / `FORECAST_PVV_CLIENT_KEY` /
   `FORECAST_PVV_CA_CERT` (+ `FORECAST_PVV_TLS_SKIP_VERIFY`). **No bearer token / API key is
   sent to this endpoint** — drop the earlier idea of reusing `FORECAST_GRAFANA_TOKEN` here.
   There is no `FORECAST_RETRAIN_FETCH` switch: the cert pair is the switch. Cert/secret
   material is deployment config, never rendered on the plugin's config pages. The CN itself
   must be added to PVV's `AllowedCNs` (a PVV-side deployment step).
8. **Tolerate transient failures** the existing way (v15 lease + `attempts` backoff): Abyss /
   the `/gse/data` API can be briefly down; a failed fetch burns no fit and the row retries —
   do not lose the row (cf. aiom's `PVM-185894` "keep pushing baselines while the Abyss API is
   down" and its `PVM-196761` worker throttle).

## Constraints (confirmed from the source)

- **The panel and its `refId` must exist at retrain time** (the query is read from the live
  dashboard, `dashboard.go:48`); a deleted panel or removed refId → `404`.
- **A panel query edit changes what a retrain fetches.** v1 does not send `queryHash`, so it
  accepts that drift. DataByRefId does **not** take SQL from the caller.
- **Window width ≤ 24h (default cap)** — chunk. (`Panel.Validate`, `metro.go:89`.)
- **`panelId` must be ≥ 1** — `0` is rejected as missing. (Overlay panels are normally ≥ 1.)
- **Exactly one result, `refId` must match** the request (`api.go:114-123`).
- **DataByRefId is generic**: it works for any datasource type; only `sbt-datasource-abyss`
  targets get `InjectAbyssParams` (`utils.go:49`).
- **Admin dashboard block → `403`**; a blocked dashboard cannot be retrained until unblocked.

## Reconciling (corrects the earlier inference)

- [ABYSS_TRAINING_BUG_REPORT.md](ABYSS_TRAINING_BUG_REPORT.md)'s unattended section assumed *no
  `pkg/` change is needed* on the premise that the Abyss datasource carries a b2b
  `auth_token`/cert so `/api/ds/query` → the Go backend authenticates headless. That premise
  does not hold in the PVV ST deployment (Abyss forwards the panel **user's** credential). This
  brief supersedes it: fetch via PVV `/gse/data/v1/DataByRefId`, which **supplies the
  datasource credential itself** (per-org token + Abyss-param injection). The plugin only needs
  its **client-cert CN allowlisted**, the `refId` identity, and window chunking.
- There is **no user token for the plugin to provide** — that earlier framing was wrong. PVV
  resolves it (`tokenService.GetToken(org)`).
- The `aiom` client and its `PVM-196761` throttle corroborate chunking/gentleness, and confirm
  CN/mTLS (its token headers are commented out).

## Verification

1. **Unit (Go, table-driven):** the `models.Panel` body builder (exact keys, integer ms, all
   required fields present, no bearer token or `queryHash`); the bare-`DataResponse` →
   `data.Frames` parse (columnar `schema.fields`/`data.values`; a null/NaN value dropped;
   `error` treated as failure); chunk split of a 7d window at 24h; mode selection by client
   certificate presence; no `/api/ds/query` fallback from PVV mode.
2. **Live (ST):** provision the plugin's client cert on PVV's `AllowedCNs`, set
   `FORECAST_PVV_CLIENT_CERT` / `FORECAST_PVV_CLIENT_KEY` and the PVV URL. Train an overlay
   once (creates the `panel` row with `refId`), force a retrain tick, and confirm the row's
   `last_status` is `ok`. Map failures: `401`→cert/CN, `403`→CN-not-listed / block,
   `404`→panel/refId missing, `400`→field/range, `DataResponse.error`→datasource.
3. **Regression:** with no PVV client certificate configured, the existing `/api/org` +
   `/api/ds/query` behavior is unchanged.

## Appendix — code references

`pvv_plugin` (the `/gse/data` server — authoritative):
- `internal/infrastructure/usecase/metro/api.go` — routes `InitMetroRouter` 22;
  `GetDataByRefId` 28 (validate 36, block-check 41, token 71, `queryHash` 87,
  `InjectAbyssParams` 102, single-result/refId 114-123, `201` 124); `GetRefIdList` 131;
  `queryDashboardData` (inner `/api/ds/query`, Bearer per-org token) 176.
- `pkg/models/metro.go` — `Panel` + `Validate` 21-54; `PanelRequest` 66;
  `DatasourceResponse`/`DataResponse`/`DataFrame`/`FrameData{values}` 145-191;
  `GetRefIdListRequest` 119; `RefIdItem` 138.
- `internal/infrastructure/usecase/metro/dashboard.go` — `GetDashboardQueries` 20;
  `GetTargetByRefID` 48; bus read + cache 58-97.
- `internal/infrastructure/usecase/metro/utils.go` — `InjectAbyssParams` 38 (abyss type only);
  `writeErrorResponse`/`WriteResponseJson` 18-34.
- `internal/infrastructure/usecase/metro/metro.go` — `api_prefix` default `/gse/data` 50;
  `max_time_range_sec` default 86400s→ms 89.
- `internal/infrastructure/middleware/cn_checker.go` — `CnCheckerMiddleware` 82 (TLS chain or
  XFCC; CN allowlist; `*` skips).
- `internal/infrastructure/token/token_service.go` — `GetToken(orgID)` 167.
- `internal/infrastructure/usecase/metro/api_test.go` — request bodies / 201 / error shapes
  57-176; Abyss-injection case #18.

`aiom` (a client of the same endpoint):
- `reworked/datasources/pvv.py` — `_build_metric_query_json` 110; `_execute_query` (DataByRefId,
  mTLS, token headers commented) 305-357; `_parse_pvv_response` 239; `split_time_window` 436.

This repo (`timeseries-grafana`):
- `pkg/plugin/retrain.go` — `fetchSeries` (add the strategy); `fetchFrames` 401; request 416-435;
  `dsQueryResponse` 393-472; `seriesFromFrames`/`pickSeries`/`frameFields` 481/526/742; window
  resolution 406-414.
- `pkg/plugin/forecast.go` — `TrainSource` 28 / `PanelProvenance` 83 (add `refId`; `queryHash` deferred).
- `pkg/plugin/limits.go` / `resources.go` — reply/train-source bounds (apply per chunk).
- See [ABYSS_TRAINING_BUG_REPORT.md](ABYSS_TRAINING_BUG_REPORT.md) for the frontend
  frame-shape fix and Abyss datasource internals.
