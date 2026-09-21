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

Both environments were exercised on 2026-09-21 (13:07–14:2x MSK) from the same source revisions, and both were
left running afterwards.

| | Compose sandbox | Kubernetes (Helm) |
| --- | --- | --- |
| Grafana | 13.1.0 (`commit b309c9bb3b81a748c3a75289236a27309ed2566a`), `http://localhost:3000`, anonymous Admin, org 1 | 13.1.0, `svc/timeseries-grafana` — LoadBalancer `EXTERNAL-IP 172.18.0.5:80` (a cluster-internal address); the host reaches it at `http://localhost:80` through Docker Desktop's kind cloud provider (`kindccm-…`, `envoyproxy/envoy:v1.36.7`, `0.0.0.0:80->80/tcp`), and `kubectl -n timeseries port-forward svc/timeseries-grafana 30001:80` (what `make helm-grafana` runs) serves the same — anonymous Admin, org 1 |
| Plugin build | `dist/` mounted from the workspace: `gpx_forecast_linux_amd64` sha256 `34bf53244166948b6a0bbfc9fb79f942da2a4dbd229b54a5b1cba91ae98b43d6`, `module.js` sha256 `42564adfe17e496b3463f344d49fd7c0db3477c978f0e0e5fb3c7e8e13977f15` — both byte-identical inside the Grafana container. The discrepancy fixes were verified on a rebuild of the same tree: `gpx_forecast_linux_amd64` `a034c1ca02c028f065f0bc8eb206e8fec12b950a04d4f22126c9fbfae7c1d4df`, `forecast-panel/module.js` `429a2f99d4257049fdc13c4b7132df4c5a92600b3c0b8d586d6194808e6b4c80` | images built from the same pinned refs: `ghcr.io/eduard-kolotushin/timeseries-grafana:0.1.0` (`b466f49309cf`, built 13:59:36) and `…-baselines:0.1.0` (`11adb8b064c9`, built 13:59:58), imported into the node's containerd with `docker save … \| docker exec -i desktop-control-plane ctr -n k8s.io images import -`; the fix run rebuilt and re-imported the Grafana image from `PLUGIN_REF=8f2a9ff…` (`477c42ed65ff`, manifest `b542555444440409b2ad9e03925141a2c98e58aefd50d66590a1df0ec6665fb8`) and rolled the Deployment |
| Source revisions | `timeseries-grafana` `d6399e5a3451c4cacf433736d28c80a966235502`, `timeseries-baselines` `179a1551e4dd1064b93cbdd42d12fb684a20dfcd` | same two commits, baked into the images by `timeseries-k8s` `PLUGIN_REF` / `BASELINES_REF`. The discrepancy fixes add `10a23cc` and `8f2a9ff` to `timeseries-grafana` (the worker is unchanged) and move the K8s pin to `8f2a9ff` |
| Data plane | Druid 37.0.0 (`http://localhost:8888`, datasource `druid`, tables `minuteweek`/`metrics`/`baselines`), Kafka 3.9.1 (`metrics`, `baselines`), OpenSearch 2.18.0, Prometheus 2.55.1, overlay Postgres 17.6 (schema `forecast`) | Helm releases `kafka`, `druid`, `prometheus`, `opensearch`, `overlay-postgres`, `timeseries` — all `deployed` on one kind node (`desktop-control-plane`, v1.36.1, containerd 2.3.1) |
| Worker | `alpine:3.21` + `/usr/local/bin/baselines` (sha256 `c725417cbd3a84cbb97258b8d40e5368bd9e2434f18373f938c26d748a072c7a`), env from `docker-compose.yaml` | Deployment `timeseries-baselines` (`SHARD_DNS=timeseries-baselines-headless`, `SHARD_MEMBERSHIP=store`), env from ConfigMap `timeseries-baselines-env` |
| Configuration source | provisioning `provisioning/plugins/apps.yaml` + `docker-compose.yaml` env | ConfigMaps `timeseries-forecast-app` (app `apps.yaml`), `timeseries-forecast-datasource`, `timeseries-forecast-store`, `timeseries-baselines-env` |
| Notable | 20 containers, ~4.2 GiB resident | 15.6 GiB allocatable; the two stacks ran **concurrently** without an OOM (≈4 GiB used); no metrics-server |

