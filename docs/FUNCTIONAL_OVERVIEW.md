# Functional overview

What a caller can invoke in `timeseries-grafana` and `timeseries-baselines`, as whom, configured how, with what
input, and what comes back — plus a positive and a negative scenario observed live on two environments.

`timeseries-grafana` ships three Grafana plugins from one backend binary (`gpx_forecast`): the **app**
(`eduardkolotushin-forecast-app`, HTTP resources, snapshot store, retrain scheduler, two configuration pages),
the **overlay panel** (`eduardkolotushin-forecast-panel`, draws history + forecast + interval bands) and the
**Forecast datasource** (`eduardkolotushin-forecast-datasource`, a second `gpx_forecast` process that restores
snapshots for Grafana alerting). `timeseries-baselines` is a separate process with **no HTTP surface**: it
reads Druid, fits minute-of-week baselines, publishes them to Kafka and owns the `baseline` half of the retrain
queue.

**How to read a row.** *Who can use* is the actor that can actually call it, with the live evidence that
established it: `Any user` = any Grafana identity that reaches the plugin route (including anonymous when
`GF_AUTH_ANONYMOUS_ENABLED` is on, which both test environments use with `ORG_ROLE=Admin`); `Admin` = Grafana
org Admin only; `Operator` = whoever deploys the process (env/values, no Grafana identity); `Alert rule` =
Grafana unified alerting evaluating a datasource query; `Panel` = a dashboard user through the panel options.
*Expected result* quotes the literal string the caller sees; error strings are the plugin's own, not Grafana's.

Commands are re-runnable as written. In the Compose environment the shell variables are:

```bash
B=http://localhost:3000/api/plugins/eduardkolotushin-forecast-app/resources   # app resource route
G=http://localhost:3000                                                       # Grafana
PG='docker compose exec -T overlay-postgres psql -U overlay -d overlay -tAc'  # overlay Postgres
KAFKA='docker compose exec -T kafka /opt/kafka/bin/kafka-console-consumer.sh --bootstrap-server localhost:9092'
```