The Kubernetes plugin image cannot be pulled from GHCR (`timeseries-k8s` carries no `v*` tag, so the two
`:0.1.0` tags are unpublished): both pods first came up `ErrImagePull` / `ImagePullBackOff`, and only the
`ctr -n k8s.io images import` step above made them `1/1 Running`. The Compose sandbox needed no such step
(it mounts the workspace `dist/`). The Kubernetes dashboards are provisioned **read-only** (`Cannot save
provisioned dashboard`), so the panel checks in the discrepancy fixes ran against a throwaway copy of
`forecast-minute-week`, which was deleted afterwards.

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
| **Input params** | `times` (int64 ms, ascending, unique), `values` (number or `null`), `model` (`naive`\|`mean`\|`drift`\|`seasonal`\|`baseline`\|`ses`\|`holt`), `from`/`to` (int64 ms, the forecast window), `alpha`, `beta`, `period`, `season` (`hour`\|`day`\|`week`\|`minute-week`), `calendar` (`""`\|`ru`), `level` (0..1; 0 omits bands), `cacheKey` (64 lowercase hex), `retrain` (bool), `trainSource`, `provenance`. Limits: ≤100000 training points, body ≤16 MiB (413 beyond it), 4 concurrent Fit/ForecastRange calls. |
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
| **Expected result** | 200, JSON array of `{scope,key,cron,timezone,enabled,nextRunAt,lastRunAt,lastStatus,hasSpec,source}`. |

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
| **Input params** | Body `{scope, key, cron, timezone, enabled}`. `cron` is 5-field or `@hourly`/`@daily`; `timezone` is IANA. A `panel` key may be created this way; a `baseline` key must already exist. |
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
| **How configured** | Datasource jsonData `storeHost…`/`storeUrl` (alerting `QueryData` also falls back to the parent app's `AppInstanceSettings`), or the merged `[plugin.eduardkolotushin-forecast-datasource]` ini section. Query editor is manual — no auto-fill; *Copy source from query A* is opt-in. |
| **Input params** | Query `{kind: "forecast"\|"lower"\|"upper", cacheKey, level?}` inside a normal `/api/ds/query` body with `from`/`to`. |
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
| **How configured** | Panel options, stored in the dashboard JSON. Live labels: *Model*, *Forecast range*, *Alpha*, *Beta*, *Seasonal period*, *Seasonality*, *Calendar*, *Show prediction interval*, *Interval coverage*, *Training period*, *Max in-flight loads*, *Saved model*, *Retrain*, and the *Alerting* group (*New alert rule*). Keys: `model`, `forecastRange`, `alpha`, `beta`, `period`, `season`, `calendar`, `showInterval`, `interval`, `trainRange`, `maxInflightLoads` (source: `src/forecast-panel/module.ts`). |
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

### F11. Configuration page — store DSN and default retrain schedule

| | |
| --- | --- |
| **Function** | Set the snapshot-store DSN and the org's default cron in jsonData, and validate them before saving. |
| **Who can use** | **Admin** (Grafana's plugin configuration page, `role: Admin` in `plugin.json`). |
| **How configured** | Fields: *Snapshot store* — Host, Port, Database, User, SSL mode, Password, Reset; *Default retrain schedule* — Cron, Timezone. Saved into `jsonData` (+ `secureJsonData.storePassword`). Env/ini override these at runtime (F20). |
| **Input params** | `storeHost`, `storePort`, `storeDatabase`, `storeUser`, `storeSslMode`, `secureJsonData.storePassword`, `retrainCron`, `retrainTimezone`. |
| **Expected result** | 200 and the alert `Settings not saved` + the reason when the cron does **not** parse; nothing is persisted in that case. |

**Positive — Compose:** `?page=configuration` renders every field with the provisioned values
(`overlay-postgres`, `5432`, `overlay`, `overlay`, `disable`, password `configured`, cron `*/5 * * * *`) and a
`Save` that persists (`jsonData.retrainTimezone: "UTC"` appeared after saving the valid cron).

**Negative — Compose:** typing `nope` into *Cron* and pressing Save → alert `Settings not saved` /
`forecast: invalid cron`, and the stored jsonData was **unchanged** (`retrainCron` still `*/5 * * * *`).

**Kubernetes:** the same page; the chart provisions the identical keys through the `timeseries-forecast-app`
ConfigMap (`apps.yaml` → `jsonData.storeHost: overlay-postgres.overlay-postgres.svc`, `retrainCron: "*/5 * * * *"`,
`secureJsonData.storePassword`).

### F12. Retrain schedules tab — inspect, edit, delete

| | |
| --- | --- |
| **Function** | A page over the `forecast.retrain` table: what will retrain, when, how it last went, and what it belongs to. |
| **Who can use** | **Admin** (peer tab of Overview and Configuration on the plugin configuration page). |
| **How configured** | Store required (F20). Rows come from the overlay's fits (F2) and from the worker (F25). |
| **Input params** | Filters *Search* / *Scope* (All, panel, baseline) / *Enabled* (All, Enabled, Disabled) / *Status* (All, ok, error, never run); per-row cron, timezone and enabled editors; per-row *copy key*, *Save*, *Delete*; a *Source* deep link for `panel` rows. |
| **Expected result** | Columns *Source, Scope, Key, Cron, Timezone, Next run, Last run, Status, Enabled* + actions; errors surface as `Schedules failed` + the backend reason. |

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
| **How configured** | jsonData `retrainCron` (default `0 3 * * *`; both environments use `*/5 * * * *`) and `grafanaUrl` (default `http://127.0.0.1:3000`); the token comes from `FORECAST_GRAFANA_TOKEN`, the ini section, or `secureJsonData.grafanaToken`. |
| **Input params** | Per row: `cron`, `timezone`, `enabled`; per spec: the stored queries and window (`relative` + `lookbackMs` re-resolve at claim time, absolute `from`/`to` replay verbatim). |
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

**Negative — Compose:** the Prometheus-instant case is the negative this row names, and it was **not**
reproduced (F13); the OpenSearch and Postgres branches were not exercised in this sandbox. All three literals are
source-verified in `src/forecast-panel/reasons.ts`.

**Kubernetes:** the same Druid rewrite against the K8s Grafana (the `ready` baseline row and the three panel rows
all retrained to `ok`).

### F20. Snapshot store: DSN resolution and persist on/off

| | |
| --- | --- |
| **Function** | Decide where fitted snapshots live; with no DSN the plugin still fits and forecasts, it just cannot remember. |
| **Who can use** | **Operator** (deployment config), plus **Admin** through the Configuration page (F11). |
| **How configured** | In order: process env `FORECAST_STORE_URL`/`FORECAST_STORE_*` (Grafana 12.4+ does not forward host env by default), then `GF_PLUGIN_EDUARDKOLOTUSHIN_FORECAST_APP_*` / `…_DATASOURCE_*` / `GrafanaCfg` (`[plugin.eduardkolotushin-forecast-app]`, `[plugin.eduardkolotushin-forecast-datasource]`), then jsonData / `secureJsonData`. |
| **Input params** | `storeHost`, `storePort`, `storeDatabase`, `storeUser`, `storeSslMode`, `storePassword` (or `storeUrl`). |
| **Expected result** | With a store: snapshots in `forecast.snapshots (org_id, cache_key, snapshot jsonb, updated_at)` and schedules usable. Without: `/schedules` is 503 and every probe answers `needTrain`. |

**Positive — Compose:** the Configuration page values (`overlay-postgres`/`5432`/`overlay`/`overlay`/`disable`)
come from `provisioning/plugins/apps.yaml`; a fit with `cacheKey 0f…0f` produced a row
(`org_id 1`, `pg_column_size(snapshot)=236`) and the panel row `<org 1> 0f0f…` with a `spec` holding
`{"to":…,"from":…,"model":"holt","season":"","panelId":1,"queries":null,"calendar":"","lookback":"21d","relative":true,"lookbackMs":1814400000,…}`;
the K8s store behaved identically through the chart ConfigMaps.

**Negative — Compose (throwaway Grafana without a DSN, as in F8):** `GET :3001/…/resources/schedules` → **503**
`forecast: snapshot store not configured`; the datasource health → 400
`snapshot store not configured (needTrain)`. Persist-off is not fit-off: `POST /forecast` with points but no
`cacheKey` → 200, and a probe with a `cacheKey` → 200 `{"needTrain":true}`.

**Kubernetes:** the app and the datasource each get the store from their own ConfigMap
(`timeseries-forecast-app`, `timeseries-forecast-datasource`) plus `timeseries-forecast-store` for the plugin
process env — which is why the alerting datasource can restore snapshots without the app.

## timeseries-baselines functions

The worker is one process with an env-only interface and no HTTP surface. `SHARD_ID` defaults to the container
hostname/IP; `SHARD_MEMBERSHIP=store` makes the peer set the `baselines.workers` heartbeat table (the Compose and
K8s configurations both use it). "Owned" counts below are from the live tick lines.

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

**Positive — Compose:** `$PG "SELECT metric_hash, model, season, lookback_ms, trained_at, encode(substring(snapshot from 1 for 2),'hex') FROM baselines.snapshots"`
→ `ready|baseline|minute-week||1209600000|2026-09-21 11:05:28.025158+00|17414|1f8b` (gzip magic, ~17 KB).

**Negative — Compose:** a claimed hash whose Druid window holds no data finishes as
`error: no data in the last 336h0m0s` and writes no snapshot.

**Kubernetes:** `ready|baseline|minute-week|1209600000|2026-09-21 11:11:31.004464+00` — the Deployment trained
and persisted through the chart-provisioned store.

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
| **How configured** | Every knob is an env var: `DRUID_BROKER`, `DRUID_DATASOURCE`, `DRUID_MAX_RANGE`, `DRUID_MAX_RPS`, `DRUID_MAX_INFLIGHT`, `DRUID_TIMEOUT`, `DRUID_RETRIES`, `KAFKA_BROKERS`, `KAFKA_TOPIC`, `LOOKBACK`, `SCAN_RANGE`, `AHEAD_MINUTES`, `INTERVAL`, `TRAIN_CONCURRENCY`, `HASH_SCAN_TTL`, `DEFAULT_RETRAIN_CRON`, `RETRAIN_RETRY`, `SHARD_ID`, `SHARD_PEERS`, `SHARD_DNS`, `SHARD_MEMBERSHIP`, `BASELINE_STORE_*`, `LOG_LEVEL`. |
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
- **`forecast.retrain (scope, org_id, key, cron, timezone, enabled, spec, next_run_at, last_run_at, last_status, claimed_by, claimed_until, updated_at)`**,
  primary key `(scope, org_id, key)` — the queue. `panel` rows are written by the plugin (with `spec` holding the
  queries, window and identity), `baseline` rows by the worker (with no `spec`), and each side claims only what it
  owns: the plugin its own org's `panel` rows, the worker the fleet-wide (`org_id = 0`) `baseline` rows.
- The worker also owns `baselines.snapshots` (gzip `forecast.Snapshot` + `trained_at`) and
  `baselines.workers` (heartbeat). The worker creates only schema `baselines`; `forecast.retrain` is created and
  owned by the plugin, which is why the K8s worker logged `relation "forecast.retrain" does not exist` until the
  plugin's store path had run once.

## Verified live, not verified live

Everything in the scenario lines above was observed on a running system. The following claims come from the
source and are **not** backed by a live observation in this run:

| Claim | Where it lives | Why it was not observed |
| --- | --- | --- |
| The OpenSearch and Postgres train rejections | `src/forecast-panel/reasons.ts`, `trainRewrite.ts` | No OpenSearch or Postgres panel exists in the sandbox dashboards; the Prometheus branch is live (F13). |
| `Dashboard must be saved before alerts can be added.` | `src/forecast-panel/alertFromPanel.ts:4` | The positive (`/alerting/new` with defaults) was verified; the unsaved-dashboard case needs a brand-new unsaved dashboard with the panel, which was not built. |
| Scheduler auto-disable after repeated 401/403 | `pkg/plugin/retrain.go` (`errGrafanaUnauthorized`) | Needs three consecutive rejected fetches across cron ticks; the Compose sandbox uses anonymous Admin and the token path was only exercised in its working (empty-token) form. |
| Druid/Postgres/OpenSearch train-query rewrites beyond Druid | `src/forecast-panel/trainRewrite.ts` | Only the Druid builder and Druid SQL targets exist in the sandbox dashboard. |
| `Copy source from query A` (Forecast editor) | `src/forecast-datasource/` | Opt-in, manual feature; the Forecast editor was not driven by hand. |
| `DRUID_MAX_INFLIGHT` saturation | `druid.go`, `limits.go` | `DRUID_MAX_RPS` was verified; the inflight cap was not driven to saturation. |
| `FORECAST_MAX_INFLIGHT` env/ini precedence | `pkg/plugin/limits.go` | Only the jsonData path was used (the Compose env does not set it). |
| The CI/CD `forecast.ini.template` merge | `conf/forecast.ini.template` | Neither test environment merges the ini; both configure through jsonData/ConfigMaps. |

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
only a message above the 32 MiB transport ceiling is refused by the SDK. The cap itself stays 16 MiB, and
`TestGRPCSettingsClearsTheBodyCap` fails if the two limits are ever equalised again.

```bash
# body = JSON with a junk field of N MiB, times/values empty
for mb in 15 16 16.5 17 20; do … curl --data-binary @body-$mb.json … ; done
```

| Body | Before | After |
| --- | --- | --- |
| 15 MiB | 400 `forecast: series is empty` | 400 `forecast: series is empty` |
| 16 MiB, 16.5 MiB, 17 MiB, 20 MiB | 500 `{"statusCode":500,"messageId":"plugin.requestFailureError",…}` | **413** `forecast: request body too large` |
| 33 MiB | 500 | 500 — above the transport ceiling, refused by the SDK and not by the plugin |

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