`docker compose` commands run in `C:/Users/Eduard/Cursor/timeseries-grafana-sandbox`. In the Kubernetes
environment the same route is `B=http://localhost:80/api/plugins/eduardkolotushin-forecast-app/resources` — the
LoadBalancer's host port, published by Docker Desktop's kind cloud provider (see
[Environment under test](#environment-under-test)); `kubectl -n timeseries port-forward svc/timeseries-grafana
30001:80`, which the sandbox `make helm-grafana` runs, answers the same. Postgres is reached with
`kubectl -n overlay-postgres exec deploy/overlay-postgres -- psql -U overlay -d overlay -tAc "<sql>"`.

## Environment under test

Both environments were first exercised on 2026-09-21 (13:07–14:2x MSK) from the same source revisions, and both
were left running afterwards. A third pass the same day (14:16–14:31 UTC) added a **temporary second release** of
the same chart with two Grafana replicas over one Postgres, to observe the HA claim
[Scaling and HA](#scaling-and-ha); it was removed again at the end of the run.

The **rows below carry the remediated state**, re-measured on 2026-09-22 (12:39–13:00 UTC) on that same
still-running pair of stacks; a transcript that quotes a build hash quotes the build *its own* run used, so the
2026-09-21 numbers in the discrepancy sections stay as observations of that day.

| | Compose sandbox | Kubernetes (Helm) |
| --- | --- | --- |
| Grafana | 13.1.0 (`commit b309c9bb3b81a748c3a75289236a27309ed2566a`), `http://localhost:3000`, anonymous Admin, org 1 | 13.1.0, `svc/timeseries-grafana` — LoadBalancer `EXTERNAL-IP 172.18.0.5:80` (a cluster-internal address); the host reaches it at `http://localhost:80` through Docker Desktop's kind cloud provider (`kindccm-…`, `envoyproxy/envoy:v1.36.7`, `0.0.0.0:80->80/tcp`), and `kubectl -n timeseries port-forward svc/timeseries-grafana 30001:80` (what `make helm-grafana` runs) serves the same — anonymous Admin, org 1 |
| Plugin build | **2026-09-22 (head `74b12e4`)**: `dist/` mounted from the workspace — `gpx_forecast_linux_amd64` sha256 `33b9ede91619f7c3caf22f0ffcc2edb595b936710b6d78e0da7232185522c885`, `forecast-panel/module.js` sha256 `0b3b25d0b56727ff7081524686ff108f792bc0efccbb18fc850ec1fcc4fb4226` — both byte-identical inside the Grafana container. The binary embeds `vcs.revision=74b12e46dc28`, so its hash moved with the docs-only commit while the panel bundle is the fix rebuild's. For reference: pass 1 measured `34bf53244166948b6a0bbfc9fb79f942da2a4dbd229b54a5b1cba91ae98b43d6` / `42564adfe17e496b3463f344d49fd7c0db3477c978f0e0e5fb3c7e8e13977f15`, the discrepancy rebuild `a034c1ca02c028f065f0bc8eb206e8fec12b950a04d4f22126c9fbfae7c1d4df` / `429a2f99d4257049fdc13c4b7132df4c5a92600b3c0b8d586d6194808e6b4c80` | images built from the pinned refs and tagged by the **pin's short sha**: `ghcr.io/eduard-kolotushin/timeseries-grafana:8feecafc14ba` and `…-baselines:7ec489faafeb`, imported into the node's containerd by the sandbox's `make helm-import` (`docker save … \| docker exec -i desktop-control-plane ctr -n k8s.io images import -`); the pass-3 `--set grafana.configRevision=…` run rolled the Deployment to `timeseries-grafana-7f89fd5cb-nr9zw` |
| Source revisions | **2026-09-22**: `timeseries-grafana` `74b12e46dc282a4663addb7ee9d323149a07dbca`, `timeseries-baselines` `7ec489faafeb85971dd5f5c247aaf0bc37c63913`; the plugin depends on the tags `timeseries v0.1.1` (`e74ecaa`) and `timeseries-forecast v0.5.0` (`029c690`). Pass 1 measured `d6399e5a…` / `179a155…`; the discrepancy fixes added `10a23cc` and `8f2a9ff` | the same two commits, baked into the images by `timeseries-k8s` `2406007`'s `PLUGIN_REF=8feecafc14ba…` / `BASELINES_REF=7ec489faafeb…` (the chart pins an *ancestor* of this repo's head when the trailing commits are docs-only, which `make check-pins` accepts and reports) |
| Data plane | Druid 37.0.0 (`http://localhost:8888`, datasource `druid`, tables `minuteweek`/`metrics`/`baselines`), Kafka 3.9.1 (`metrics`, `baselines`), OpenSearch 2.18.0, Prometheus 2.55.1, overlay Postgres 17.6 (schema `forecast`) | Helm releases `kafka`, `druid`, `prometheus`, `opensearch`, `overlay-postgres`, `timeseries` — all `deployed` on one kind node (`desktop-control-plane`, v1.36.1, containerd 2.3.1) |
| Worker | `alpine:3.21` + `/usr/local/bin/baselines` (2026-09-22 build sha256 `759a9be5280e986e56a179c28befe5cc38f405b1b9de1efb9080fada4ff01e46`; pass 1 measured `c725417cbd3a84cbb97258b8d40e5368bd9e2434f18373f938c26d748a072c7a`), env from `docker-compose.yaml` | Deployment `timeseries-baselines` (`SHARD_DNS=timeseries-baselines-headless`, `SHARD_MEMBERSHIP=store`), env from ConfigMap `timeseries-baselines-env` |
| Configuration source | provisioning `provisioning/plugins/apps.yaml` + `docker-compose.yaml` env | ConfigMaps `timeseries-forecast-app` (app `apps.yaml`), `timeseries-forecast-datasource`, `timeseries-forecast-store`, `timeseries-baselines-env` |
| Notable | 17 containers (pass 1 counted 18), ~4 GiB resident | 15.6 GiB allocatable; the two stacks ran **concurrently** without an OOM (≈4 GiB used); no metrics-server |

The Kubernetes plugin image is still not *pulled* from GHCR (`timeseries-k8s` carries no `v*` tag, so the two
`:0.1.0` tags are unpublished), but the sandbox no longer asks it to be: `make helm-images` builds both images
from the Dockerfiles' pinned sibling refs, tags them by the pin's short sha (`…-grafana:8feecafc14ba`,
`…-baselines:7ec489faafeb` — an immutable tag, which `pullPolicy: IfNotPresent` cannot mask behind a cached
one), and `make helm-import` loads them into the node's containerd; `make helm-up`/`helm-refresh` run the import
before the upgrade, so the pods come up `1/1 Running` without a manual `ctr` step. The Compose sandbox needs
none of that (it mounts the workspace `dist/`: the in-container `gpx_forecast_linux_amd64`,
`forecast-datasource/gpx_forecast_linux_amd64` and `forecast-panel/module.js` are byte-identical to the host
files, sha256 `33b9ede9…`, `33b9ede9…` and `0b3b25d0…` on 2026-09-22). The Kubernetes dashboards are
provisioned **read-only** (`Cannot save provisioned dashboard`), so panel checks that need an edit run against a
throwaway copy of `forecast-minute-week`, which is deleted afterwards; Compose dashboards are writable and are
restored from `provisioning/dashboards/minute-week.json` after such a check.

## timeseries-grafana functions

### F1. `GET /ping` — liveness

| | |
| --- | --- |
| **Function** | Prove the app backend process answers; used as a warm-up because the first resource call is what builds the app instance. |
| **Who can use** | Any user. Live: anonymous `GET` returned 200 on both environments (no Admin gate on this route). |
| **How configured** | Nothing; the route exists as soon as Grafana loads the app plugin. |
| **Input params** | None. |
| **Expected result** | 200, `{"message":"ok"}`. An unknown path under the route answers Grafana's mux: 404 `404 page not found`. |

**Positive — Compose:** `curl -s $B/ping` → `{"message":"ok"}`

**Negative — Compose:** `curl -s -w ' http=%{http_code}\n' $B/nope` → `404 page not found` (http 404)

**Kubernetes:** `curl -s $B/ping` → `{"message":"ok"}` (http 200); same build, same answer.

### F2. `POST /forecast` — fit, probe, restore

| | |
| --- | --- |
| **Function** | One request shape for three jobs, chosen by the body: **fit** (`times`+`values`), **fit and persist** (fit + `cacheKey`), **restore** (`cacheKey`, no points). |
| **Who can use** | Any user — no Admin gate (unlike `/schedules`). Live: the overlay panel (anonymous Admin) and an unauthenticated `curl` both got 200. |
| **How configured** | Nothing beyond the plugin being enabled; the store DSN decides whether a `cacheKey` persists (F20). |
| **Input params** | `times` (int64 ms, ascending, unique), `values` (number or `null`), `model` (`naive`\|`mean`\|`drift`\|`seasonal`\|`baseline`\|`ses`\|`holt`), `from`/`to` (int64 ms, the forecast window), `alpha`, `beta`, `period`, `season` (`hour`\|`day`\|`week`\|`minute-week`), `calendar` (`""`\|`ru`), `level` (0..1; 0 omits bands), `cacheKey` (64 lowercase hex), `retrain` (bool), `trainSource`, `provenance`. Limits: ≤100000 training points (`413 forecast: training series exceeds 100000 points`), one emitted window capped at 1000000 points (413, checked before the fit), 4 concurrent Fit/ForecastRange calls (`429 forecast: busy`). Bodies are capped twice: a declared `Content-Length` above `2 × 100000 × 32 B = 6400000` is refused **before** decoding with `413 forecast: request body too large for a legal training series`, and one above the 16 MiB body cap with `413 forecast: request body too large`; a body past the 32 MiB transport ceiling never reaches the handler (Grafana answers its own 500 `grpc: received message larger than max`). The pre-flight reads `Content-Length` only, so a chunked body is bounded by the decoder's `MaxBytesReader` instead — this is not reachable through Grafana's proxy, which buffers the body and re-sends it with a known length (verified 2026-09-22: the same 14.23 MB body sent `Transfer-Encoding: chunked` still answered the pre-flight's 413). |
| **Expected result** | 200 with `times`/`values` (and `lower`/`upper` when `level≠0`); points start at `last_time + step` and are clipped to `[from,to]`. An empty fit → 400 `forecast: series is empty`. With `cacheKey`+points the snapshot is stored and the panel's schedule row is written. |

**Positive — Compose:** 300 points ending now, `model:"holt"`, `from=now`, `to=now+2h` →
`200`, `len(times)=120 len(values)=120`, first point `now+60s`, last `now+7200s`, no nulls (1-minute step of the
model grid, `k≥1`).

**Negative — Compose:** `POST $B/forecast -d '{"cacheKey":"nope"}'` → 400 `forecast: cacheKey must be 64 lowercase hex chars`;
`-d '{"times":[],"values":[],"model":"holt","from":1,"to":2}'` → 400 `forecast: series is empty`;
`model:"nope"` with otherwise valid points → 400 `forecast: unknown model: nope`.

**Kubernetes:** the same three requests → 200 (`len=120`), 400 `forecast: cacheKey must be 64 lowercase hex chars`,
400 `forecast: unknown model: nope`. `POST` with 100001 points → **413** `forecast: training series exceeds 100000 points`
on both environments.

### F2b. Prediction interval bands (`level`)

| | |
| --- | --- |
| **Function** | Add Hyndman Gaussian `lower`/`upper` around the forecast. |
| **Who can use** | Any user. |
| **How configured** | Request `level` (panel option *Interval coverage*, default `0.95`, gated by *Show prediction interval*, default on). |
| **Input params** | `level` in (0,1). `level: 0` (or omitting it) returns no bands. |
| **Expected result** | 200 with `lower`/`upper` of the same length as `values`, strictly positive width. |

**Positive — Compose:** fit + `"cacheKey":"0f…0f"` (+ `level: 0.95`) → 200, `lower=120 upper=120`,
`band>0 = 120/120`, first width `3.005`.

**Negative — Compose:** 8 concurrent 100000-point fits at the default 4 slots → all 200 (the host is fast);
with `maxInflight` lowered to `1` through the app jsonData → `statuses=[200,200,429,200,200,200,429,200]`,
`n429=2`, body `forecast: busy` (**http 429**). No tight retry in the overlay, so the panel simply shows the
reason for that refresh.

**Kubernetes:** fit + `level: 0.95` → 200, `lower=120 upper=120`, first width `3.005` (identical to Compose).

### F2c. Probe / cached load

| | |
| --- | --- |
| **Function** | Ask for the saved model without sending points, so a warm dashboard skips the training query. |
| **Who can use** | Any user. |
| **How configured** | Panel option *Saved model* ("Reuse the fitted snapshot until Retrain or a query, model, or training-period change"). |
| **Input params** | `cacheKey` (+ optional `from`/`to`/`level`, `provenance` on the read path, `retrain: true` to force a refit). |
| **Expected result** | 200 `{"cached":true, …}` with the forecast window; unknown key or `retrain:true` → 200 `{"needTrain":true}`. |

**Positive — Compose:** probe with the key just fitted → 200 `cached=true`, `times=120`, `lower=120`.

**Negative — Compose:** probe with a never-used key (`ab…ab`) → 200 `{"needTrain":true}`; probe with
`"retrain":true` → 200 `{"needTrain":true}` (that is what the Retrain button turns into a fit).

**Kubernetes:** probe → 200 `cached=true`; unknown key → 200 `{"needTrain":true}`.

### F3. `GET /schedules` — list retrain rows with derived identity

| | |
| --- | --- |
| **Function** | The retrain queue for the caller's org: every row plus a derived `source` object identifying the panel that owns it. |
| **Who can use** | **Admin only.** Live: anonymous Admin → 200; a Grafana **Viewer** service-account token → 403 `forecast: admin required` (Compose and Kubernetes). |
| **How configured** | Needs a snapshot store (F20); returns 503 without one. Scheduling itself is driven by `retrainCron` (F16). |
| **Input params** | None. Rows are org-scoped: `panel` rows of the caller's org only (a foreign org's row is invisible), `baseline` rows are fleet-wide (`org_id = 0`). |
| **Expected result** | 200, JSON array of `{scope,key,cron,timezone,enabled,nextRunAt,lastRunAt,lastStatus,hasSpec,supersededAt,source}`. |

**Positive — Compose:** `curl -s $B/schedules` → 200 with 6 rows; the `panel` row's derived source was
`{"dashboardUid":"forecast-minute-week","panelId":1,"panelTitle":"Seasonal baseline (minute of week)","datasourceUid":"druid","seriesName":"value","querySummary":"Druid: minuteweek · minute","lookback":"21d"}`.

**Negative — Compose:** with a Viewer token, `GET $B/schedules` → 403 `forecast: admin required`.
An org-2 `panel` row inserted in Postgres (`org_id = 2`) did **not** appear in the org-1 response
(`org2row=false`) and stayed due while org-1 rows advanced.

**Kubernetes:** 200 as anonymous Admin; a K8s Viewer service account → 403 `forecast: admin required`; the
`Retrain schedules` tab lists 4 rows with the same derived `source` and `/d/forecast-minute-week?viewPanel=N`
links.

### F4. `PUT /schedules` — retime or enable one row

| | |
| --- | --- |
| **Function** | Change when a model retrains. It never changes *how*: the stored `spec` (queries, window, identity) is kept. |
| **Who can use** | **Admin only** (403 as Viewer). |
| **How configured** | Store required; the row's cron/timezone are what the scheduler reads. |
| **Input params** | Body `{scope, key, cron, timezone, enabled}`. `cron` is 5-field or `@hourly`/`@daily`; `timezone` is IANA. `enabled` is optional: omitting it keeps the stored value, so a retime never switches a row off. A `panel` key may be created this way; a `baseline` key must already exist. |
| **Expected result** | 200 with the stored row. 400 for a bad scope/cron/timezone/missing key; **404** for an unknown `baseline` key. |

**Positive — Compose:** `PUT` `{"scope":"baseline","key":"ready","cron":"*/2 * * * *","timezone":"UTC","enabled":true}`
→ 200 echoing `nextRunAt":"2026-09-21T10:56:00Z"`; restored to `*/5 * * * *` afterwards.

**Negative — Compose:** unknown baseline key → 404 `forecast: no such baseline schedule; the baselines worker creates those rows, so an admin may only edit an existing one`;
`"cron":"nope"` → 400 `forecast: invalid cron`; `"timezone":"Mars/Olympus"` → 400 `forecast: invalid timezone`;
`"scope":"nope"` → 400 `forecast: invalid scope`; empty key → 400 `forecast: scope and key required`.

**Kubernetes:** unknown baseline key → 404 with the identical literal; `"cron":"nope"` → 400 `forecast: invalid cron`.

### F5. `DELETE /schedules` — drop a row

| | |
| --- | --- |
| **Function** | Remove one row. A `panel` row is this org's; a `baseline` row is fleet-wide and is re-created by the worker on its next tick while the metric still reports. |
| **Who can use** | **Admin only** (403 as Viewer). |
| **How configured** | Store required. |
| **Input params** | Query `?scope=…&key=…`, both required. |
| **Expected result** | 200 `{"message":"ok"}`; 400 on missing/invalid parameters. |

**Positive — Compose:** `DELETE "$B/schedules?scope=panel&key=0f…0f"` → `{"message":"ok"}` (row gone);
`DELETE "$B/schedules?scope=baseline&key=ready"` → `{"message":"ok"}`, and on the following worker tick the row
was back (`ready`, cron `*/5 * * * *`, `last_status ok`) with the tick reporting `retrained=1` — the worker's
insert path, not a leftover.

**Negative — Compose:** `DELETE $B/schedules` → 400 `forecast: scope and key required`;
`?scope=nope&key=ready` → 400 `forecast: invalid scope`.

**Kubernetes:** 400 `forecast: scope and key required` for the parameterless call; the tab's `Delete` removes the
row immediately (no confirmation dialog) as observed on Compose.

### F6. `POST /schedules/default` — validate a default cron

| | |
| --- | --- |
| **Function** | Let the Configuration page prove a cron/timezone pair parses before writing it into jsonData. |
| **Who can use** | **Admin only**. |
| **How configured** | None; pure validation. |
| **Input params** | Body `{cron, timezone}`. |
| **Expected result** | 200 `{"cron":"<as sent>","timezone":"<normalized>"}`; 400 on a bad pair. |

**Positive — Compose:** `-d '{"cron":"*/5 * * * *","timezone":"Europe/Moscow"}'` → 200 `{"cron":"*/5 * * * *","timezone":"Europe/Moscow"}`.

**Negative — Compose:** `-d '{"cron":"not a cron"}'` → 400 `forecast: invalid cron`;
`-d '{"cron":"* * * * *","timezone":"Nowhere/Zone"}'` → 400 `forecast: invalid timezone`.

**Kubernetes:** 200 for the valid pair, 400 `forecast: invalid cron` for the bad one.

### F7. Forecast datasource `QueryData` — restore for alerting

| | |
| --- | --- |
| **Function** | Turn a stored snapshot back into a frame so Grafana alerting can query the forecast and its interval by `refId`. This is the **only** way to read a snapshot from another datum. |
| **Who can use** | **Alert rule** (Grafana alerting) or any datasource query. No Admin gate; the datasource's own jsonData carries the store (F20). |
| **How configured** | Datasource provisioned jsonData — `storeUrl` as one DSN, or field-wise `storeHost`/`storePort`/… (the datasource's config editor renders no store fields; F20) — or the merged `[plugin.eduardkolotushin-forecast-datasource]` ini section. Alerting `QueryData` also falls back to the parent app's `AppInstanceSettings`. Query editor is manual — no auto-fill; *Copy source from query A* is opt-in. |
| **Input params** | Query `{kind: "forecast"\|"lower"\|"upper", cacheKey, level?}` inside a normal `/api/ds/query` body with `from`/`to`. Each query's JSON is capped at 64 KiB (`maxQueryJSONBytes`); a larger one is rejected rather than decoded. |
| **Expected result** | 200 with one frame per query and real values; a miss is an error: `needTrain: train on the Forecast overlay panel first`; unknown `kind` → 400. |

**Positive — Kubernetes:** `POST /api/ds/query` with `{"kind":"forecast","cacheKey":"1a…1a","level":0.95}` →
200, `frames=1`, `values=60`; `{"kind":"upper"}` → 200, 60 values.

**Negative — Kubernetes:** a never-trained key → the frame carries `error: "needTrain: train on the Forecast overlay panel first"`;
`{"kind":"nope"}` → 400 `forecast: unknown query kind: nope`.

**Compose:** the same datasource answered its health check with `{"message":"ok","status":"OK"}`.

### F8. Forecast datasource `CheckHealth`

| | |
| --- | --- |
| **Function** | Tell Grafana whether the datasource can serve restores. |
| **Who can use** | Any user (Grafana's `Test` button, or `GET /api/datasources/uid/<uid>/health`). |
| **How configured** | Same store resolution as F7. |
| **Input params** | None. |
| **Expected result** | 200 `{"message":"ok","status":"OK"}`; without a store, 400 with `status: "ERROR"` and a message that names the missing store. |

**Positive — Compose and Kubernetes:** `curl -s $G/api/datasources/uid/forecast/health` → 200 `{"message":"ok","status":"OK"}`.

**Negative — Compose (throwaway Grafana, no DSN):** a `grafana/grafana:13.1.0` container on `:3001` with the
workspace `dist/` mounted and the Forecast datasource created with empty jsonData →
`GET /api/datasources/uid/forecast/health` → **400** `{"message":"snapshot store not configured (needTrain)","status":"ERROR"}`.

**Kubernetes:** 200 `{"message":"ok","status":"OK"}` (the datasource's ConfigMap carries the store).

### F9. Overlay panel — history + forecast + interval bands

| | |
| --- | --- |
| **Function** | Draw the queried series, the forecast window and (optionally) the band, or explain why it cannot. |
| **Who can use** | **Panel** (any dashboard user). |
| **How configured** | Panel options (F10); the panel's training query comes from the same datasource as the display query. |
| **Input params** | The dashboard time range, the panel's queries, and the option set. |
| **Expected result** | History plus a forecast segment past *now* and a shaded band; when it cannot fit, the panel shows `Forecast failed` and a reason instead of a line. |

**Positive — Compose:** `http://localhost:3000/d/forecast-minute-week` renders three panels
(`Seasonal baseline (minute of week)`, `… (hour of week)`, `… (minute of week, with variance)`), each with the
grey history, the orange forecast past *now*, the shaded band on panels 2–3, and the `Using saved model` +
`Retrain` header chips.

**Negative — Compose:** with the panel's *Training period* set to `2001-01-01 → 2001-01-02` (applied through the
dashboard JSON, the same field the editor writes) the panel renders `Forecast failed` / `Training query returned
no points`, keeps drawing history, and sends no fit (only the three probes with `points=0`).

**Kubernetes:** the same dashboard from the `overlay-dashboards` ConfigMap renders the same three panels with
history, forecast and bands, and no reason text.

### F10. Overlay panel options

| | |
| --- | --- |
| **Function** | Choose the model, the two windows, the band and the panel's load cap. |
| **Who can use** | **Panel** (any user who can edit the dashboard). |
| **How configured** | Panel options, stored in the dashboard JSON. Live labels: *Model*, *Forecast range*, *Alpha*, *Beta*, *Seasonal period*, *Seasonality*, *Calendar*, *Show prediction interval*, *Interval coverage*, *Training period*, *Legacy lookback*, *Max in-flight loads*, *Saved model*, *Retrain*, and the *Alerting* group (*New alert rule*). Keys: `model`, `forecastRange`, `alpha`, `beta`, `period`, `season`, `calendar`, `showInterval`, `interval`, `trainRange`, `lookback`, `maxInflightLoads` (source: `src/forecast-panel/module.ts`). |
| **Input params** | `forecastRange`/`trainRange` are `{from,to}` raw strings — empty means **Auto** (`now` → `now` + model duration; the last model window ending at the panel's `to`). `interval` 0 hides the band; `maxInflightLoads` minimum 1 (default 1). |
| **Expected result** | The panel redraws with the new option. |

**Positive — Compose:** `Interval coverage 0` → the panel's probe carried `level=0` and panel 1 drew **no** band
while panels 2 and 3 kept theirs; `Max in-flight loads 2` → all three panels still probed (`level=0.95`) and
rendered. Both options were applied through the dashboard JSON API and reverted afterwards (verified: panel 1's
options back to `{alpha:0.8,beta:0.2,calendar:"",horizon:180,model:"baseline",period:7,season:"minute-week"}`).

**Negative — Compose:** an inverted **Forecast range** (`2027-01-01 → 2026-01-01`) → `Forecast failed` /
`Forecast range is inverted or invalid`, **no** `/api/ds/query` and **no** `/forecast` request. An inverted
**Training period** reports the same way — `Forecast failed` / `Training period is inverted or invalid`, history
still drawn, no train query and no `/forecast` even after `Retrain` (it used to fall back to the Auto window and
train 30006 points; see [Discrepancies](#discrepancies-found)).

**Kubernetes:** the same dashboard and option set (the panels come from the ConfigMap); the K8s store shows the
resulting `panel` rows with `cron */5 * * * *`.

### F11. Configuration page — store deployment note and default retrain schedule

| | |
| --- | --- |
| **Function** | Show where the snapshot store is configured (deployment only) and set the org's default cron in jsonData, validating it before saving. |
| **Who can use** | **Admin** (Grafana's plugin configuration page, `role: Admin` in `plugin.json`). |
| **How configured** | Fields: *Snapshot store* — a read-only note naming the deployment keys; *Default retrain schedule* — Cron, Timezone. A save posts only `retrainCron`/`retrainTimezone` merged over the existing jsonData, and sends no `secureJsonData`, so neither a provisioned store value nor the store password can be overwritten from the page. |
| **Input params** | `retrainCron`, `retrainTimezone`. |
| **Expected result** | 200 and the alert `Settings not saved` + the reason when the cron does **not** parse; nothing is persisted in that case. |

**Positive — Compose:** `?page=configuration` renders the *Snapshot store* note (the env / `GF_PLUGIN_*` / ini /
jsonData key list) and the *Default retrain schedule* fields with the provisioned cron `*/5 * * * *`; no
Host/Port/Database/User/SSL mode/Password input exists anywhere on the page, and a `Save` persists only the retrain
keys (`jsonData.retrainTimezone: "UTC"` appeared after saving the valid cron while the provisioned store keys stayed
untouched).

**Negative — Compose:** typing `nope` into *Cron* and pressing Save → alert `Settings not saved` /
`forecast: invalid cron`, and the stored jsonData was **unchanged** (`retrainCron` still `*/5 * * * *`).

**Kubernetes:** the same page; the chart provisions the store through the `timeseries-forecast-app` ConfigMap and
the `timeseries-store-credentials` Secret (`apps.yaml` → `jsonData.storeHost: overlay-postgres.overlay-postgres.svc`,
`retrainCron: "*/5 * * * *"`, `secureJsonData.storePassword`) — the page lists those keys and writes only the retrain
schedule.

### F12. Retrain schedules tab — inspect, edit, delete

| | |
| --- | --- |
| **Function** | A page over the `forecast.retrain` table: what will retrain, when, how it last went, and what it belongs to. |
| **Who can use** | **Admin** (peer tab of Overview and Configuration on the plugin configuration page). |
| **How configured** | Store required (F20). Rows come from the overlay's fits (F2) and from the worker (F25). |
| **Input params** | Filters *Search* / *Scope* (All, panel, baseline) / *Enabled* (All, Enabled, Disabled) / *Status* (All, ok, error, never run); per-row cron, timezone and enabled editors; per-row *copy key*, *Save*, *Delete*; a *Refresh* button; 20-row client-side paging; a *Source* deep link for `panel` rows. |
| **Expected result** | Columns *Source, Scope, Key, Cron, Timezone, Next run, Last run, Status, Enabled* + actions; a row a newer key superseded shows a *Superseded* marker beside its status and is never retrained again; errors surface as `Schedules failed` + the backend reason. |

**Positive — Compose:** the tab listed 6 then 5 rows; the `panel` rows showed the panel title and
`value 21d Druid: minuteweek · minute` as their source with `/d/forecast-minute-week?viewPanel=N` links, and the
`baseline` row showed `—` as its source with a copy-key button. Editing my fixture row's cron to `*/3 * * * *`
and pressing *Save* → no alert and the server row read `*/3 * * * *` (the editor writes through F4).

**Negative — Compose:** the same editor with `nope` → alert `Schedules failed` / `forecast: invalid cron` and the
server row unchanged. Pressing *Delete* on the fixture row removed it immediately (`before=5 after=4`,
`rowGone=true`, no confirmation dialog).

**Kubernetes:** the tab lists the K8s store's 4 rows with the same columns, derived sources and deep links
(`ready` baseline + the three dashboard panels, all `last ok`).

### F13. Retrain action

| | |
| --- | --- |
| **Function** | Force one panel to refit now, instead of waiting for the cron. |
| **Who can use** | **Panel** (the header `Retrain` button or the panel option). |
| **How configured** | Always available; the button lives in the panel header next to the `Using saved model` chip. |
| **Input params** | None — the panel re-runs its own training query and posts a fit with `trainSource`. |
| **Expected result** | No probe on the wire: the panel runs the training query and posts one fit carrying points and `trainSource`; the chip stops claiming a saved model. |

**Positive — Compose:** pressing `Retrain` on panel 1 produced exactly two requests —
`POST /api/ds/query?…requestId=SQR100-train` with the panel's own training window
(`2026-08-31T13:56:39Z → now`, `interval: 1m`, `maxDataPoints: 30241`) and one
`POST /resources/forecast` with `times` (30011 points), `values`, `provenance`, `cacheKey` and
`trainSource {datasourceUid:"druid", queries:[…], from, to, relative:true, lookbackMs:1814400000}`.

**Negative — Compose:** pointing panel 1 at a Prometheus **instant** query (`expr:"up"`, `instant:true`) →
`Forecast failed` / `Prometheus instant queries cannot train a forecast`, no train query and no `/forecast`
(see [Discrepancies](#discrepancies-found) for the panel error this used to be).

**Kubernetes:** the same action against the Kubernetes Grafana at `http://localhost:80` (the panels are the same plugin build).

### F14. New alert rule from the panel options

| | |
| --- | --- |
| **Function** | Open Grafana's alerting editor pre-filled with this panel's live queries and identity. |
| **Who can use** | **Panel** (needs the dashboard saved and, in Grafana, permission to create rules). |
| **How configured** | Panel options → *Alerting* → *New alert rule* (navigates to `/alerting/new`; the plugin ships no rules). |
| **Input params** | None; the defaults carry the panel title, the panel's queries (including a Mixed Forecast row) and the dashboard/panel annotations. |
| **Expected result** | The browser lands on `/alerting/new?defaults=…` with a rule form; an unsaved dashboard answers `Dashboard must be saved before alerts can be added.` and does not navigate. |

**Positive — Compose:** clicking *New alert rule* navigated to
`http://localhost:3000/alerting/new?defaults=%7B%22type%22%3A%22grafana-alerting%22%2C%22name%22%3A%22Seasonal+baseline+%28minute+of+week%29%22%2C%22queries%22%3A%5B%7B%22refId%22%3A%22A%22…`
— i.e. name `Seasonal baseline (minute of week)`, `refId A`, `datasourceUid: druid`, `relativeTimeRange {from: 21600, to: -10800}`
and the panel's Druid target — and the editor rendered `1. Enter alert rule name` (prefilled) and
`2. Define query and alert condition` with that query.

**Negative — Compose:** not exercised live; the message is source-verified
(`src/forecast-panel/alertFromPanel.ts: 'Dashboard must be saved before alerts can be added.'`) and is emitted
when the panel has no dashboard UID.

**Kubernetes:** the same option on the same build; the rule editor opened on the Kubernetes Grafana (that check
ran through `make helm-grafana`'s port-forward, which serves the same pod).

### F15. Mixed overlay (metric + Forecast datasource)

| | |
| --- | --- |
| **Function** | Let one panel draw the metric *and* hold a Forecast datasource query for alerting, without letting the Forecast frames affect the fit or the cache key. |
| **Who can use** | **Panel**. |
| **How configured** | Panel datasource `-- Mixed --`, query A = the metric, query B = the Forecast datasource. No draw-mode option; the Forecast editor is filled by hand (*Copy source from query A* is optional and copies the source fingerprint only). |
| **Input params** | Any metric target plus a `forecast` target. |
| **Expected result** | One forecast series from the metric; the `cacheKey` is computed from the metric targets only; with no metric target nothing is fitted or drawn. |

**Positive — Compose:** panel 1 with `-- Mixed --`, target A (Druid) + target B (Forecast datasource,
`kind:"forecast"`) → `POST /resources/forecast panel=1 key=2e827911 pts=0` — the **same** `cacheKey` as the
A-only run (`2e8279112de4cb64cd546eb6ed52ccf29f752f89f019efcea4875459a004f55f`) — plus one `QueryData B`
request, i.e. the metric-only fingerprint while both queries run.

**Negative — Compose:** removing A and leaving only B → panel 1 showed its empty state (`No data`), sent no fit,
and the two `/forecast` requests on the wire belonged to the other panels.

**Kubernetes:** the same dashboard and the same plugin build; the K8s store's panel rows use the identical three
keys (`2e827911…`, `f8046f42…`, `7713db25…`), which is the cross-environment fingerprint check.

### F16. Unattended retrain of `panel` rows

| | |
| --- | --- |
| **Function** | Refit stored panel snapshots on their cron with no browser open: resolve the row's window, fetch frames from Grafana's own `/api/ds/query`, fit, store. |
| **Who can use** | **Operator** (it runs by itself once the app is configured). |
| **How configured** | jsonData `retrainCron` (default `0 3 * * *`; both environments use `*/5 * * * *`) and `grafanaUrl` (default `http://127.0.0.1:3000`); the token comes from `FORECAST_GRAFANA_TOKEN`, the ini section, or `secureJsonData.grafanaToken`. The ticker itself is `FORECAST_RETRAIN_ENABLED` (default `true`, the same precedence chain) with `FORECAST_RETRAIN_TICK` (default `30s`) and `FORECAST_RETRAIN_LEASE` (derived from the claim batch and the fetch timeout — 6m today). |
| **Input params** | Per row: `cron`, `timezone`, `enabled`; per spec: the stored queries and window. A window the picker expressed relatively — Auto, a legacy duration, or a Quick range such as `now-7d`/`now` — is stored as `relative: true` with `lookbackMs` and re-resolved at claim time, so a cron retrain follows the clock; a calendar/absolute pick, a window that does not end at `now`, and a rounded bound stay absolute and replay verbatim. |
| **Expected result** | `next_run_at` advances, `last_run_at`/`last_status` are written, `forecast.snapshots.updated_at` moves; a failure records `last_status = "error: …"` and never fails a user query. |

**Positive — Compose:** `$PG "SELECT scope,key,last_run_at,last_status,next_run_at FROM forecast.retrain ORDER BY next_run_at"`
showed the panel rows advancing on the `*/5` cron with `last_status ok` (`…10:55:24Z`, then `…11:00:32Z`), and
Grafana's log carried one `msg=retrain … status=ok dur=230ms` line per row per slot.
**Kubernetes:** the same cron from the chart ConfigMap — after the dashboard was loaded once, the K8s rows read
`next_run_at 11:15:00Z`, `last_status ok` and `forecast.snapshots.updated_at` moved with them.

**Negative — Compose:** pointing `jsonData.grafanaUrl` at a dead port is the documented way to break it; not
exercised live, because the same code path's failure recording is visible on the worker side (F26) and the
scheduler never fails a query. The `needTrain` flag is the user-visible half: when a row is due, the next probe
returns `{"needTrain":true}` (F2c), which is what makes the overlay refit on the next dashboard load.

### F17. Scheduler credentials

| | |
| --- | --- |
| **Function** | Authenticate the scheduler's own `/api/org` and `/api/ds/query` calls; disable itself instead of hammering Grafana with a bad credential. |
| **Who can use** | **Operator**. |
| **How configured** | Empty token (both test environments run anonymous Admin, so nothing is needed) or a Viewer service-account token via env/ini/`secureJsonData.grafanaToken`. |
| **Input params** | `FORECAST_GRAFANA_URL` / `FORECAST_GRAFANA_TOKEN`. |
| **Expected result** | With a working credential the ticks keep writing `last_status ok`; a credential Grafana rejects (401/403) is retried a bounded number of times and then the scheduler disables itself with a log line naming the env vars. |

**Positive — Compose and Kubernetes:** with an empty token against anonymous Admin the scheduler retrained on
every 5-minute slot (`last_status ok`, F16) — the token is genuinely optional in this configuration.

**Negative — Compose:** not exercised live (it needs three consecutive rejected fetches across cron ticks and a
due row each time); the behaviour and the literal
(`forecast: grafana refused the scheduler credentials (401/403)`) are source-verified
(`pkg/plugin/retrain.go`).

### F18. Org binding of queue rows

| | |
| --- | --- |
| **Function** | Keep a `cacheKey` (which is org-independent) from leaking between orgs: `org_id` is part of the row key, so a panel row belongs to one org and the scheduler claims only its own org's rows. |
| **Who can use** | **Operator** (implicit). |
| **How configured** | Nothing; the plugin context's `OrgID` is written into every row it creates. |
| **Input params** | — |
| **Expected result** | Rows of another org are neither listed nor retrained by this Grafana; they stay due for their own org. |

**Positive — Compose:** org-1 rows advanced on their cron while an org-2 `panel` row (`org_id = 2`) stayed due
and was absent from the org-1 listing (F3), and the worker — which only claims `scope='baseline'` — never touched
it.

**Negative — Compose:** deleting the org-2 row through the org-1 API is not possible (it is not listed); the
fixture had to be removed directly in Postgres, which *is* the boundary being described.

**Kubernetes:** one org in play; the same `org_id` column and the same PK `(scope, org_id, key)`.

### F19. Train-query rewrite per datasource type

| | |
| --- | --- |
| **Function** | Turn the panel's own query into a training query for its window and step, so the fit is trained on the same data the panel draws. |
| **Who can use** | **Panel** (any fit or retrain). |
| **How configured** | Automatic, by the target's datasource type (Prometheus range, OpenSearch metric+histogram / PPL time series, Postgres time-series SQL; Druid keeps the existing rewrite). Unsupported shapes get a reason instead of a fit. |
| **Input params** | The panel's targets plus the resolved training window (`trainRange` or Auto). |
| **Expected result** | One `/api/ds/query` with the training window and a step that matches the model, then a fit whose `trainSource` records the queries and window actually used. |

**Positive — Compose:** for the Druid builder target the train request was
`POST /api/ds/query?…requestId=SQR100-train` with `intervals:[2026-08-31T13:56:39Z/2026-09-21T13:56:39Z]`,
`interval: 1m`, `maxDataPoints: 30241`; for the Druid **SQL** panel (panel 3) the stored spec's
`querySummary` is `Druid SQL: SELECT __time, SUM("value") + 4.0 * SIN(…) …`, so the rewrite carries the SQL form
too.

**Negative — Compose:** the Prometheus-instant case is the negative this row names, and it **was** reproduced
(F13, and Discrepancy 3 below): an instant query is rejected with a reason, no training query runs and no fit is
posted. The OpenSearch and Postgres branches were not exercised in this sandbox — those two literals are
source-verified in `src/forecast-panel/reasons.ts`.

**Kubernetes:** the same Druid rewrite against the K8s Grafana (the `ready` baseline row and the three panel rows
all retrained to `ok`).

### F20. Snapshot store: DSN resolution and persist on/off

| | |
| --- | --- |
| **Function** | Decide where fitted snapshots live; with no DSN the plugin still fits and forecasts, it just cannot remember. |
| **Who can use** | **Operator** (deployment configuration; the store has no UI fields — F11). |
| **How configured** | In order: process env `FORECAST_STORE_URL`/`FORECAST_STORE_*` (Grafana 12.4+ does not forward host env by default), then `GF_PLUGIN_EDUARDKOLOTUSHIN_FORECAST_APP_*` / `…_DATASOURCE_*` / `GrafanaCfg` (`[plugin.eduardkolotushin-forecast-app]`, `[plugin.eduardkolotushin-forecast-datasource]`), then provisioned jsonData / `secureJsonData`. |
| **Input params** | `storeUrl` (one DSN, jsonData camel; env/ini spell it `FORECAST_STORE_URL` / `store_url`), or field-wise `storeHost`, `storePort`, `storeDatabase`, `storeUser`, `storeSslMode`, `storePassword`. A URL short-circuits the fields at the same level. |
| **Expected result** | With a store: snapshots in `forecast.snapshots (org_id, cache_key, snapshot jsonb, updated_at)` and schedules usable. Without: `/schedules` is 503 and every probe answers `needTrain`. |

**Positive — Compose:** the store comes from provisioning, not from a page: `provisioning/plugins/apps.yaml`
(app) and `provisioning/datasources/datasources.yml` (Forecast datasource) carry `storeHost: overlay-postgres`,
`storePort: 5432`, `storeDatabase: overlay`, `storeUser: overlay`, `storeSslMode: disable` and
`secureJsonData.storePassword`; a fit with `cacheKey 0f…0f` produced a row
(`org_id 1`, `pg_column_size(snapshot)=236`) and the panel row `<org 1> 0f0f…` with a `spec` holding
`{"to":…,"from":…,"model":"holt","season":"","panelId":1,"queries":[],"calendar":"","lookback":"21d","relative":true,"lookbackMs":1814400000,…}`
— the writer stores an absent or `null` query list as `[]` (F3), so a row the scheduler can read is always a row
it can fetch; the 2026-09-21 transcript's `"queries":null` is the pre-fix writer.

**Negative — Compose (throwaway Grafana without a DSN, as in F8):** `GET :3001/…/resources/schedules` → **503**
`forecast: snapshot store not configured`; the datasource health → 400
`snapshot store not configured (needTrain)`. Persist-off is not fit-off: `POST /forecast` with points but no
`cacheKey` → 200, and a probe with a `cacheKey` → 200 `{"needTrain":true}`.

**Kubernetes:** the app and the datasource each get the store from their own ConfigMap
(`timeseries-forecast-app`, `timeseries-forecast-datasource`) plus `timeseries-forecast-store` for the plugin
process env — which is why the alerting datasource can restore snapshots without the app.

## timeseries-baselines functions

The worker is one process with an env-only interface and no HTTP surface. `SHARD_ID` defaults to the container's
first non-loopback IP (the hostname only when there is none); `SHARD_MEMBERSHIP=store` makes the peer set the
`baselines.workers` heartbeat table (the Compose and K8s configurations both use it). "Owned" counts below are
from the live tick lines.

### F21. Tick loop

| | |
| --- | --- |
| **Function** | One pass per interval: scan hashes, decide ownership, publish, schedule and claim retrains. |
| **Who can use** | **Operator**. |
| **How configured** | `INTERVAL` (Compose/K8s `1m`), `LOG_LEVEL=debug` for the tick line. |
| **Input params** | Env only; the tick takes no arguments. |
| **Expected result** | One `msg=tick` line per interval with `shard peers owned skipped ineligible published retrained`. |

**Positive — Compose:** `docker logs --tail=4 timeseries-grafana-sandbox-baseline-worker-1` →
`time=… level=DEBUG msg=tick shard=172.19.0.16 peers=1 owned=2 skipped=0 ineligible=1 published=1 retrained=0`
once a minute (10:52:26Z, 10:53:26Z, 10:54:26Z). A throwaway worker with `INTERVAL=5s` ticked every ~5.0 s —
`INTERVAL` is the only driver of cadence.

**Negative — Compose:** there is no bad-value case beyond F31's validation; a container with an unparseable
`INTERVAL` exits at startup with a config error rather than ticking.

**Kubernetes:** the same tick line shape, `mode=store`, from the Deployment (`interval=1m0s` in the startup log).

### F22. Publish from a stored snapshot

| | |
| --- | --- |
| **Function** | Emit the Kafka message a consumer reads, using the stored fit rather than a fresh one. |
| **Who can use** | **Operator** (and whoever consumes the topic). |
| **How configured** | `KAFKA_BROKERS`, `KAFKA_TOPIC` (both `baselines`), `AHEAD_MINUTES` (30 in both environments), store DSN. |
| **Input params** | None per message: one message per published hash, at minute-truncated `now` + `AHEAD_MINUTES`. |
| **Expected result** | Key `"<metric_hash>|<epoch_ms>"`, value `{"metric_hash":…,"metric_ts":…,"baseline_value":<float>}`. |

**Positive — Compose:** `$KAFKA --topic baselines --max-messages 3 --property print.key=true` → key
`ready|1789990080000`, value `{"metric_hash":"ready","metric_ts":1789990080000,"baseline_value":83.5239}`;
`metric_ts` is the tick's minute (10:58) + 30 minutes.

**Negative — Compose:** a throwaway worker with `KAFKA_BROKERS=127.0.0.1:1` logged
`msg=metric … connection refused`, reported `published=0`, and kept running (`running=true`) until the timeout
killed it — a Kafka outage degrades publishing, it does not stop the process.

**Kubernetes:** the same topic and shape from the Deployment (the sandbox's `baselines` topic is shared by both
stacks only through their own brokers — each stack has its own Kafka).

### F23. Publish without a store (v1/v2 path)

| | |
| --- | --- |
| **Function** | Fit on the fly and publish, with no Postgres: the original single-process behaviour. |
| **Who can use** | **Operator**. |
| **How configured** | Omit every `BASELINE_STORE_*` variable. |
| **Input params** | Same as F22 plus `LOOKBACK` (336h). |
| **Expected result** | Druid `op=series` reads and `published>=1`; a hash below `LOOKBACK` is never published. |

**Positive — Compose:** a throwaway worker with no store and `LOG_LEVEL=debug` read series
(`op=series`) and published (`published=1`); with `LOOKBACK=8760h` the same worker reported
`ineligible=2 published=0`.

**Negative — Compose:** the same worker against an unreachable Druid (`DRUID_BROKER=http://127.0.0.1:9`) logged
retry attempts and then the failure, and the process survived.

**Kubernetes:** the Deployment always has the store (F25), so the no-store path was exercised on Compose only.

### F24. Eligibility gate (`span >= LOOKBACK`)

| | |
| --- | --- |
| **Function** | Refuse to train a hash whose history is shorter than `LOOKBACK` — on the scan path *and* on the retrain path. |
| **Who can use** | **Operator** (implicit; it is what keeps 3-day hashes out of the fleet). |
| **How configured** | `LOOKBACK` (336h), `SCAN_RANGE` (must be ≥ `LOOKBACK + 2*INTERVAL`), `DRUID_MAX_RANGE` (24h slicing). |
| **Input params** | None. |
| **Expected result** | The hash is counted under `ineligible` and gets no row; a claimed row that is below it is finished with `error: only <span> of history in the last <SCAN_RANGE>, want 336h0m0s`. |

**Positive — Compose:** `LOOKBACK=336h` with `DRUID_MAX_RANGE=24h` → exactly **14** `op=series` windows per
retrain (two claims in one tick → 28); with `DRUID_MAX_RANGE` unset the same read is **one** request
(`0 = unlimited`). The `short` hash (≈3 days) was counted under `ineligible` and had **no**
`forecast.retrain` row.

**Negative — Compose:** `LOOKBACK=8760h` made every hash ineligible (`ineligible=2 published=0`); a forced-claim
row for a hash below the gate finished as `error: …`.

**Kubernetes:** `LOOKBACK=336h`, `DRUID_MAX_RANGE=24h` from the ConfigMap; the Deployment's only eligible hash
(`ready`) trained (F27).

### F25. Schedule owned hashes into `forecast.retrain`

| | |
| --- | --- |
| **Function** | Make sure every owned, eligible hash has a row, so the retrain queue covers the fleet without an operator. |
| **Who can use** | **Operator**. |
| **How configured** | `DEFAULT_RETRAIN_CRON` (`*/5 * * * *`), store DSN. |
| **Input params** | None; the insert is keyed `(scope='baseline', org_id=0, key=metric_hash)`. |
| **Expected result** | One row per owned eligible hash; an existing row keeps its cron and timezone. |

**Positive — Compose:** `$PG "SELECT … FROM forecast.retrain WHERE scope='baseline'"` → `ready` with cron
`*/5 * * * *`, timezone `UTC`, enabled; editing that cron to `*/2 * * * *` and letting a tick pass kept the edit
(the worker only inserts missing rows). Deleting the row made the next tick re-insert it (F5).

**Negative — Compose:** the ineligible `short` hash had **no** row (`count = 0`).

**Kubernetes:** the same rows for the K8s eligible hash; the worker's first ticks logged
`ERROR msg=schedule hashes=1 err="ERROR: relation \"forecast.retrain\" does not exist (SQLSTATE 42P01)"` until
the plugin had created the table — the documented ownership boundary, not a crash.

### F26. Claim due `baseline` rows fleet-wide

| | |
| --- | --- |
| **Function** | Lease due rows so any number of workers can share the queue without a coordinator. |
| **Who can use** | **Operator**. |
| **How configured** | `TRAIN_CONCURRENCY` (2), retry interval (lease), `SHARD_MEMBERSHIP=store`. |
| **Input params** | None; the claim takes the oldest due rows with `FOR UPDATE SKIP LOCKED` and stamps `claimed_by`/`claimed_until`. |
| **Expected result** | `last_status`/`last_run_at`/`next_run_at` move on success; a failure records `error: …` and is retried sooner than the cron. |

**Positive — Compose:** `$PG "UPDATE forecast.retrain SET next_run_at=now() WHERE scope='baseline' AND key='ready'"`
then one tick → `last_status ok`, `last_run_at` set, `next_run_at` at the next cron slot,
`baselines.snapshots.trained_at` moved 10:55:25 → 10:59:25 and the tick reported `published=1 retrained=1`.

**Negative — Compose:** forcing the synthetic `ghost-hash` row due → the tick logged
`level=ERROR msg=retrain metric_hash=ghost-hash err="no data in the last 336h0m0s"` and the row's `last_status`
became `error: no data in the last 336h0m0s` with `next_run_at = now + 5m` (the retry interval, not the cron).

**Kubernetes:** the same claim/finish cycle against the K8s Postgres (`ready` → `ok`, `11:11` then `11:15`).

### F27. Fit and persist a minute-of-week baseline

| | |
| --- | --- |
| **Function** | Train the model the worker publishes and keep it as a gzip snapshot. |
| **Who can use** | **Operator**. |
| **How configured** | `LOOKBACK` (336h), `DRUID_DATASOURCE` (`metrics`), store DSN; the fit is linear and pre-sized. |
| **Input params** | The hash and the window; the model is fixed (`baseline`/`minute-week`). |
| **Expected result** | One `baselines.snapshots` row per retrained hash with `model=baseline`, `season=minute-week`, `lookback_ms=1209600000` and a gzip payload. |

**Positive — Compose:** `$PG "SELECT metric_hash, model, season, calendar, lookback_ms, trained_at, length(snapshot), encode(substring(snapshot from 1 for 2),'hex') FROM baselines.snapshots"`
→ `ready|baseline|minute-week||1209600000|2026-09-21 11:05:28.025158+00|17414|1f8b` (gzip magic, ~17 KB).

**Negative — Compose:** a claimed hash whose Druid window holds no data finishes as
`error: no data in the last 336h0m0s` and writes no snapshot.

**Kubernetes:** `SELECT metric_hash, model, season, lookback_ms, trained_at FROM baselines.snapshots` →
`ready|baseline|minute-week|1209600000|2026-09-21 11:11:31.004464+00` — the Deployment trained and persisted
through the chart-provisioned store.

### F28. Membership heartbeat and peer source

| | |
| --- | --- |
| **Function** | Keep the fleet's peer set current, so rendezvous ownership reflects the workers that are actually alive. |
| **Who can use** | **Operator**. |
| **How configured** | `SHARD_MEMBERSHIP=store` + store DSN (or `SHARD_DNS` + `SHARD_PEERS`). |
| **Input params** | Nothing; the worker writes `baselines.workers (id, last_seen, owned, peers)` each tick. |
| **Expected result** | One row per live worker with a fresh `last_seen`; a stopped worker's row ages out and its share is taken over. |

**Positive — Compose:** `$PG "SELECT id,last_seen,owned,peers FROM baselines.workers"` → the live worker with
`owned`/`peers` populated and `last_seen` inside the last tick; the Kubernetes store showed the same for
`10.244.0.29` (`owned=2 peers=1`).

**Negative — Compose:** `docker compose stop baseline-worker` froze its `last_seen` (11:01:25) while it stayed
stopped for ~95 s, and `docker compose start` refreshed it (11:05:28).

**Kubernetes:** the peer source is the headless Service DNS (`shardDNS=timeseries-baselines-headless`), the
membership is still the store table.

### F29. Ownership by rendezvous hashing

| | |
| --- | --- |
| **Function** | Split the hash set across workers without a coordinator: a hash belongs to exactly one live worker. |
| **Who can use** | **Operator** (scale a Deployment or start more containers). |
| **How configured** | Same membership source on every replica; `SHARD_ID` per replica. |
| **Input params** | Replica count. |
| **Expected result** | `owned` counts are disjoint and sum to the eligible set; every eligible hash is published by exactly one worker. |

**Positive — Compose:** `--scale baseline-worker=2` → two `baselines.workers` rows, both reporting `peers=2`,
`owned 0+2` over the same two hashes, and the single eligible hash published by exactly one of them
(worker-2 `published=1`, worker-1 `published=0`).

**Negative — Compose:** scaling back to 1 did **not** hand the share over instantly: the survivor kept
`peers=2` until the removed peer's row aged out (effective TTL `max(defaultWorkerTTL, 2*INTERVAL)` = 120 s), then
reported `peers=1 owned=2 published=1` on the first tick after that.

**Kubernetes:** `kubectl -n timeseries scale deploy/timeseries-baselines --replicas=2` → both pods `1/1`, two
heartbeat rows (`10.244.0.29` `owned=0`, `10.244.0.32` `owned=2`), each tick line `peers=2`; scaled back to 1
and the survivor kept running.

### F30. Bounded Druid access

| | |
| --- | --- |
| **Function** | Read Druid without ever issuing an unbounded query: window slicing, a request-rate cap, an inflight cap, retries and a reply-size cap. |
| **Who can use** | **Operator**. |
| **How configured** | `DRUID_MAX_RANGE` (24h), `DRUID_MAX_RPS` (4), `DRUID_MAX_INFLIGHT`, `DRUID_TIMEOUT`, `DRUID_RETRIES`; one reply is capped at 64 MiB. |
| **Input params** | Env only. |
| **Expected result** | A 336h read at a 24h cap becomes 14 consecutive requests, spaced by the rate cap; `0` disables slicing. |

**Positive — Compose:** the debug log's request timestamps for one retrain were ≥ 1 s apart with
`DRUID_MAX_RPS=1` (hash reads 0.993–1.014 s, series reads 0.984 s), and the same code made one request per query
when `DRUID_MAX_RANGE` was unset.

**Negative — Compose:** `DRUID_MAX_RPS=1` with `LOOKBACK=336h` visibly slowed the retrain (the requests
serialise at ~1/s) instead of failing; an unreachable broker produced retries and then the recorded error
(F24/F26's negative).

**Kubernetes:** the ConfigMap carries the same `DRUID_MAX_RANGE=24h` / `DRUID_MAX_RPS=4`.

### F31. Configuration from env: parse and validate

| | |
| --- | --- |
| **Function** | Fail fast and loudly on a bad deployment instead of half-working. |
| **Who can use** | **Operator**. |
| **How configured** | Every knob is an env var: `DRUID_BROKER`, `DRUID_DATASOURCE`, `DRUID_MAX_RANGE`, `DRUID_MAX_RPS`, `DRUID_MAX_INFLIGHT`, `DRUID_TIMEOUT`, `DRUID_RETRIES`, `DRUID_AUTH_HEADER`, `DRUID_AUTH_VALUE`, `KAFKA_BROKERS`, `KAFKA_TOPIC`, `LOOKBACK`, `SCAN_RANGE`, `SNAPSHOT_CACHE_TTL`, `AHEAD_MINUTES`, `INTERVAL`, `TRAIN_CONCURRENCY`, `HASH_SCAN_TTL`, `WORKER_TTL`, `CALENDAR`, `DEFAULT_RETRAIN_CRON`, `RETRAIN_RETRY`, `SHARD_ID`, `SHARD_PEERS`, `SHARD_DNS`, `SHARD_MEMBERSHIP`, `BASELINE_STORE_*`, `LOG_LEVEL`. |
| **Input params** | Env only; no flags and no config file. |
| **Expected result** | Exit code 1 with one `ERROR config err="…"` line naming the knob. |

**Positive — Compose:** the running worker's env is the compose file's (`DRUID_BROKER=http://druid-broker:8082`, `SHARD_MEMBERSHIP=store`, `LOOKBACK=336h`, `AHEAD_MINUTES=30`, `INTERVAL=1m`, …) and it started cleanly;
the Kubernetes ConfigMap `timeseries-baselines-env` renders the same keys against in-cluster DNS.

**Negative — Compose:** throwaway containers exited 1 with, respectively,
`DRUID_BROKER is required`, `DRUID_BROKER must be an absolute URL`,
`SHARD_MEMBERSHIP=store requires BASELINE_STORE_HOST or BASELINE_STORE_URL`,
a `DEFAULT_RETRAIN_CRON` error, `SCAN_RANGE must be at least LOOKBACK + 2*INTERVAL`, and an `AHEAD_MINUTES` parse error.

**Kubernetes:** `kubectl -n timeseries run … --image=…timeseries-baselines:0.1.0 --env=SHARD_MEMBERSHIP=store`
(no store) → pod `terminated (Error)` with
`ERROR config err="SHARD_MEMBERSHIP=store requires BASELINE_STORE_HOST or BASELINE_STORE_URL"`; without
`DRUID_BROKER` → `ERROR config err="DRUID_BROKER is required"` and `terminated (Error)`.

### F32. No HTTP surface and graceful shutdown

| | |
| --- | --- |
| **Function** | Be a pure worker: nothing listens, nothing is exposed, and a stop is clean. |
| **Who can use** | **Operator** (a probe would be the caller — there is none). |
| **How configured** | Nothing to configure; the binary opens no listener. |
| **Input params** | — |
| **Expected result** | No published port, no listener in the network namespace, exit code 0 on `stop`. |

**Positive — Compose:** `docker compose config` reports no `ports`/`expose` for the worker; inside the network,
`nc -z -w2 <worker-ip> 8080` returned rc=1 (nothing listening) against a control host where it returned rc=0;
`docker compose stop baseline-worker` exited 0.

**Negative — Compose:** the same probe against the Grafana container succeeds, which is what makes the empty
result for the worker meaningful.

**Kubernetes:** the Deployment declares no container port and the chart ships no probes (the sandbox's K8s path
inherits that).

## The contract between the two

The two processes meet at exactly two places, both in the overlay Postgres:

- **`forecast.snapshots (org_id, cache_key, snapshot, updated_at)`** — the fitted state, keyed by the 64-hex
  `cacheKey` the panel computes. Observed live: `pg_column_size(snapshot)` `236` (a two-point API fit) up to
  `447908` (a panel's minute-of-week fit); the column is `jsonb`, and the panel's `trainSource`/`provenance`
  fields are **not** part of the fingerprint — a Mixed panel held the same key as the metric-only run.
- **`forecast.retrain (scope, org_id, key, cron, timezone, enabled, spec, next_run_at, last_run_at, last_status, claimed_by, claimed_until, superseded_at, updated_at)`**,
  primary key `(scope, org_id, key)` — the queue. `panel` rows are written by the plugin (with `spec` holding the
  queries, window and identity), `baseline` rows by the worker (with no `spec`), and each side claims only what it
  owns: the plugin its own org's `panel` rows, the worker the fleet-wide (`org_id = 0`) `baseline` rows.
  `superseded_at` is the plugin's retire marker (see [Scaling and HA](#scaling-and-ha) and F46): the worker reads
  it in its own claim predicate, so a superseded row is claimed by neither side.
- The worker also owns `baselines.snapshots` (gzip `forecast.Snapshot` + `trained_at`) and
  `baselines.workers` (heartbeat). The worker creates only schema `baselines`; `forecast.retrain` is created and
  owned by the plugin, which is why the K8s worker logged `relation "forecast.retrain" does not exist` until the
  plugin's store path had run once.

## Scaling and HA

`gpx_forecast` is a Grafana **backend plugin**, not a service: Grafana spawns the binary from its plugin
directory and dials the gRPC address the child announces in the plugin handshake (`pkg/main.go` serves through
`datasource.Manage` and `app.Manage`). There is no remote or externally hosted mode — the SDK's standalone
support (`internal/standalone`, `standalone.txt`) exists so an IDE can debug a plugin against a local plugin
directory, and even that needs Grafana's plugins dir. So the backend runs **where Grafana runs**: one
`gpx_forecast` process per plugin (overlay app, Forecast datasource) **per Grafana replica**, and the only
scaling axis is more Grafana replicas over one Postgres. That is safe because the *queue* coordinates, not the
processes: [`FOR UPDATE SKIP LOCKED` is what makes Grafana HA safe](ARCHITECTURE.md) — one plugin process per
replica, all ticking, each due row retrained by exactly one of them, no leader and no lock table.

Observed live (Kubernetes, 2026-09-21, 14:16–14:31 UTC) with a temporary second release of the same chart: the
`timeseries` release's single Grafana pod plus two `fx-ha` replicas — **three scheduler processes**, all ticking
every 30 s against the one overlay Postgres, all inheriting `FORECAST_STORE_*` and the `*/5 * * * *` cron.

| Mechanism | Where | Observation |
| --- | --- | --- |
| **Claim** — `scope='panel' AND org_id=$org AND enabled AND spec IS NOT NULL AND jsonb_typeof(spec->'queries')='array' AND spec->'queries' <> '[]'::jsonb AND superseded_at IS NULL AND next_run_at <= now() AND (claimed_until IS NULL OR claimed_until < now())`, then `LIMIT <batch> FOR UPDATE SKIP LOCKED` | `pkg/plugin/schedule.go` (`panelClaimSQL`) | Three rows were due at 14:20:00, 14:25:00 and 14:30:00 and still untouched at 14:20:12, 14:20:27 and 14:25:15; each slot produced **exactly three retrains**, all from one replica (`fx-ha-grafana-687dcc9476-7svct`): 14:20:28.499/.691/.960, 14:25:28.480/.700/.951, 14:30:28.408/.594/.846. Per-pod `status=ok` counts over 14:19–14:31: `7svct` **10**, `9hrmh` 0, `timeseries` 0 (nine slot retrains plus the lease one below) — a double claim would read 6 per slot, a stalled claim 0. The `queries` and `superseded_at` predicates were added by the remediation (F3, F46) and were re-verified on Compose in pass 3: a row with `queries:[]` and a row with `spec IS NULL` both stayed overdue and unclaimed across the scheduler's 12:49:49 slot while a sibling row was claimed and refit, and a re-enabled but superseded row was left alone too |
| **Lease** — `claimed_by=$owner, claimed_until=now()+$lease` (default `FORECAST_RETRAIN_LEASE`: 5 m when this run was taken; **derived** since the remediation as `retrainClaimBatch × frameFetchTimeout + frameFetchTimeout + 1 min` = 6 m, `pkg/plugin/retrain.go`), and a held lease is excluded from every later claim | same statement | A row forced to `claimed_by='ghost-holder', claimed_until=now()+100s, next_run_at=now()` (due from 14:26:05, lease until 14:27:45) stayed untouched across the nine ticks of the three processes in that window (30 s apart, 14:26:28–14:27:28) and was retrained **exactly once**, 13 s after the lease expired: 14:27:58.409, `dur=235ms` |
| **Owner-guarded finish** — `… WHERE … AND claimed_by=$owner`, then `next_run_at = nextRun(cron, timezone, now)` | `pkg/plugin/schedule.go` (`Finish`) | Every retrain cleared the claim (`claimed_by`/`claimed_until` back to `NULL`), left `last_status=ok` and pushed `next_run_at` to the next cron slot (14:20:28 → 14:25:00, 14:25:28 → 14:30:00, 14:30:28 → 14:35:00); `forecast.snapshots.updated_at` moved with each one (14:20:28.47/.69/.93, 14:25:28.45/.69/.92, 14:30:28.39/.59/.82) |
| **Snapshot upsert** — `ON CONFLICT (org_id, cache_key) DO UPDATE` | `pkg/plugin/store_postgres.go` | Two replicas may fit the same key without error; each retrain stored exactly one snapshot per row |
| **Org-bound claim** — the credential's own org once per process (`GET /api/org`), `org_id = $org` in the predicate | `pkg/plugin/schedule.go`, `pkg/plugin/retrain.go` | All three processes were anonymous-Admin org 1 and competed for the same rows (the other-org case is F18) |
| **Per-process read-through cache** (256 entries, 30 s TTL) | [ARCHITECTURE.md](ARCHITECTURE.md) (store section) | Cross-replica *visibility* observed: 45 probes sent through the **`timeseries` release's** process (whose own scheduler claimed nothing) walked the whole cycle while a replica retrained — `{"needTrain":true}` for the 18 probes that landed while the ghost lease held (14:26:05–14:27:54) and for the 5 that landed in the 14:30:00 slot, then the restored forecast within 6 s of the retrain that finished at 14:27:58.409, and `"cached":true` on a probe 72 s after the 14:20:28 retrain. The 30 s **staleness** window itself was not timed (no warm entry across another replica's retrain) |

The recipe is the sandbox's own values plus these overrides (replicas share Grafana's database, not a PVC):

```bash
helm upgrade --install fx-ha ../timeseries-k8s/charts/timeseries -n timeseries -f helm/timeseries-values.yaml \
  --set grafana.replicas=2 \
  --set grafana.persistence.enabled=false \
  --set grafana.env.GF_DATABASE_TYPE=postgres \
  --set grafana.env.GF_DATABASE_HOST=overlay-postgres.overlay-postgres.svc:5432 \
  --set grafana.env.GF_DATABASE_NAME=grafana_ha \
  --set grafana.env.GF_DATABASE_USER=overlay \
  --set grafana.env.GF_DATABASE_PASSWORD=overlay \
  --set grafana.env.GF_DATABASE_SSL_MODE=disable \
  --set baselines.enabled=false
```

- **Shared Grafana state instead of a PVC.** The chart's default install keeps Grafana's own state in SQLite on
  an RWO claim, which two pods cannot mount, so the replica set points Grafana at Postgres and drops
  persistence. Both replicas then provision the same dashboards and datasources into that database (observed in
  `resource` and `data_source` of `grafana_ha`) and write the same `forecast.*` tables through
  `FORECAST_STORE_*`.
- **Configure the database by env, not through `grafana.ini`.** This chart keys the ini as the literal dotted
  key `grafana.ini`, so `--set grafana.ini.database.type=postgres` builds a nested map the chart never reads: the
  replicas first came up on SQLite, with no `[database]` section in the rendered ini. `GF_DATABASE_*` is what
  Grafana reads — the pods logged `Config overridden from Environment variable var="GF_DATABASE_TYPE=postgres"`
  and `Connecting to DB dbtype=postgres`, and the fixture dashboard appeared in `grafana_ha`. A values file has
  to use the same literal key (as `helm/timeseries-values.yaml` does); a YAML `grafana.ini:` key and
  `--set grafana.ini.…` are different maps.
- **No ingress needed.** The scheduler is internal: the second release's LoadBalancer stayed `<pending>` and its
  replicas only had to reach Grafana and the datasources in-cluster. Each replica's scheduler fetches frames from
  `FORECAST_GRAFANA_URL` — the sandbox's `http://127.0.0.1:3000`, i.e. **its own** Grafana (observed: `…-7svct`
  logged `dsUid=druid … queryData … status=ok` for the retrain it had claimed).
- **`baselines.enabled=false`** keeps the run from starting a second `timeseries-baselines` fleet; the two claim
  disjoint scopes anyway (`baseline` vs this org's `panel`).

What it does not change:

- The three load caps (`MAX_TRAIN_POINTS`, `MAX_INFLIGHT`, the 16 MiB body cap) are **per process**, so N
  replicas mean N× the fleet-wide fit concurrency: the queue bounds who retrains, not how much compute exists.
- Every replica ticks and any of them may win — the `timeseries` process lost all three observed slots only
  because a replica's tick came first each time (the rows were still due 27 s into the slot); nothing is pinned
  to the oldest pod.
- A replica's retrain goroutine starts with its **app plugin instance** (`pkg/plugin/app.go`, `newApp`), not
  with the pod: here both replicas were up at 14:15, served their app at 14:16:28 and claimed their first slot at
  14:20:28.
- A process that dies mid-retrain releases nothing: its claim expires with the lease and the next tick after that
  retries the row — the ghost row above is that case seen from the other side.
- Adding a replica needs no plugin configuration, no leader and no lock table; the claim predicate plus the lease
  is the whole protocol.

## Verified live, not verified live

Everything in the scenario lines above was observed on a running system. The following claims come from the
source and are **not** backed by a live observation in this run:

| Claim | Where it lives | Why it was not observed |
| --- | --- | --- |
| The OpenSearch and Postgres train rejections, and the Druid/Postgres/OpenSearch rewrite branches beyond the Druid builder and Druid SQL | `src/forecast-panel/reasons.ts`, `src/forecast-panel/trainRewrite.ts` | No OpenSearch or Postgres panel exists in the sandbox dashboards and no non-Druid target either; the Prometheus branch is live (F13). |
| `Dashboard must be saved before alerts can be added.` | `src/forecast-panel/alertFromPanel.ts:4` | The positive (`/alerting/new` with defaults) was verified; the unsaved-dashboard case needs a brand-new unsaved dashboard with the panel, which was not built. |
| Scheduler auto-disable after repeated 401/403 | `pkg/plugin/retrain.go` (`errGrafanaUnauthorized`) | Needs three consecutive rejected fetches across cron ticks; the Compose sandbox uses anonymous Admin and the token path was only exercised in its working (empty-token) form. |
| `Copy source from query A` (Forecast editor) | `src/forecast-datasource/` | Opt-in, manual feature; the Forecast editor was not driven by hand. |
| `DRUID_MAX_INFLIGHT` saturation | `timeseries-baselines/druid.go`, `timeseries-baselines/limits.go` | `DRUID_MAX_RPS` was verified; the inflight cap was not driven to saturation. |
| `FORECAST_MAX_INFLIGHT` env/ini precedence | `pkg/plugin/limits.go` | Only the jsonData path was used (the Compose env does not set it). |
| The CI/CD `forecast.ini.template` merge | `conf/forecast.ini.template` | Neither test environment merges the ini; both configure through jsonData/ConfigMaps. |
| A **stale** read-through cache entry served for up to 30 s after another replica retrains | `docs/ARCHITECTURE.md` (store section), `pkg/plugin/store_postgres.go` | Cross-replica visibility *was* observed (the [Scaling and HA](#scaling-and-ha) probe sequence), but every probe hit a process with no warm entry for that key, so the staleness window itself was never timed; timing it needs one process to cache a snapshot and another to retrain that key inside 30 s. |

## Discrepancies found

Live results that contradicted what the code or the docs led one to expect. All three defects are fixed, and each
post-fix observation is recorded beside its pre-fix one; item 4 lists the run's side effects with their verdicts.

### 1. The 16 MiB body cap could never answer 413 through Grafana — fixed

`pkg/plugin/limits.go` caps the request body at `defaultMaxForecastBody = 16 << 20` and maps `errBodyTooLarge` to
413, but the plugin SDK's default receive limit was the **same number**:
`grafana-plugin-sdk-go@v0.296.1/backend/serve.go:32 defaultServerMaxReceiveMessageSize = 1024 * 1024 * 16`.
A body at the cap already exceeded the *gRPC message* limit once framing was added, so the SDK rejected the call
before the handler's `MaxBytesReader` could run.

The fix serves both plugin processes with `GRPCSettings{MaxReceiveMsgSize: cap + cap}` (`pkg/plugin/limits.go`,
`pkg/main.go`), so the plugin's own cap is the limit a caller meets: an oversize body now reaches the handler and
only a message above the 32 MiB transport ceiling is refused by the SDK. The cap itself stays 16 MiB, and the
sweep below is what pins the boundary: `TestForecastCapBoundaries` covers the handler side (a body at the
pre-flight budget decodes, one byte over it is refused with the pre-flight's own reason) and the 33 MiB row the
transport's.

```bash
# body = JSON with a junk field of N MiB, times/values empty
for mb in 15 16 16.5 17 20; do … curl --data-binary @body-$mb.json … ; done
```

| Body | Before | After |
| --- | --- | --- |
| 15 MiB | 400 `forecast: series is empty` | **413** `forecast: request body too large for a legal training series` — pass 3 re-measured a 14.23 MB body, which is above the 6,400,000-byte pre-flight budget |
| 16 MiB | 500 `{"statusCode":500,"messageId":"plugin.requestFailureError",…}` | **413** (the pre-flight message: the body cap is `>` 16 MiB, so 16 MiB itself reaches the decoder's guard) |
| 16.5 MiB, 17 MiB, 20 MiB | 500 | **413** `forecast: request body too large` (pass 3 re-measured 17.31 MB) |
| 33 MiB and above | 500 | 500 — above the 32 MiB transport ceiling, refused by the SDK and not by the plugin; the reason is explicit: `grpc: received message larger than max (37151257 vs. 33554432)` (pass 3, a 37.15 MB body — the process stayed up, `restarts=0 oom=false`) |

The same sweep on the Kubernetes release over `http://localhost:80` answers 413 for 16, 16.5, 17 and 20 MiB.

### 2. An inverted *Training period* silently trained the Auto window — fixed

The panel reports `Forecast range is inverted or invalid` for an inverted **Forecast range** (observed, no
requests sent), but an inverted **Training period** was treated as unusable and fell back to Auto:

```bash
# dashboard JSON: panels[0].options.trainRange = {from: "2026-09-17T00:00:00Z", to: "2001-01-01T00:00:00Z"}
# then load /d/forecast-minute-week and read the wire:
#   TRAIN window 2026-08-31T14:01:12Z -> 2026-09-21T14:01:12Z
#   FIT panel=1 points=30006 trainSource={"relative":true,"lookbackMs":1814400000}
```

`resolveTrainWindow` returned the Auto window whenever `parseTimeRange` could not parse the pair, so one of two
pickers that look alike was validated and the other was not.

The fix marks a non-empty picker it cannot parse as invalid (an empty picker still means Auto, an absolute
window is still a window) and the panel shows the reason. With the same inverted picker applied through the
dashboard API, the wire after the fix is:

- panel 1: `Forecast failed` / `Training period is inverted or invalid`, history still drawn;
- the load sent only the three display `/api/ds/query` requests — no `…-train` query and no `/forecast`;
- pressing `Retrain` sent **nothing** (0 requests).

The Kubernetes release, rebuilt from the fix commit, reports the same reason and likewise sends nothing on
`Retrain` (checked on a throwaway copy: the provisioned dashboards there are read-only).

### 3. The Prometheus-instant rejection was unreachable, and the panel threw instead — fixed

With panel 1's datasource and target replaced by a Prometheus instant query (`expr:"up"`, `instant:true`,
`range:false`), the panel emitted **no** reason text (`/cannot train|Prometheus|failed/` matched nothing) even
though `Retrain` was pressed. The literal `Prometheus instant queries cannot train a forecast` exists in
`src/forecast-panel/reasons.ts` and the guard in `trainRewrite.ts`, but two defects kept it off the screen:

- the overlay only asked the rewrite while it had a series to attribute a fit to, and rendered Grafana's empty
  state on top of any reason it did resolve;
- with the dashboard's configured future range (`to=now+3h`) the whole panel threw
  `TypeError: Cannot read properties of undefined (reading 'name')`: a Prometheus instant query evaluated past
  its data returns an **empty frame**, and the panel passed that frame to Grafana's `TimeSeries` — an A/B against
  the pre-fix build reproduces it, and Grafana's own time series panel answers the same frame with `No data`.

The fix computes the per-group rewrite rejection without resolving a datasource or running a query
(`trainQuery.trainRejectReason`), checks it — and the invalid picker — before the panel loads anything, and plots
only frames that have something to draw.

| Panel 1 = Prometheus instant | Before | After |
| --- | --- | --- |
| dashboard range `to=now+3h` (the provisioned dashboard) | `An unexpected error happened` | `Forecast failed` / `Prometheus instant queries cannot train a forecast` over Grafana's `No data` |
| dashboard range `to=now` | reason shown (reachable by accident, via the probe) | reason shown, the one series drawn |
| `/forecast` and `…-train` requests from panel 1 | none (the panel threw first) | none (the target can never train) |

The Kubernetes release, rebuilt from the fix commit, answers both ranges the same way: the reason over Grafana's
`No data` at `to=now+3h`, the reason plus the series at `to=now`, with no `…-train` query and no `/forecast` from
that panel (again on a throwaway copy of the read-only provisioned dashboard).

The untouched dashboard still renders after the fix: three panels with history, forecast and bands, and the
`forecast.retrain` rows keep `last_status ok` on the `*/5` cron.

### 4. Small side effects of the run — verdicts

- The Compose app jsonData gained `retrainTimezone: "UTC"` when the Configuration page saved the valid cron
  (F11). Intended, not a defect: the page shows `UTC` when jsonData carries no timezone
  (`AppConfig.tsx`: `useState(json.retrainTimezone ?? 'UTC')`) and Save writes the whole form — the value the
  scheduler already used.
- The Kubernetes worker logs `ERROR msg=schedule … relation "forecast.retrain" does not exist` on its first ticks
  until the plugin has created the table: `timeseries-baselines/AGENTS.md` gives that table to the plugin, so the
  line is a deployment note rather than a defect.
- The K8s LoadBalancer's `EXTERNAL-IP` (`172.18.0.5`) is a cluster-internal address, but the service is
  reachable from the Docker Desktop host at `http://localhost:80`: Docker Desktop's kind cloud provider runs
  `envoyproxy/envoy:v1.36.7` (`kindccm-…`, `0.0.0.0:80->80/tcp`) in front of it. The overview run reached Grafana
  through `kubectl port-forward svc/timeseries-grafana 30001:80` instead — the same pod, and the 413 sweep plus
  the two panel reasons were re-checked over `http://localhost:80`.

## Remediation pass (2026-09-22)

Every finding in `audit/AUDIT.md` was addressed, and each behavior change was re-verified on a rebuild. The
commits:

| Repo | Commits |
| --- | --- |
| `timeseries` | `e74ecaa` (tag **`v0.1.1`**) |
| `timeseries-forecast` | `029c690` (tag **`v0.5.0`** — `MaxForecastPoints`, `ErrTooManyPoints`, Welford buckets) |
| `timeseries-baselines` | `0c296fe`, `7ec489f` (depends on the two tags) |
| `timeseries-grafana` | `adf4e05`, `869dfdd`, `8feecaf` (backend, frontend, specs; depends on both tags) |
| `timeseries-k8s` | `2406007` (`PLUGIN_REF=8feecafc14ba…`, `BASELINES_REF=7ec489faafeb…`) |
| `timeseries-grafana-sandbox` | `2bb6528` (local only) |

The Compose plugin was rebuilt for this run (`gpx_forecast_linux_amd64` sha256
`87e5f1e3c6c890b8942484c0a352cb7715f064c1fae1b0414c93e78115d66463`, `forecast-panel/module.js`
`0b3b25d0b56727ff7081524686ff108f792bc0efccbb18fc850ec1fcc4fb4226`), and the Kubernetes release runs the images
tagged with the pins (`…-grafana:8feecafc14ba`, `…-baselines:7ec489faafeb`).

| Finding | Fix | Live re-verification |
| --- | --- | --- |
| **F1** one request killed the process | `windowK` rejects a window above `MaxForecastPoints` (1e6) with `ErrTooManyPoints`; the plugin pre-checks the same bound on the fit, restore and datasource paths and maps the library error to 413 | Compose: the original probe answers **413 `forecast: window has too many points`** and the process stays up (`restarts=0 oom=false`); a 365-day 1-minute window is still 200 (7,173,234 B) and 730 days is 413 — the cap is exactly at 1e6 points. Kubernetes: the same 413 on the new image, `restarts=0` (was `OOMKilled`, exit 137) |
| **F2** decode before the point cap | a body larger than a legal training series (two arrays of `MAX_TRAIN_POINTS` at 32 bytes each) is refused with 413 **before** the decoder | a 16,600,066-byte body → `413 forecast: request body too large for a legal training series`; peak RSS +118 MiB (was +305 MiB) |
| **F3** a `null` query list was claimed forever | `null`/`[]` counts as "no queries" on read, writers store `[]`, and the claim requires a non-empty `queries` array | a fit without queries writes `"queries":[]`; forcing that row due leaves it `still_due=true status=-` through four tick slots (was `400 query.noQueries` on every slot) |
| **F4** `PUT` without `enabled` switched rows off | `Enabled *bool`: absent keeps the stored value | PUT with `enabled:true` then a PUT without it → the row stays `enabled:true` |
| **F5** `±Inf` fit output was a 500 | non-finite values are nulled like `NaN` | `{"values":[1e308,1e308]}` mean → 200 with `values:[null,null]` |
| **F7** `/ping` ignored the method | GET-only, as ARCHITECTURE documents | `GET` 200, `PUT` 405, `DELETE` 405 |
| **F9/F10** history lost datasource config; a superseded load kept drawing | history frames carry the source `field.config`/`meta`; the drawn frames are keyed to the panel's current query | both dashboards still draw history + forecast + bands, and the jest regression test pins the superseded-load case |
| **F11** a relative Quick range was stored absolute | `resolveTrainWindow` marks a relative picker pair (`now-7d` → `now`) as `relative` with `lookbackMs` = its width | choosing **Last 7 days** posts `trainSource{relative:true, lookbackMs:604800000}` and the row reads `relative=true lookbackMs=604800000` (was `relative` absent and a frozen week) |
| **F20** an empty `cacheKey` was a 400 | `QueryData` answers the miss `needTrain` error | `{"kind":"forecast","cacheKey":""}` → `needTrain: train on the Forecast overlay panel first` (was `400 forecast: cacheKey must be 64 lowercase hex chars`) |
| **F23** interval width collapsed on large offsets | Welford buckets and a two-pass mean replace the raw moments | the offset-1e9 band is `2.1170029640197754` wide against `2.1170030603370598` for offset 0 (was `0`) |
| **F24** an unvalidated `DRUID_MAX_RANGE` OOMed the worker | `Validate` requires ≥ 1m (or 0) and `windows()` refuses more than `1<<20` windows before allocating | `DRUID_MAX_RANGE=1ms` → exit 1 with `ERROR config err="DRUID_MAX_RANGE must be at least 1m (or 0 for one request per window)"` (was exit 137 under a 512 MiB cap) |
| **F25/F26/F27/F28** | the store test creates the queue table so the SQL runs; an old `(scope, key)` table is named once; ownership is computed once per span; the publish comment credits the Kafka key | `TestPostgresRetrainQueue` and `TestPostgresScheduleStaleKey` pass against the sandbox store (no skip) |
| **F29/F30/F31** | `JoinLeft`'s sharing is documented and pinned (the accessors copy); `FromPoints` allocates twice instead of three times; the unreachable resample branch is gone | library suites green; the allocation test reports three allocations against the old code |
| **F32-F44, F47** | the chart exposes the nine worker knobs, `postgres.sslMode`, default Grafana resources (2Gi limit) and a DSN-aware `postgres.url`; the store password moved to a Secret; the pod rolls on `retrainCron`/`pluginToken` through a checksum env plus the documented `grafana.configRevision` lever; plugin versions are pinned; the sandbox tags images by pin, imports them into the kind node and no longer shares one dashboards ConfigMap | rendered proofs for each value; `make check-pins` reports both pins at the sibling heads; the cluster shows the Secret, `FORECAST_CONFIG_CHECKSUM=e80beffb…`, `limits.memory=2Gi`; every Compose datasource still reports OK after the pinned preinstall |
| **F46** a panel's old key kept retraining forever | `superseded_at`: a fit stamps the same dashboard panel's other keys and clears its own | switching panel 1 to Last 7 days stamped `2e827911` (`superseded=12:20:43`), switching back to Auto cleared it and stamped the 7-day key; a superseded row is never claimed |
| **D1-D10** | the ten documentation corrections listed under *Discrepancies found* above | each was re-checked against the code before the edit |

Every row the pass-2 record left unit-tested only — and the rollout lever it recorded as not exercised — was
driven live in pass 3 (2026-09-22) on the Compose stack and the Kubernetes release:

| Row | Live observation |
| --- | --- |
| F12 a target with no `datasource` field | a throwaway dashboard whose query A omits `datasource` trained from the panel's own datasource and drew history + forecast (`POST …/forecast` 200, "Using saved model"); the copy was deleted afterwards |
| F13 *Legacy lookback* | the option exists as a panel option (empty = Auto) **and** as a field in the Forecast query editor (`Explore`, datasource `forecast`: *Train from*, *Train to*, *Legacy lookback*); it is part of the `cacheKey` fingerprint (`src/forecast-panel/cacheKey.ts`), which is why the editor's tooltip tells you to keep it equal to the panel's |
| F14 a Mixed panel with a reduce row | **New alert rule** navigated to `/alerting/new` with a `defaults` payload carrying both rows: the Druid metric (query A) and the reduce expression (`refId B`, `datasourceUid __expr__`, `queryType expression`, `model.reducer mean`, `model.expression A`) |
| F15 two option edits in one editing session | *Show prediction interval* off plus *Max in-flight loads* 2 applied without an intermediate save; the save dialog's diff listed `"maxInflightLoads": 2`, `"showInterval": false` and the clamped `"interval": 0.99`, the saved dashboard (version 41) carries all three, and the band left the drawn panel at the same time |
| F21 *Interval coverage* bounds | typing `5` into the field clamps to `0.99` (`COVERAGE_SETTINGS.max`) |
| the `postgres.*`-only rollout lever | `helm upgrade --install timeseries … --set grafana.configRevision=$(date +%s)` created a new ReplicaSet and rolled the Grafana pod (`timeseries-grafana-7f89fd5cb-nr9zw`, 12:55:59) with a new `FORECAST_CONFIG_CHECKSUM=9d2aced3…` (was `e80beffb…`) |

Still not live-verified: the pass-1 rows in the *Verified live, not verified live* table above (the OpenSearch and
Postgres train rejections, the unsaved-dashboard reason, the credential auto-disable, *Copy source from query A*,
`DRUID_MAX_INFLIGHT` saturation, the `FORECAST_MAX_INFLIGHT` env precedence), and the 30 s cache-staleness window
that the HA table already names.

