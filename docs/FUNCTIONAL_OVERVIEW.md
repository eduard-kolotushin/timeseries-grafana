# Functional overview

What a caller can invoke in `timeseries-grafana` and `timeseries-baselines`, as whom, configured how, with what
input, and what comes back — plus a positive and a negative scenario observed live on two environments.

`timeseries-grafana` ships three Grafana plugins from one backend binary (`gpx_forecast`) plus the
`gpx_forecast_migrate` CLI, which applies the schema out of process (F33): the **app**
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
MIG='FORECAST_STORE_URL=postgres://overlay:overlay@127.0.0.1:5433/overlay?sslmode=disable go run ./cmd/migrate'  # schema CLI (F33); add --dry-run to look without writing
```

`docker compose` commands run in `C:/Users/Eduard/Cursor/timeseries-grafana-sandbox`. In the Kubernetes
environment the same route is `B=http://localhost:80/api/plugins/eduardkolotushin-forecast-app/resources` — the
LoadBalancer's host port, published by Docker Desktop's kind cloud provider (see
[Environment under test](#environment-under-test)); `kubectl -n timeseries port-forward svc/timeseries-grafana
30001:80`, which the sandbox `make helm-grafana` runs, answers the same. Postgres is reached with
`kubectl -n overlay-postgres exec deploy/overlay-postgres -- psql -U overlay -d overlay -tAc "<sql>"`.

## Environment under test

Both environments were first exercised on 2026-09-21 from 13:07 (times in this document are UTC unless a line
says otherwise) from the same source revisions, and both were left running afterwards. A third pass the same day (14:16–14:31 UTC) added a **temporary second release** of
the same chart with two Grafana replicas over one Postgres, to observe the HA claim
[Scaling and HA](#scaling-and-ha); it was removed again at the end of the run.

The **rows below carry the remediated state**, re-measured on 2026-09-22 (12:39–13:00 UTC) on that same
still-running pair of stacks; a transcript that quotes a build hash quotes the build *its own* run used, so the
2026-09-21 numbers in the discrepancy sections stay as observations of that day.

| | Compose sandbox | Kubernetes (Helm) |
| --- | --- | --- |
| Grafana | 13.1.0 (`commit b309c9bb3b81a748c3a75289236a27309ed2566a`), `http://localhost:3000`, anonymous Admin, org 1 | 13.1.0, `svc/timeseries-grafana` — LoadBalancer `EXTERNAL-IP 172.18.0.5:80` (a cluster-internal address); the host reaches it at `http://localhost:80` through Docker Desktop's kind cloud provider (`kindccm-…`, `envoyproxy/envoy:v1.36.7`, `0.0.0.0:80->80/tcp`), and `kubectl -n timeseries port-forward svc/timeseries-grafana 30001:80` (what `make helm-grafana` runs) serves the same — anonymous Admin, org 1 |
| Plugin build | **2026-09-22 (head `12b6381`, pass 4)**: `dist/` mounted from the workspace — `gpx_forecast_linux_amd64` sha256 `7b62cf133fb88afcaf230515747ae367c6eec76dc956d30cfa0e111712b58073`, app `module.js` sha256 `167f3dcf36d9cf4e635ace39d7d483daa2645c1051fcf92d87ad1425ed0c6613`, `forecast-datasource/module.js` sha256 `2c4fef85936ffafdcf6848f0946c330409e69096a4d239e8395d078905264bf2`, `forecast-panel/module.js` sha256 `0b3b25d0b56727ff7081524686ff108f792bc0efccbb18fc850ec1fcc4fb4226` — every one byte-identical inside the Grafana container. The binary embeds `vcs.revision=12b6381…`, so its hash moves with every commit; the bundles are content-addressed and do not. Pass 3 measured `33b9ede9…` for the binary at `74b12e4`. For reference: pass 1 measured `34bf53244166948b6a0bbfc9fb79f942da2a4dbd229b54a5b1cba91ae98b43d6` / `42564adfe17e496b3463f344d49fd7c0db3477c978f0e0e5fb3c7e8e13977f15`, the discrepancy rebuild `a034c1ca02c028f065f0bc8eb206e8fec12b950a04d4f22126c9fbfae7c1d4df` / `429a2f99d4257049fdc13c4b7132df4c5a92600b3c0b8d586d6194808e6b4c80`. **2026-09-27 (pass 5, head `28916fe` plus the v14 working tree, Compose):** `gpx_forecast_linux_amd64` (and its `forecast-datasource/` copy) `bab9bf26f641ccb6424b9cc44ecb1e46a3a41c257168f7ee8bb7635a450c365f`, `gpx_forecast_migrate_linux_amd64` `ffa86a9def2c1c11ed2b1bd5cbb3ceaf585cd77808b7a11af4080ce8d930d11d`, app `module.js` `c73acf8abe18db252b563881a223f690d07b1d3303787c6c02a0427bd5dc5277`, `forecast-datasource/module.js` `c10657243a5507ddcf5d9ab1928922787397a44f09a10bf88f666e5b0347ec03`, `forecast-panel/module.js` `b8234d7fd984e5586f21025bdf2e056063a92cc5b9ae171f9625500ffc7f8399` — all six re-checked inside the container and byte-identical to the host files (the migrator included, which the same `dist/` mount publishes). The Kubernetes release was **not** re-measured in pass 5; its last measured pins remain pass 4's. | images built from the pinned refs and tagged by the **pin's short sha**: `ghcr.io/eduard-kolotushin/timeseries-grafana:12b6381e2092` and `…-baselines:7ec489faafeb`, imported into the node's containerd by the sandbox's `make helm-import` (`docker save … \| docker exec -i desktop-control-plane ctr -n k8s.io images import -`); the pass-3 `--set grafana.configRevision=…` run rolled the Deployment to `timeseries-grafana-7f89fd5cb-nr9zw`, and the pass-4 release runs the tags above (`timeseries-grafana-66bddd6cc4-zb6dl`, `timeseries-baselines-5c7d8976db-p5x6k`, both `1/1 Running`) |
| Source revisions | **2026-09-22 (pass 4)**: `timeseries-grafana` `12b6381e20920b31f8cc2a7087377d018006652d`, `timeseries-baselines` `7ec489faafeb85971dd5f5c247aaf0bc37c63913`; the plugin depends on the tags `timeseries v0.1.1` (`e74ecaa`) and `timeseries-forecast v0.5.1` (`ec7c534`) — this head still pinned `v0.5.0` (`029c690`), and `84ad9b6`, the commit the pass-4 refresh then deployed, moved it (and `timeseries-baselines`' `a82e0f3`) to `v0.5.1` — and `timeseries-forecast/go.mod` requires `timeseries v0.1.1`. Pass 3 measured `74b12e46dc28…`; the store-UI commits added `861d25d` and `12b6381`. **Pass 5 (2026-09-27)** measured `timeseries-grafana` `28916fe087cd91543d1cbd682b58adc803d435c6` with the v14 retention and audit change set on top at measure time (that tree was then committed as `c8339fe`), `timeseries-baselines` `7ec489faafeb85971dd5f5c247aaf0bc37c63913` plus its own v4/v5 working tree (migrations + retention), and the dependency tags above (`timeseries v0.1.1`, `timeseries-forecast v0.5.1`) | the same two commits, baked into the images by `timeseries-k8s` `64ae2f5`'s `PLUGIN_REF=12b6381e2092…` / `BASELINES_REF=7ec489faafeb…` — both pins at the sibling heads, which `make check-pins` reports. **Pass 7 (2026-10-05)**: all three consumers require `timeseries v0.2.0` (the step-extrapolation fix — a target time past the last observation is NaN, not the stale last value — and the removal of the unreleased `ErrEmpty`/`AsRegular`, which no call site used), and `timeseries-baselines` requires `timeseries-forecast v0.5.3` |
| Data plane | Druid 37.0.0 (`http://localhost:8888`, datasource `druid`, tables `minuteweek`/`metrics`/`baselines`), Kafka 3.9.1 (`metrics`, `baselines`), OpenSearch 2.18.0, Prometheus 2.55.1, overlay Postgres 17.6 (schema `forecast`) | Helm releases `kafka`, `druid`, `prometheus`, `opensearch`, `overlay-postgres`, `timeseries` — all `deployed` on one kind node (`desktop-control-plane`, v1.36.1, containerd 2.3.1) |
| Worker | `alpine:3.21` + `/usr/local/bin/baselines` (2026-09-22 build sha256 `759a9be5280e986e56a179c28befe5cc38f405b1b9de1efb9080fada4ff01e46`; pass 1 measured `c725417cbd3a84cbb97258b8d40e5368bd9e2434f18373f938c26d748a072c7a`), env from `docker-compose.yaml` | Deployment `timeseries-baselines` (`SHARD_DNS=timeseries-baselines-headless`, `SHARD_MEMBERSHIP=store`), env from ConfigMap `timeseries-baselines-env` |
| Configuration source | provisioning `timeseries-grafana-sandbox/provisioning/plugins/apps.yaml` + `docker-compose.yaml` env | ConfigMaps `timeseries-forecast-app` (app `apps.yaml`), `timeseries-forecast-datasource`, `timeseries-forecast-store`, `timeseries-baselines-env` |
| Notable | 21 containers on 2026-09-22 (`docker ps --format '{{.Names}}' \| wc -l`: the 17 Compose services plus the four kind containers; pass 3's own count of the Compose services was 17, pass 1's 18), ~4 GiB resident | 15.6 GiB allocatable; the two stacks ran **concurrently** without an OOM (≈4 GiB used); no metrics-server |

The Kubernetes plugin image is still not *pulled* from GHCR (`timeseries-k8s` carries no `v*` tag, so the two
`:0.1.0` tags are unpublished), but the sandbox no longer asks it to be: `make helm-images` builds both images
from the Dockerfiles' pinned sibling refs, tags them by the pin's short sha (`…-grafana:5b50446910f4`,
`…-baselines:fe0c1cb4bd8f` — an immutable tag, which `pullPolicy: IfNotPresent` cannot mask behind a cached
one), and `make helm-import` loads them into the node's containerd; `make helm-up`/`helm-refresh` run the import
before the upgrade, so the pods come up `1/1 Running` without a manual `ctr` step. The Compose sandbox needs
none of that (it mounts the workspace `dist/`: the in-container `gpx_forecast_linux_amd64`,
`forecast-datasource/gpx_forecast_linux_amd64`, `gpx_forecast_migrate_linux_amd64` and the three frontend bundles
are byte-identical to the host files; pass 5 re-checked all six, sha256 in the Plugin-build row). The Kubernetes dashboards are
provisioned **read-only** (`Cannot save provisioned dashboard`), so panel checks that need an edit run against a
throwaway copy of `forecast-minute-week`, which is deleted afterwards; Compose dashboards are writable and are
restored from `timeseries-grafana-sandbox/provisioning/dashboards/minute-week.json` after such a check.

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
| **Input params** | `times` (int64 ms, ascending, unique), `values` (number or `null`), `model` (`naive`\|`mean`\|`drift`\|`seasonal`\|`baseline`\|`ses`\|`holt`), `from`/`to` (int64 ms, the forecast window), `alpha`, `beta`, `period`, `season` (`hour`\|`day`\|`week`\|`minute-week`), `calendar` (`""`\|`ru`), `level` (0..1; 0 omits bands), `cacheKey` (64 lowercase hex), `retrain` (bool), `trainSource`, `provenance`, `panelKeys` (string[], the panel's visible series keys, ≤64 — a longer set is refused with `400 forecast: panelKeys holds more than 64 keys`, and a panel showing more series than that sends none, falling back to the single-key supersede; sent with every fit so a supersede keeps every series the panel still shows). Limits: ≤100000 training points (`413 forecast: training series exceeds 100000 points`), one emitted window capped at 1000000 points (413, checked before the fit), 4 concurrent Fit/ForecastRange calls (`429 forecast: busy`). Bodies are capped three ways: a declared `Content-Length` above `2 × 100000 × 32 B + 1048576 = 7448576` is refused **before** decoding with `413 forecast: request body too large for a legal training series`, a decoded `trainSource` larger than 1 MiB with `413 forecast: trainSource is larger than 1048576 bytes` (a fit and a probe alike), and one above the 16 MiB body cap with `413 forecast: request body too large`; a body past the 32 MiB transport ceiling never reaches the handler (Grafana answers its own 500 `grpc: received message larger than max`). The pre-flight reads `Content-Length` only, so a chunked body is bounded by the decoder's `MaxBytesReader` instead — this is not reachable through Grafana's proxy, which buffers the body and re-sends it with a known length (verified 2026-09-22: the same 14.23 MB body sent `Transfer-Encoding: chunked` still answered the pre-flight's 413). |
| **Expected result** | 200 with `times`/`values` (and `lower`/`upper` when `level≠0`); points start at `last_time + step` and are clipped to `[from,to]`. An empty fit → 400 `forecast: series is empty`. With `cacheKey`+points the snapshot is stored and the panel's schedule row is written, and the panel's other rows a former cache key trained are retired unless `panelKeys` names them as still visible (a fit that omits `panelKeys` keeps only its own row current). |

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

### F5. `DELETE /schedules` — drop a row, or the row and its model

| | |
| --- | --- |
| **Function** | Remove one row. A `panel` row is this org's; a `baseline` row is fleet-wide and is re-created by the worker on its next tick while the metric still reports. |
| **Who can use** | **Admin only** (403 as Viewer). |
| **How configured** | Store required. |
| **Input params** | Query `?scope=…&key=…`, both required, plus optional `drop=row` (the default) or `drop=model`. |
| **Expected result** | 200 `{"message":"ok"}`; 400 on missing/invalid parameters (`drop` outside `row`/`model` → 400 `forecast: invalid drop: want row or model`). `drop=row` is v12's contract: the row goes and the model stays, because the snapshot is what the panel's `cacheKey` still resolves to. `drop=model` removes the snapshot and the row together, so the retention reconcile (F16) cannot hand the row back on its next tick; a `baseline` key's model lives in the worker's schema, so that call also answers a `note` saying so. |

**Positive — Compose:** `DELETE "$B/schedules?scope=panel&key=0f…0f"` → `{"message":"ok"}` (row gone);
`DELETE "$B/schedules?scope=baseline&key=ready"` → `{"message":"ok"}`, and on the following worker tick the row
was back (`ready`, cron `*/5 * * * *`, `last_status ok`) with the tick reporting `retrained=1` — the worker's
insert path, not a leftover.

**Negative — Compose:** `DELETE $B/schedules` → 400 `forecast: scope and key required`;
`?scope=nope&key=ready` → 400 `forecast: invalid scope`.

**Kubernetes:** 400 `forecast: scope and key required` for the parameterless call; the tab's `Delete` removes the
row immediately (no confirmation dialog) as observed on Compose.

**Pass 5 — Compose (a temporary `snapshotTtl: 1h`):** `drop=row` on a `panel` key removed the row, kept the
snapshot, and the next tick gave the row back with the deployment default cron (`*/5 * * * *`, `last_run_at` NULL);
`drop=model` removed both (`snaps c=0`) and the following tick re-created neither; `drop=everything` → **400**
`forecast: invalid drop: want row or model`, with the row still in place.

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
| **How configured** | Datasource provisioned jsonData — `storeUrl` as one DSN, or field-wise `storeHost`/`storePort`/… (the datasource's config editor renders only a read-only list of the `store*` keys it finds, never a value; F20) — or the merged `[plugin.eduardkolotushin-forecast-datasource]` ini section. Alerting `QueryData` also falls back to the parent app's `AppInstanceSettings`. Query editor is manual — no auto-fill; *Copy source from query A* is opt-in. |
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
| **How configured** | Panel options, stored in the dashboard JSON. Live labels: *Model*, *Forecast range*, *Alpha*, *Beta*, *Seasonal period*, *Seasonality*, *Calendar*, *Show prediction interval*, *Interval coverage*, *Training period*, *Legacy lookback*, *Max in-flight loads*, *Saved model*, *Retrain*, and the *Alerting* group (*New alert rule*). Keys: `model`, `forecastRange`, `alpha`, `beta`, `period`, `season`, `calendar`, `showInterval`, `interval`, `trainRange`, `lookback`, `maxInflightLoads` (source: `src/forecast-panel/module.ts`). The same file registers two button-only editors whose keys store nothing — `retrainAction` (*Saved model*) and `alertAction` (*New alert rule*): they never call `onChange`, so they are named after actions rather than options. |
| **Input params** | `forecastRange`/`trainRange` are `{from,to}` raw strings — empty means **Auto** (`now` → `now` + model duration; the last model window ending at the panel's `to`). `interval` 0 hides the band; `maxInflightLoads` minimum 1 (default 1). |
| **Expected result** | The panel redraws with the new option. |

**Positive — Compose:** `Interval coverage 0` → the panel's probe carried `level=0` and panel 1 drew **no** band
while panels 2 and 3 kept theirs; `Max in-flight loads 2` → all three panels still probed (`level=0.95`) and
rendered. Both options were applied through the dashboard JSON API and reverted afterwards (verified: panel 1's
options back to `{alpha:0.8,beta:0.2,calendar:"",horizon:180,model:"baseline",period:7,season:"minute-week"}`).
Pass 4 repeated both through the same API: panel 1's probe carried `level=0` while panels 2 and 3 stayed at
`0.95`, and after the revert all three probed at `0.95` with the options above (dashboard version 48).

**Negative — Compose:** an inverted **Forecast range** (`2027-01-01 → 2026-01-01`) → `Forecast failed` /
`Forecast range is inverted or invalid`, no *training* `/api/ds/query` (the panel's own display query still runs,
so history stays drawn) and **no** `/forecast` request from that panel. An inverted
**Training period** reports the same way — `Forecast failed` / `Training period is inverted or invalid`, history
still drawn, no train query and no `/forecast` even after `Retrain` (it used to fall back to the Auto window and
train 30006 points; see [Discrepancies](#discrepancies-found)).

**Kubernetes:** the same dashboard and option set (the panels come from the ConfigMap); the K8s store shows the
resulting `panel` rows with `cron */5 * * * *`.

### F11. Configuration page — default retrain schedule

| | |
| --- | --- |
| **Function** | Set the org's default cron and timezone in jsonData, validating the cron before saving. The snapshot store has no representation on this page: it is deployment configuration (F20). |
| **Who can use** | **Admin** (Grafana's plugin configuration page, `role: Admin` in `plugin.json`). |
| **How configured** | Fields: *Default retrain schedule* — Cron, Timezone. A save posts only `retrainCron`/`retrainTimezone` merged over the existing jsonData, and sends no `secureJsonData`, so neither a provisioned store value nor the store password can be overwritten from the page. The page prints no store key list and no explanation of the missing fields. |
| **Input params** | `retrainCron`, `retrainTimezone`. |
| **Expected result** | 200 and the alert `Settings not saved` + the reason when the cron does **not** parse; nothing is persisted in that case. |

**Positive — Compose:** `?page=configuration` renders the *Default retrain schedule* fields with the provisioned cron
`*/5 * * * *` and the timezone; the page has no store field, no store key list and no explanation of why the store
has none. A `Save` persists only the retrain keys (`jsonData.retrainTimezone: "UTC"` appeared after saving the valid
cron while the provisioned store keys stayed untouched).

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
| **Input params** | Filters *Search* / *Scope* (All, panel, baseline) / *Enabled* (All, Enabled, Disabled) / *Status* (All, ok, error, never run); per-row cron, timezone and enabled editors; per-row *copy key*, *Save*, *Delete*, *Delete model*; a *Refresh* button; 20-row client-side paging; a *Source* deep link for `panel` rows. *Delete* removes the row (`DELETE /schedules?scope=&key=`, F5) and leaves the model; *Delete model* adds `drop=model`, which removes the snapshot with it, so the backend's retention reconcile cannot give the row back on its next tick (v14). |
| **Expected result** | Columns *Source, Scope, Key, Cron, Timezone, Next run, Last run, Status, Enabled* + actions; a row a newer key superseded shows a *Superseded* marker beside its status and is never retrained again; errors surface as `Schedules failed` + the backend reason; a `drop=model` of a `baseline` row answers with a note that the model belongs to the worker's schema, which the page renders as an info alert. |

**Positive — Compose:** the tab listed 6 then 5 rows; the `panel` rows showed the panel title and
`value 21d Druid: minuteweek · minute` as their source with `/d/forecast-minute-week?viewPanel=N` links, and the
`baseline` row showed `—` as its source with a copy-key button. Editing my fixture row's cron to `*/3 * * * *`
and pressing *Save* → no alert and the server row read `*/3 * * * *` (the editor writes through F4).

**Negative — Compose:** the same editor with `nope` → alert `Schedules failed` / `forecast: invalid cron` and the
server row unchanged. Pressing *Delete* on the fixture row removed it immediately (`before=5 after=4`,
`rowGone=true`, no confirmation dialog).

**Kubernetes:** the tab lists the K8s store's 4 rows with the same columns, derived sources and deep links
(`ready` baseline + the three dashboard panels, all `last ok`).

**Pass 5 — Compose:** `DELETE …/schedules?scope=panel&key=…&drop=model` — the request the tab's **Delete model**
action sends, pinned by that action's own jest case — answered `{"message":"ok"}` for a `panel` key and, for a
`baseline` row, `{"message":"ok","note":"the model behind a baseline key lives in the baselines worker's schema and
is collected there"}`. The page renders that `note` as an info alert (unit-tested; the button itself was not
clicked in this pass). The `Delete` beside it still leaves the model (F5).

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
(`src/forecast-panel/alertFromPanel.ts:6`: `'Dashboard must be saved before alerts can be added.'`) and is emitted
when the panel has no dashboard UID (pass 4 attempted it and stopped at Grafana 13's add-panel flow).

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
| **How configured** | jsonData `retrainCron` (default `0 3 * * *`; both environments use `*/5 * * * *`) and `grafanaUrl` (default `http://127.0.0.1:3000`); the token comes from `FORECAST_GRAFANA_TOKEN`, the ini section, or `secureJsonData.grafanaToken`. The ticker itself is `FORECAST_RETRAIN_ENABLED` (default `true`, the same precedence chain) with `FORECAST_RETRAIN_TICK` (default `30s`), `FORECAST_RETRAIN_LEASE` (derived from the claim batch and the fetch timeout — 6m today) and `FORECAST_RETRAIN_RETRY_MAX` (the retry backoff's cap, default `1h`; a value that does not exceed the lease is refused with the default kept). |
| **Input params** | Per row: `cron`, `timezone`, `enabled`; per spec: the stored queries and window. A window the picker expressed relatively — Auto, a legacy duration, or a Quick range such as `now-7d`/`now` — is stored as `relative: true` with `lookbackMs` and re-resolved at claim time, so a cron retrain follows the clock; a calendar/absolute pick, a window that does not end at `now`, and a rounded bound stay absolute and replay verbatim. |
| **Expected result** | A row's claim is **extended to `FORECAST_RETRAIN_LEASE` right before its work** and the work runs under that deadline, so a fit cannot outlive its claim; an extension that matches zero rows means another replica took the row, and it is skipped without fitting or finishing. `next_run_at` advances, `last_run_at`/`last_status` are written, `forecast.snapshots.updated_at` moves; a failure records `last_status = "error: … (attempt N)"` and is due again after `min(lease × 2^(N-1), FORECAST_RETRAIN_RETRY_MAX)`, with `N` reset to 0 by a success and left alone by a busy compute slot (`errBusy`), which only costs a tick. Every asked-for tick logs `msg="retrain tick" claimed=… ok=… failed=…`, and no tick ever fails a user query. Every tick also **collects** what nothing refreshes (v14): a snapshot untouched for `FORECAST_SNAPSHOT_TTL` (default `72h`; `0` disables the sweep, a value below `1h` is refused and the default kept) is deleted, a row idle for the whole window with no snapshot behind it goes with it (a row another replica has claimed is not idle — neither `next_run_at` nor `last_run_at` moves while a claim is in flight — so a sweep cannot delete a row mid-fit and have the reconcile hand back the deployment default in place of an admin's cron), and a snapshot without a row gets the deployment default schedule back — which is what keeps *deleting a schedule row* from deleting the model (F5). `FORECAST_RETRAIN_ENABLED=false` turns the sweep off with the ticker; a sweep failure is a log line, never a failed query. |

**Positive — Compose:** `$PG "SELECT scope,key,last_run_at,last_status,next_run_at FROM forecast.retrain ORDER BY next_run_at"`
showed the panel rows advancing on the `*/5` cron with `last_status ok` (`…10:55:24Z`, then `…11:00:32Z`), and
Grafana's log carried one `msg=retrain … status=ok dur=230ms` line per row per slot.
**Kubernetes:** the same cron from the chart ConfigMap — after the dashboard was loaded once, the K8s rows read
`next_run_at 11:15:00Z`, `last_status ok` and `forecast.snapshots.updated_at` moved with them.

**Negative — Compose:** pointing `jsonData.grafanaUrl` at a dead port is the documented way to break it; not
exercised live, because the same code path's failure recording is visible on the worker side (F26) and the
scheduler never fails a query. The `needTrain` flag is the user-visible half: when a row is due, the next probe
returns `{"needTrain":true}` (F2c), which is what makes the overlay refit on the next dashboard load.

**Pass 5 — Compose (a temporary `snapshotTtl: 1h`):** planning three fixtures and one pre-existing stale pair, one
tick logged `msg="forecast retention" recreated=1 rows=2 snapshots=2` — a snapshot and row backdated three hours
went, the four-day-idle *superseded* key `2e827911…` went with its snapshot, the fresh snapshot whose row had been
idle for three hours survived **with the admin's `*/9 * * * *` cron** (the scheduler then retrained it), and the
orphan snapshot was re-created as a row with the deployment default `*/5 * * * *`, `enabled true` and no `spec`
(unclaimable, which is why the reconcile cannot resurrect a fetchable-but-unscheduled row). A later tick logged
`recreated=1 rows=0 snapshots=0` after a `drop=row` removed the row it had just given back (F5).

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

**Kubernetes:** one org in play; the same `org_id` column and the same `UNIQUE (scope, org_id, key)` beside the uuid primary key.

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
| **Expected result** | With a store: snapshots in `forecast.snapshots (id uuid pk, org_id, cache_key, snapshot jsonb, updated_at)` and schedules usable. Without: `/schedules` is 503 and every probe answers `needTrain`. Retention (v14): the retrain ticker deletes a snapshot nothing refreshed for `FORECAST_SNAPSHOT_TTL` (default `72h`, `0` disables), so the table does not grow forever, and gives a snapshot without a schedule row the deployment default back rather than orphaning it (F16). |

**Positive — Compose:** the store comes from provisioning, not from a page:
`timeseries-grafana-sandbox/provisioning/plugins/apps.yaml`
(app) and `timeseries-grafana-sandbox/provisioning/datasources/datasources.yml` (Forecast datasource) carry `storeHost: overlay-postgres`,
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
| **How configured** | `TRAIN_CONCURRENCY` (2), `RETRAIN_RETRY` (the retry backoff's base, 5m), `RETRAIN_LEASE` (the claim's work lease, derived as `max(RETRAIN_RETRY, ceil(LOOKBACK / DRUID_MAX_RANGE) × DRUID_TIMEOUT + 1m)` — 15m at the sandbox shape), `RETRAIN_RETRY_MAX` (the backoff's cap, default 1h, refused at startup unless it exceeds `RETRAIN_RETRY`), `SHARD_MEMBERSHIP=store`. |
| **Input params** | None; the claim takes the oldest due rows with `FOR UPDATE SKIP LOCKED` and stamps `claimed_by`/`claimed_until`. Each claim is then re-extended to `RETRAIN_LEASE` right before its fit, so a fit that takes longer than a tick is still covered. |
| **Expected result** | `last_status`/`last_run_at`/`next_run_at` move on success, with `attempts` reset to 0; a failure records `error: … (attempt N)` and is due again after `min(RETRAIN_RETRY × 2^(N-1), RETRAIN_RETRY_MAX)` — sooner than the cron for a fresh failure, at the cap for a chronic one. An expired lease re-admits the row to any survivor (the row is still due, because a claim never moves `next_run_at`), an extension that matches zero rows means a survivor owns it and is skipped with no Druid request and no finish, and the tick logs `msg="retrain tick" claimed=… retrained=… failed=…`. |

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
| **Input params** | Nothing; the worker writes `baselines.workers (id, worker_id, last_seen, owned, peers)` each tick. |
| **Expected result** | One row per live worker with a fresh `last_seen`; a stopped worker's row ages out and its share is taken over. |

**Positive — Compose:** `$PG "SELECT id,worker_id,last_seen,owned,peers FROM baselines.workers"` → the live worker with
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
| **Expected result** | No published port, no listener in the network namespace, exit code 0 on `stop`, and the claims this tick still holds are **released** on the way out (`next_run_at = now`, `last_status = 'error: interrupted'`, `attempts` untouched) so a rolling restart hands the queue back instead of parking up to `TRAIN_CONCURRENCY` rows per worker for a whole lease. |

**Positive — Compose:** `docker compose config` reports no `ports`/`expose` for the worker; inside the network,
`nc -z -w2 <worker-ip> 8080` returned rc=1 (nothing listening) against a control host where it returned rc=0;
`docker compose stop baseline-worker` exited 0.

**Negative — Compose:** the same probe against the Grafana container succeeds, which is what makes the empty
result for the worker meaningful.

**Kubernetes:** the Deployment declares no container port and the chart ships no probes (the sandbox's K8s path
inherits that).

### F33. `gpx_forecast_migrate` — apply the schema before Grafana starts

| | |
| --- | --- |
| **Function** | Apply the embedded versioned migrations (`pkg/store/migrations`) out of process, so a pipeline can prepare the database before Grafana starts. |
| **Who can use** | **Operator** — a CI/CD step or a human holding the store DSN. No Grafana identity and no HTTP surface. |
| **How configured** | `--dsn`, or the same `FORECAST_STORE_*` / `GF_PLUGIN_EDUARDKOLOTUSHIN_FORECAST_APP_*` env chain the plugin reads (no grafana.ini, no jsonData); `--timeout` (default 60s). `make migrate` builds `dist/gpx_forecast_migrate_linux_amd64`. |
| **Input params** | `--dsn`, `--dry-run`, `--timeout`. |
| **Expected result** | `applied NNNN_name` per pending file, `pending NNNN_name` under `--dry-run`, or `nothing to apply (2 known, 2 applied)`; exit 0. A failing file is named with exit 1 and leaves neither its DDL nor its ledger row. |

One transaction per file, opened with `pg_advisory_xact_lock(0x666f726563617374)`, against the ledger `forecast.schema_migrations (id uuid pk, version TEXT UNIQUE, name, applied_at)` — created by the engine, not by a migration file, so a CI job and every Grafana replica's lazy apply serialise. Running it is **optional**: the plugin applies the same set at its first store use, one request late.

**Positive — Compose (pass 5, head `28916fe` plus the v14 working tree):** `$MIG` → `nothing to apply (2 known, 2 applied)` (exit 0), the same with `--dry-run`, and the *shipped linux artifact* inside the Grafana container — `docker exec timeseries-grafana-sandbox /var/lib/grafana/plugins/eduardkolotushin-forecast-app/gpx_forecast_migrate_linux_amd64`, which reads that container's own `FORECAST_STORE_*` env — → `nothing to apply (2 known, 2 applied)` (exit 0).

**Negative — Compose (a scratch database created for the check, dropped afterwards):** `--dry-run` → `pending 0001_snapshots` / `pending 0002_retrain` (exit 0) and `to_regnamespace('forecast')` **false** — nothing is created, not even the ledger; a real run then printed `applied 0001_snapshots` / `applied 0002_retrain` and a second run `nothing to apply (2 known, 2 applied)`, leaving `snapshots_pkey PRIMARY KEY (id)`, `snapshots_org_cache_key_unique UNIQUE (org_id, cache_key)`, `retrain_pkey PRIMARY KEY (id)`, `retrain_scope_org_key_unique UNIQUE (scope, org_id, key)` and `id uuid DEFAULT gen_random_uuid()` on all three tables.

### F34. Overview page — the app's landing page

| | |
| --- | --- |
| **Function** | The app's root page at `/a/<plugin id>` (the *Overview* tab, a peer of Configuration and Retrain schedules). |
| **Who can use** | **Any user** who can open the plugin's page. |
| **How configured** | Nothing: the page is static copy (`src/module.tsx`, `src/components/App/App.tsx`, `src/pages/Home.tsx`) and calls no plugin resource route. |
| **Input params** | None. |
| **Expected result** | 200 rendering the plugin's own text, with no `/api/plugins/eduardkolotushin-forecast-app/resources/…` request issued by the page. |

**Positive — Compose (pass 5):** loading the page as an anonymous Admin rendered "Overlay univariate forecasts from timeseries-forecast on Grafana queries. Add the Forecast overlay visualization to a dashboard panel…" and the page made **no** `/resources/…` request (the only resource calls in that session came from the dashboard panels).

## The contract between the two

The two processes meet at exactly two places, both in the overlay Postgres:

- **`forecast.snapshots (id uuid pk, org_id, cache_key, snapshot, updated_at)`** — the fitted state, keyed by the 64-hex
  `cacheKey` the panel computes (`UNIQUE (org_id, cache_key)`; the uuid `id` is the surrogate primary key every table
  here carries since v13). Observed live: `pg_column_size(snapshot)` `236` (a two-point API fit) up to
  `447908` (a panel's minute-of-week fit); the column is `jsonb`, and the panel's `trainSource`/`provenance`
  fields are **not** part of the fingerprint — a Mixed panel held the same key as the metric-only run.
- **`forecast.retrain (id, scope, org_id, key, cron, timezone, enabled, spec, next_run_at, last_run_at, last_status, claimed_by, claimed_until, superseded_at, updated_at)`**,
  uuid primary key `id`, `UNIQUE (scope, org_id, key)` — the queue. `panel` rows are written by the plugin (with `spec` holding the
  queries, window and identity), `baseline` rows by the worker (with no `spec`), and each side claims only what it
  owns: the plugin its own org's `panel` rows, the worker the fleet-wide (`org_id = 0`) `baseline` rows.
  `superseded_at` is the plugin's retire marker (see [Scaling and HA](#scaling-and-ha) and finding F46 of the pass-2 report (`audit/AUDIT-2026-09-22-pass2.md`)): the worker reads
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

- The four load caps (`MAX_TRAIN_POINTS`, the 1,000,000-point emitted-window cap, `MAX_INFLIGHT`, the 16 MiB body cap) are **per process**, so N
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
| `REASON_TRAIN_MULTI_DATASOURCE` (a series whose datasource group the stored `trainSource` cannot replay) | `src/forecast-panel/trainQuery.ts`, `src/forecast-panel/overlayLoad.ts`, `src/forecast-panel/reasons.ts` | Needs one panel whose metric targets live on two datasources (Mixed); every sandbox dashboard trains from one, so the literal is source-verified rather than measured. |
| The OpenSearch and Postgres train rejections, and the Druid/Postgres/OpenSearch rewrite branches beyond the Druid builder and Druid SQL | `src/forecast-panel/reasons.ts`, `src/forecast-panel/trainRewrite.ts` | No OpenSearch or Postgres panel exists in the sandbox dashboards and no non-Druid target either; the Prometheus branch is live (F13). |
| `Dashboard must be saved before alerts can be added.` | `src/forecast-panel/alertFromPanel.ts:6` | The positive (`/alerting/new` with defaults) was verified; the unsaved-dashboard case needs a brand-new unsaved dashboard with the panel. Pass 4 attempted it and stopped at Grafana 13's add-panel flow (`Add` → a layout picker → `Add new element` never became reachable under automation), so the row stays source-verified. |
| Scheduler auto-disable after repeated 401/403 | `pkg/plugin/retrain.go` (`errGrafanaUnauthorized`) | Needs three consecutive rejected fetches across cron ticks; the Compose sandbox uses anonymous Admin and the token path was only exercised in its working (empty-token) form. |
| `Copy source from query A` (Forecast editor) | `src/forecast-datasource/` | Opt-in, manual feature; the Forecast editor was not driven by hand. |
| `DRUID_MAX_INFLIGHT` saturation | `timeseries-baselines/druid.go`, `timeseries-baselines/limits.go` | `DRUID_MAX_RPS` was verified; the inflight cap was not driven to saturation. |
| `FORECAST_MAX_INFLIGHT` env/ini precedence | `pkg/plugin/limits.go` | Only the jsonData path was used (the Compose env does not set it). |
| The CI/CD `forecast.ini.template` merge | `conf/forecast.ini.template` | Neither test environment merges the ini; both configure through jsonData/ConfigMaps. |
| The **plugin** scheduler's lease reclaim, `Extend` skip and tick counters under a mid-fit kill | `pkg/plugin/retrain.go` (`retrainOne`), `pkg/plugin/schedule.go` | Pass 7 measured the identical protocol live on the worker side (Pass 7's SIGKILL/SIGTERM run) and pinned it in `TestPostgresReclaimsAnExpiredLease`, `TestRetrainOneClaimLostSkipsTheRow` and `TestRetryDelay`, but the Compose Grafana was down, so no `gpx_forecast` process was killed mid-retrain this pass. |
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
| the pre-flight budget, `7448576` | *(did not exist: the budget was the two arrays alone)* | **200** — a legal 100,000-point fit carrying a 1 MiB `trainSource` decodes and fits; one byte over (`7448577`) → **413** `forecast: request body too large for a legal training series` (pass 4, Compose and Kubernetes alike) |
| 15 MiB | 400 `forecast: series is empty` | **413** `forecast: request body too large for a legal training series` — above the pre-flight budget (pass 3 re-measured a 14.23 MB body; pass 4, 15,728,640 bytes) |
| 16 MiB | 500 `{"statusCode":500,"messageId":"plugin.requestFailureError",…}` | **413**, and the reason is the pre-flight's rather than the body cap's: 16,777,216 bytes is flat above the 7,448,576-byte budget, so the handler refuses it before the decoder (pass 4) |
| 16.5 MiB, 17 MiB, 20 MiB | 500 | **413** `forecast: request body too large` — above the 16 MiB body cap, below the 32 MiB transport ceiling (pass 3 re-measured 17.31 MB; pass 4, 17,301,504 / 17,825,792 / 20,971,520 bytes) |
| 33 MiB and above | 500 | 500 — above the 32 MiB transport ceiling, refused by the SDK and not by the plugin; the reason is explicit: `grpc: received message larger than max (37151257 vs. 33554432)` (pass 3, a 37.15 MB body; pass 4 re-measured 38,797,312 bytes — the process stayed up, `restarts=0 oom=false`) |
| a decoded `trainSource` above 1 MiB | *(bounded only by the body cap)* | **413** `forecast: trainSource is larger than 1048576 bytes`, on a fit and on a probe alike (pass 4) |

The same sweep on the Kubernetes release over `http://localhost:80` answers 413 for 16, 16.5, 17 and 20 MiB
(pass 3); pass 4 re-measured the budget boundary there too (`7448576` → 200, `7448577` → the pre-flight 413), and a
15 MiB body sent `Transfer-Encoding: chunked` — the F2 note above — answers the same pre-flight 413 on both
environments.

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

The `F` ids in this table are the audit's **findings** in `audit/AUDIT.md`, not the feature ids used above: the two
series share the `F` prefix by coincidence (findings `F1…F60`, features `F1…F34`). Read a *Finding* cell as a
finding; read a `### F…` heading as a feature.

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
| **F25/F26/F27/F28** | the store test creates the queue table so the SQL runs; an old `(scope, key)` table is named once; ownership is computed once per span; the publish comment credits the Kafka key | `timeseries-baselines/store_test.go`'s `TestPostgresRetrainQueue` and `TestPostgresScheduleStaleKey` pass against the sandbox store (no skip) |
| **F29/F30/F31** | `JoinLeft`'s sharing is documented and pinned (the accessors copy); `FromPoints` allocates twice instead of three times; the unreachable resample branch is gone | library suites green; the allocation test reports three allocations against the old code |
| **F32-F44, F47** | the chart exposes the nine worker knobs, `postgres.sslMode`, default Grafana resources (2Gi limit) and a DSN-aware `postgres.url`; the store password moved to a Secret; the pod rolls on `retrainCron`/`pluginToken` through a checksum env plus the documented `grafana.configRevision` lever; plugin versions are pinned; the sandbox tags images by pin, imports them into the kind node and no longer shares one dashboards ConfigMap | rendered proofs for each value; `make check-pins` reports both pins at the sibling heads; the cluster shows the Secret, `FORECAST_CONFIG_CHECKSUM=e80beffb…`, `limits.memory=2Gi`; every Compose datasource still reports OK after the pinned preinstall |
| **F46** a panel's old key kept retraining forever | `superseded_at`: a fit stamps the same dashboard panel's other keys and clears its own | switching panel 1 to Last 7 days stamped `2e827911` (`superseded=12:20:43`), switching back to Auto cleared it and stamped the 7-day key; a superseded row is never claimed |
| **D1-D10** | the ten documentation corrections the pass-2 report's own table lists (`audit/AUDIT-2026-09-22-pass2.md`; the items were never renumbered into the sections above) | each was re-checked against the code before the edit |

Every row the pass-2 record left unit-tested only — and the rollout lever it recorded as not exercised — was
driven live in pass 3 (2026-09-22) on the Compose stack and the Kubernetes release:

| Row | Live observation |
| --- | --- |
| F12 a target with no `datasource` field | a throwaway dashboard whose query A omits `datasource` trained from the panel's own datasource and drew history + forecast (`POST …/forecast` 200, "Using saved model"); the copy was deleted afterwards. Pass 5 repeated it and settled *why* it works: Grafana supplies the panel's datasource on every target it sends, so the panel never needs a fallback of its own — its `panelDatasource` parameter was dead weight and was deleted (`trainQuery.ts`), and a target that still arrives bare is reported as `Training query returned no points` rather than resolved against the org default |
| F13 *Legacy lookback* | the option exists as a panel option (empty = Auto) **and** as a field in the Forecast query editor (`Explore`, datasource `forecast`: *Train from*, *Train to*, *Legacy lookback*); it is part of the `cacheKey` fingerprint (`src/forecast-panel/cacheKey.ts`), which is why the editor's tooltip tells you to keep it equal to the panel's |
| F14 a Mixed panel with a reduce row | **New alert rule** navigated to `/alerting/new` with a `defaults` payload carrying both rows: the Druid metric (query A) and the reduce expression (`refId B`, `datasourceUid __expr__`, `queryType expression`, `model.reducer mean`, `model.expression A`) |
| F15 two option edits in one editing session | *Show prediction interval* off plus *Max in-flight loads* 2 applied without an intermediate save; the save dialog's diff listed `"maxInflightLoads": 2`, `"showInterval": false` and the clamped `"interval": 0.99`, the saved dashboard (version 41) carries all three, and the band left the drawn panel at the same time |
| F21 *Interval coverage* bounds | typing `5` into the field clamps to `0.99` (`COVERAGE_SETTINGS.max`) |
| the `postgres.*`-only rollout lever | `helm upgrade --install timeseries … --set grafana.configRevision=$(date +%s)` created a new ReplicaSet and rolled the Grafana pod (`timeseries-grafana-7f89fd5cb-nr9zw`, 12:55:59) with a new `FORECAST_CONFIG_CHECKSUM=9d2aced3…` (was `e80beffb…`) |

### Follow-up pass (2026-09-22, after the pass-3 report)

`audit/AUDIT.md`'s pass-3 findings were closed, and the plugin's configuration UI lost its store representation
entirely. The chart pins moved with the code:

| Repo | Commits |
| --- | --- |
| `timeseries-forecast` | `v0.5.1` at `ec7c534` — released so that the `timeseries v0.1.1` require is on the tagged line |
| `timeseries-grafana` | `861d25d` (the audit fixes and the store out of the app configuration page), `12b6381` (the store explanation dropped from the page), `c4d09b9` (the pass-4 findings: the derived `trainSource` 413 message, the `ConfigEditor` key-list cases, the store test's cleanup), `84ad9b6` (require `timeseries-forecast v0.5.1`) |
| `timeseries-baselines` | `a82e0f3` (require `timeseries-forecast v0.5.1`) |
| `timeseries-k8s` | `b175732` (`PLUGIN_REF=861d25d…`), `64ae2f5` (`PLUGIN_REF=12b6381e2092…`), `606fdb1` (`PLUGIN_REF=84ad9b61b1b5…`, `BASELINES_REF=a82e0f30c79d…`, both pins at the sibling heads) |

Pass 4's own findings were closed the same day, and all three were hygiene rather than behaviour: the 413 message
for the replay payload is built from `maxTrainSourceBytes` instead of spelling the number, so the number it reports
cannot drift from the one it enforces; the boundary subtest pins that literal as the documented contract; the
Forecast datasource's `ConfigEditor` grew the table-driven key-list cases it lacked; and `TestPostgresStore`
deletes its synthetic `cccc…` snapshot, so a suite run against a shared store leaves no row behind. No request, no
response and no rendered page changed. Both stacks were then refreshed onto those commits: Compose mounts the
dist built at `84ad9b6` (`gpx_forecast_linux_amd64` sha256 `e9926f53…`, identical inside the container) with the
worker's `bin/baselines` `f4faf759…`, and the Kubernetes release runs `…-grafana:84ad9b61b1b5` and
`…-baselines:a82e0f30c79d`.

What the plugin pages render now: `?page=configuration` has exactly two fields (*Cron*, *Timezone*) and prints no
store key, no store field and no explanation of the store's absence; a save posts only
`jsonData.retrainCron`/`jsonData.retrainTimezone`, merged over the existing jsonData, with **no** `secureJsonData`
at all, so neither a provisioned store value nor the store password can be touched from the page (verified live on
both environments: after a real Save the five provisioned `store*` keys were byte-identical and
`secureJsonFields.storePassword` still `true`). The Forecast datasource's editor still lists the store **keys** it
finds in its own jsonData and never a value — pass 4 gave it a `storeUrl` with a password in it and the rendered
page contained neither the DSN nor the password. The body pre-flight is `2 × 100000 × 32 B + 1 MiB = 7448576` and a
decoded `trainSource` above 1 MiB has its own 413 reason (F2/F50 above); `timeseries-forecast/go.mod` requires
`timeseries v0.1.1`, and since `v0.5.1` both consumers require that released line instead of the `v0.5.0` tag that
predated the bump.

### Pass 5 (2026-09-27) — the v13 schema, v14 retention and the audit's fixes, measured on Compose

Pass 5 re-measured **Compose** on the current head; the Kubernetes release was **not** re-measured, so its rows
above remain pass 4's. The build is the workspace `dist/` from head `28916fe` **plus** the working tree this pass
carried at measure time (that tree became `c8339fe`; the measurement itself predates the commit), hash for hash in
the Plugin-build row.

**The schema is versioned and every primary key is a uuid (v13)** — F33 carries the transcripts:
`nothing to apply (2 known, 2 applied)` against the deployed database from both the host CLI and the shipped linux
artifact inside the container; `pending 0001_snapshots` / `pending 0002_retrain` under `--dry-run` on a scratch
database with `to_regnamespace('forecast')` still false; then `applied 0001_snapshots` / `applied 0002_retrain`,
then `nothing to apply`, with `snapshots_pkey PRIMARY KEY (id)`, `snapshots_org_cache_key_unique UNIQUE (org_id,
cache_key)`, `retrain_pkey PRIMARY KEY (id)`, `retrain_scope_org_key_unique UNIQUE (scope, org_id, key)` and
`id uuid DEFAULT gen_random_uuid()` on all three tables.

**Retention and the schedule reconcile (v14)** were driven live with a temporary `snapshotTtl: 1h` (reverted
afterwards; the deployed default is `72h`), on three planted fixtures plus the environment's own stale pair — the
tick lines and the outcomes are in F5/F16. In short: two stale snapshots and two idle rows collected (one of them
the four-day-idle *superseded* key `2e827911…` pass 3 had left behind), one row re-created for an orphan snapshot,
a fresh snapshot kept with an admin's cron, `drop=row` leaving the model and getting the row back, `drop=model`
removing both with nothing coming back.

**The panel and the datasource** were exercised in a throwaway Mixed dashboard (two Prometheus targets plus one
Forecast datasource query), deleted afterwards:

- a panel whose `targets[0]` omits `datasource` trained and drew history + forecast (probe 347 B, fit 2 985 B with
  `trainSource`), which answers the pass-3 question behind the F12 row below: **Grafana supplies the panel's
  datasource on every target it sends**, so the panel's own fallback was dead weight and was deleted
  (`trainQuery.ts`);
- *Copy source from query A* — the one item pass 4 recorded as unexercised — copied **both** metric siblings into
  `sourceTargets`, and the datasource query then carried
  `cacheKey 25985f9e4577d19c4b4e1da348dce6d916b826e5412482f6acf68eaf67759474`, byte-identical to the key the panel
  had stored for the series `metrics-exporter:8000`; `POST /api/ds/query` with that key restored the snapshot
  (`status 200`, one frame with real values) where an unknown key still answered `needTrain: train on the Forecast
  overlay panel first` — the v9 Mixed path and F7's contract;
- the app landing page (`/a/eduardkolotushin-forecast-app`) rendered the plugin's own copy and issued no
  `/resources/…` request — F34.

**Still not live-verified:** the pass-1 rows in the *Verified live, not verified live* table above (the OpenSearch and
Postgres train rejections, the unsaved-dashboard reason (pass 4 tried to automate it and Grafana 13's add-panel
flow would not co-operate), the credential auto-disable, `DRUID_MAX_INFLIGHT` saturation, the
`FORECAST_MAX_INFLIGHT` env precedence), and the 30 s cache-staleness window that the HA table already names.
Pass 5's own limits: the sweep was observed under a **temporary** `snapshotTtl: 1h` (so the `72h` default was never
waited out, and the collection of a *superseded* row is that short window's consequence), the Kubernetes
environment was not re-measured, and the build measured here is the working tree that became `c8339fe` — `dist/`
was rebuilt from it before the commit, so the hashes above describe that build, not an earlier release.

### Pass 6 (2026-09-27) — the audit's fixes, measured on Compose and Kubernetes against the head this pass published

Pass 6 re-measured **Compose** against the pushed head `5b50446` with the `dist/` built from it:
`gpx_forecast_linux_amd64` sha256 `104cd6c4b47f0ae6a3812155b8f11dfde1c0dec1bcd8d78df2b1f2d525401e83` (identical to
the `forecast-datasource/` copy), `gpx_forecast_migrate_linux_amd64`
`d2c5825973c2d62f88a5c96e1711fae83bfc79948675f72128e48c59df7ad5f1`, `module.js`
`c73acf8abe18db252b563881a223f690d07b1d3303787c6c02a0427bd5dc5277`, `forecast.ini.template`
`a42a4974ddf54f68b66643d8721cde3a4191317e168b03219bf9b22250a28bf2` — on Grafana 13.1.0 with the plugin's own store in
`overlay-postgres`. The one live surface this pass changes is the supersede key set; the panel path that serves it was
measured on a throwaway two-series TestData dashboard (created through the dashboard API, **deleted afterwards**,
with its rows and snapshots).

**The panel names its whole key set, so a two-series panel stays retrainable (G1/W8).** A panel drawing two series
(`scenarioId: random_walk`, `seriesCount: 2`) sent four `/resources/forecast` POSTs on one load, captured from the
page: two probes (no `times`, no `panelKeys`, `cacheKey e5f8a3…` / `cc4af2…`) and then two fits — each carrying
`panelKeys` of **length 2** (`[e5f8a3…, cc4af2…]`), `trainSource.seriesName` `A-series` / `A-series1` and the
`provenance` object. Both rows those fits wrote were current (`superseded_at IS NULL`) afterwards; the row keys this
sentence first quoted came from a different run of the same dashboard, so the correction below re-measures the panel
and quotes one run's keys end to end.
Before this pass's fix the second fit would have stamped the first series' row, leaving only the series
drawn last claimable — and flipping which one that was on every load. The same contract at the route level, with no
panel: a fit for a second key under one `dashboardUid`+`panelId` and carrying `panelKeys: [k1, k2]` left **both**
rows current and the row count unchanged (`a1…|t, b1…|t`, three rows), while a fit that omits the field still
retired the panel's other rows (`a1…|f, b1…|f, c1…|t`) — the pre-pass behaviour preserved exactly for an older
frontend.

**A deletion is visible to the deleting process at once (G4).** Against one key: the fit stored the snapshot
(`snapshots c=1`), a probe answered `{"times":[1700000240000,1700000300000],"values":[4,4],"cached":true}`, a second
probe answered from this process's own cache, `DELETE /schedules?scope=panel&key=…&drop=model` answered
`{"message":"ok"}` — and the next probe answered `{"needTrain":true}` with `snapshots c=0`. The row's `next_run_at`
was set an hour ahead before the probes, so the scheduler's `needTrain`-when-due rule cannot be what answered: the
change is purely the cached copy the deletion now drops, which without it would have served the deleted forecast for
up to `snapshotCacheTTL` = 30 s.

**The Holt band matches the corrected coefficient (F1).** `model: holt`, `alpha: 1`, `beta: 1` over `1,2,2,5`
(`sse = 10`, three residuals, σ = √(10/3)) with `level: 0.95` returned exactly the points `8, 11, 14` and the bands
`[4.421611712565687, 11.578388287434313]`, `[2.9984805394078187, 19.00151946059218]`,
`[0.610897031576048, 27.389102968423952]` — `α = β = 1`, the one point where every candidate trace coincides, with
the h=3 width `26.778205937 = 2·z·σ·√14` (the correction below shows the coefficient was still wrong for the
panel's own defaults). The pre-fix coefficient put `√11` there (`23.73`); `TestHoltInterval` pins the
`h = 3, α = β = 1 → 14σ²` trace, and after the review it pins the interval against an impulse rollout of the
recursion at `α = 0.8, β = 0.2` as well, because `α = β = 1` alone cannot tell the candidate traces apart.

**The library and worker fixes were shown failing first, then green.** `Interpolate`'s step branch returned the last
observation past the range end (now NaN, and NaN before the first point), `RegularGrid`'s pre-size grew to `n+1`
(now clamped to `1<<20` with the full grid still returned — `cap=1048577` immediately failed the new case before the
fix), the Holt trace above, and `ceilDuration`'s ceiling wrapped `int64` in the step-wide band below `Sub`'s
saturation, so `windowK` emitted a 106751-point grid instead of the single point the caller asked for
(`TestWindowKCap`'s new row: `k0, k1 = 1, 106751, want 106751, 106751` before the fix). The worker's pg-gated suite
ran against `overlay-postgres` — the sweep now passes its `Duration` straight to `::interval`
(`TestPostgresSweepSnapshots` green) and `pruneFits` releases all three per-hash maps — and the plugin's suite ran
against the same database, including a two-key `Supersede` case that failed before the statement took a key array.

**Kubernetes re-measured (same day, the release this pass pinned).** `make helm-up` installed the six releases on
the kind node from images built *by the pin*: `…-grafana:5b50446910f4` and `…-baselines:fe0c1cb4bd8f` — the
twelve-character prefixes of `5b50446910f4f4ba7dc6466ea14fd025f77f567c` and
`fe0c1cb4bd8fa204bd68fe95d8342e2c2e3b5175`, so the running pods carry exactly those pinned commits — the plugin
commit this pass pushed, `5b50446`; the documentation below travels in the repo, not in the image. (The release has
since been re-pinned to the review's commits; the correction at the end of this block measures that image.)
Grafana 13.1.0 answered `/api/health` at `http://localhost:80` (`commit b309c9bb…`, LoadBalancer `172.18.0.5`),
`GET /resources/ping` returned `{"message":"ok"}`, and the same Holt fit the Compose half used returned the same
three points and the same bands (`[4.421611712565687, 2.9984805394078187, 0.610897031576048]` …
`[11.578388287434313, 19.00151946059218, 27.389102968423952]`) — the `α = β = 1` trace on the Kubernetes image too,
the same case the Compose half used. The Helm-provisioned **Forecast minute-of-week demo** (from the `timeseries-overlay-dashboards`
ConfigMap) rendered its three panels (three canvases, no `role="alert"`, i.e. no reason text), and the
`/resources/forecast` traffic it sent was captured from the page: three probes without `panelKeys`
(`cacheKey 2e8279…`, `f8046f…`, `7713db…` — the same fingerprints the Compose run of the same dashboard mints) and
then three fits, each with `panelKeys` of length **1**, which is right for a one-series panel and is exactly what
distinguishes it from the two-series case in the Compose half above.

The plugin's files **inside the pod** hash to `gpx_forecast_linux_amd64`
`fcdb8f2de356052faefab80fe8ef9d07c87ab50b771b8fa71fc203429f49e948`, `module.js`
`07a1b24747a6b133ae3b8db7ed95f8ce36f8c4f3038a1cfefe4f4ae94ca253a2`, `forecast-datasource/module.js`
`7a2757b818af36bdc3edaaa1423ab8347d2a6f8402f54567ea6c5069aa1b91df` and `forecast-panel/module.js`
`df0642b9d77b9cb18e7d520039243f0c3ddab99c1b2aae3a84f7debff5f8c0b1`. Those are **not** byte-identical to the host
`dist/` hashes above, and that is expected: the image compiles the pinned source inside its own containers — node 22
against this host's node 24, its own Go toolchain, and a source tarball that has no `.git`, so the binary carries no
`vcs.revision` stamp at all — while the Compose numbers describe the workspace build. What identifies the pod's
source is therefore the image tag, not a hash comparison. `forecast.ini.template` is absent there because the
image's own build steps do not run `make ini-template`; only `make build`, which the sandbox path uses, copies it.

**Pass 6's limits:** the Compose Druid broker never finished starting while those images were building (its entrypoint
was still writing `runtime.properties` ten minutes in), so the *provisioned* Druid-backed dashboards were not
re-measured on Compose this time — the Kubernetes paragraph above measures one of them, and every Compose
measurement in this block is Druid-free by construction (a TestData panel, route-level fits, one pure-data Holt fit).
The `SNAPSHOT_TTL` sweep was not re-driven (pass 5 covered it), the sandbox's `VPS.txt` and its uncommitted `xtunnel`
service were left exactly as they were, and the retired-name and scope-gate corrections are documentation-only.

**Correction (2026-09-27, after an independent review of this pass).** The review found the Holt coefficient this
pass shipped wrong for every `α < 1`: `se` computed the trace of the recursion whose trend update is
`b_t = b_{t−1} + β·e_t`, while `holt.go` moves the trend by the level increment it just applied — `b_t = b_{t−1} +
αβ·e_t` — whose trace is `1 + α²(h−1)(1 + βh + β²·h(2h−1)/6)`. `α = β = 1`, the only point the numbers above
measured, is the one point where the two coincide, so it hid the error; at the panel's own defaults the shipped
trace was `32.56σ²` at `h = 10` against the recursion's `25.58σ²` (Monte-Carlo over 2·10⁶ paths: `25.574`), i.e.
bands ~13% too wide — *wider* than the pre-fix code, not narrower. `timeseries-forecast v0.5.3` (commit `4f1a7ea`)
fixes it, the plugin's `go.mod` moved to it in `24ac89f`, and `TestHoltInterval` now pins the interval against an
impulse rollout of `holt.go` — the `α = 0.8, β = 0.2` case compares width *ratios* between horizons, which cancels
σ, and fails against v0.5.2 (`h = 2/h = 1` = 1.4142 = √2 against the recursion's 1.3862).

Re-measured on the same Compose stack, `dist/` from `24ac89f`, `model: holt`, `alpha: 0.8`, `beta: 0.2`,
`level: 0.95` over `1,2,2,5`: points `5.7616, 6.9152, 8.0688`; `lower` `[3.271699933312405, 3.4636560178723124,
3.6314655480479416]`; `upper` `[8.251500066687594, 10.366743982127687, 12.506134451952057]`. Hand-checked against
the recursion: `σ = √(4.8416/3) = 1.270287`, trace `1, 1.9216, 3.176`, so `h = 1` is `5.7616 ± z·σ` and `h = 3` is
`8.0688 ± z·σ·√3.176`. The `α = β = 1` numbers above remain the case where the two traces agree.

The panel key set was re-measured end to end on that build, through the panel's own **Retrain** button so the fits
ran with the rows in place: the two fits carried `cacheKey`/`panelKeys` `b3d1578f…` (A-series) and `5cc80279…`
(A-series1), each naming **both** keys, and `forecast.retrain` then held rows keyed `b3d1578f…` and `5cc80279…`,
both `superseded_at IS NULL`; the panel's earlier key `112b7f70…`, left by a run whose two series shared one
fingerprint, was stamped. A hand-made request with 65 keys answered `400 forecast: panelKeys holds more than 64
keys` and one with 64 answered `200`; the overlay sends no set at all for a panel wider than the cap, since a
partial one would retire the series it left out on every load.

The same review fixed three more surfaces this pass touched: the retention sweep no longer collects a row another
replica has claimed (`TestSweepKeepsAClaimedRow`, which collected both rows before the guard), `snapshotKeySQL` now
requires the `org_id` column in the key it accepts (a table keyed `(cache_key, snapshot)` passed readiness and then
answered every `Put` with `42P10`; `TestSnapshotProbeRequiresTheOrgIDColumn`), and `memSchedules.Due` mirrors the
production predicate's `superseded_at IS NULL`. In `timeseries-baselines` (commit `467421f`) the schema probe now
checks every table rather than one — a half-applied pair of migration files no longer reads as provisioned
(`TestProbeSchemaChecksEveryTable`) — `SNAPSHOT_TTL` is verified against `DEFAULT_RETRAIN_CRON`'s next gap (a 1h
window beside the daily default collects a healthy metric's snapshot and publishes nothing until the next retrain),
and the sweep's correlation names the fleet-wide `org_id = 0`.

**Kubernetes re-measured on the re-pinned release.** `make helm-images helm-refresh` rebuilt both images from the
new pins (`ghcr.io/eduard-kolotushin/timeseries-grafana:24ac89f17b84`,
`…/timeseries-baselines:467421fa4af7`) and rolled the pods onto them; `ghcr.io/eduard-kolotushin/timeseries-k8s`
commit `9cc14da` carries the pins. Grafana 13.1.0 answered `/api/health` and `/resources/ping` on
`http://localhost:80`, the `alpha: 0.8`, `beta: 0.2` fit returned the same three points and the same bands as the
Compose half (`5.7616, 6.9152, 8.0688`; `[3.271699933312405, 3.4636560178723124, 3.6314655480479416]` …
`[8.251500066687594, 10.366743982127687, 12.506134451952057]`), and a 65-key request answered
`400 forecast: panelKeys holds more than 64 keys` there too — the two surfaces this correction changes, measured on
the image rather than only in the workspace. The ConfigMap-provisioned **Forecast minute-of-week demo** rendered its
three panels (three canvases, no `role="alert"`); its three probes all hit saved models (the fingerprints are
deterministic, and the K8s store already held them), so the first panel was refitted through its own **Retrain**
button to exercise the fit path: one fit, `cacheKey 2e8279…`, `panelKeys` of length **1** — right for a one-series
panel, and the same shape the Compose half distinguishes from the two-series case.

The pod's artifacts hash to `gpx_forecast_linux_amd64`
`eb07112cf625b4b9fcf6b5be2b272cf9b72c1b4c6e22b88791a4aa7bf67b81d8`, `module.js`
`07a1b24747a6b133ae3b8db7ed95f8ce36f8c4f3038a1cfefe4f4ae94ca253a2`, `forecast-datasource/module.js`
`7a2757b818af36bdc3edaaa1423ab8347d2a6f8402f54567ea6c5069aa1b91df` and `forecast-panel/module.js`
`68920cb5a18908dd7d5fb6982a292b0d3c91cef240e3b72ad7850e8827883e16`. Only the binary and the **panel** bundle
moved from the pass-6 pin (`fcdb8f2d…` → `eb07112c…`, `df0642b9…` → `68920cb5…`); the app and datasource bundles are
byte-identical because the review changed only `src/forecast-panel` and `pkg/`, which is what the change set
predicts and a check that the image really is this source.


### Pass 7 (2026-10-09) — retrain reliability (v15 / worker v6), measured on Compose with two real worker processes

Pass 7 verified the reliability contract (`docs/INTENTIONS.md` v15 in this repo, v6 in `timeseries-baselines`)
against the working tree, on the Compose stack's `overlay-postgres` and its Druid. The schema half was measured on a
database this pass created and dropped (`mcheck`): `FORECAST_STORE_URL=…/mcheck go run ./cmd/migrate` printed
`applied 0001_snapshots`, `applied 0002_retrain`, `applied 0003_retrain_attempts`; a second run printed
`nothing to apply (3 known, 3 applied)`; `select version, name from forecast.schema_migrations` listed the three
rows and `\d forecast.retrain` showed `attempts | integer | not null | 0`. Against a database whose ledger was
already complete the same statement was a no-op, which is the upgrade path of a deployment that ran the CLI before
this pass existed.

**The worker's half was measured with two real processes and a real SIGKILL.** The linux worker was built from the
tree (18,210,613 B) and run twice with `docker compose run` as `smoke-w1` / `smoke-w2` (`INTERVAL=10s`,
`RETRAIN_LEASE=40s`, `RETRAIN_RETRY=20s`, `RETRAIN_RETRY_MAX=5m`, `DRUID_MAX_RPS=1`, the rest from the Compose
service). Druid `metrics` held `ready` and `live` at 21,600 points each (`2026-09-24T18:02Z` → `2026-10-09T18:01Z`,
i.e. 15 days) and `short` at 4,320. On the first tick **both** `baseline` rows were claimed by `172.19.0.12`
(`smoke-w1`) with `claimed_until` 40 s out — the extension the contract adds — and `attempts 0` in flight. Killing
that container with `docker kill` (no signal handler, so only the lease can recover it) at `18:03:24` produced, in
the survivor's own logs:

```
18:04:17.407 ERROR msg=retrain metric_hash=live  err="step between the last two points is 2m0s, want 1m" attempts=1
18:04:18.406 ERROR msg=retrain metric_hash=ready err="step between the last two points is 2m0s, want 1m" attempts=1
18:04:18.409 INFO  msg="retrain tick" shard=172.19.0.13 claimed=2 retrained=0 failed=2
18:04:46.451 INFO  msg="retrain tick" shard=172.19.0.13 claimed=2 retrained=2 failed=0
```

— the two rows the dead worker held were re-claimed by `172.19.0.13` after the 40 s lease (the survivor was
ticking slowly because each tick spends ~30 s on its own fits), the first attempt recorded `attempts=1` and spaced
its retry by the base delay, and the second retrained both: `last_status ok`, `attempts` back to **0**,
`next_run_at 18:05:00` (the Compose `*/5 * * * *` cron) and `baselines.snapshots.updated_at` `18:04:45` / `18:04:46`.
The dead peer's heartbeat expiry moved the survivor's view from `peers=2 owned=2` to `peers=1 owned=3`, and the
retrain happened anyway — the claim is fleet-wide, exactly as v6 states.

**SIGTERM hands the claims back.** Stopped mid-fit (`docker stop smoke-w2`, the holder of both rows, `18:05:20`),
the cancelled tick logged `level=ERROR msg="retrain finish" metric_hash=… err="context canceled"` for both rows
(a failed finish deliberately does **not** unhold), and the shutdown release then wrote the row state
`claimed_by` NULL, `last_status error: interrupted`, `next_run_at` = the stop second (already due when read 4 s
later) with `attempts` still **0** — a rolling restart costs no lease-length wait, and a deployment is not counted
as the row's failure. Both smoke containers were removed afterwards.

**The plugin's half is covered by its gated tests, not by a live Grafana this pass.** With `FORECAST_TEST_PG` set,
`go test ./pkg/... ./cmd/...` ran with **0 skips** and the worker's suite likewise (`BASELINE_TEST_PG`), including
`TestPostgresReclaimsAnExpiredLease` (plugin) and `TestPostgresClaimReclaimsAnExpiredLease` (worker): a live lease
holds the fleet out, an expired one is re-claimed, `Extend`/`Finish` by the stale owner match zero rows, and the
survivor's finish is the one that lands with its own attempt count. `TestRetrainOneClaimLostSkipsTheRow` and
`TestPublisherSkipsARowWhoseClaimMovedOn` pin the skip (no fetch, no fit, no finish), `TestPublisherShutdownReleasesHeldClaims`
the release, and `TestRetryDelay` / `TestWorkLease` the curve and the derivation
(`max(RETRAIN_RETRY, ceil(LOOKBACK / DRUID_MAX_RANGE) × DRUID_TIMEOUT + 1m)` = 15 m at the sandbox shape and 6 m in
the plugin). The assertions were shown to bite: a flat `retryDelay`, an un-reset attempt counter and an
owner-unguarded `Extend` each failed their test in both repos before being reverted. **Not measured live this
pass:** the plugin scheduler's own kill/reclaim (it needs a Grafana process and `/api/org`, and the Compose Grafana
was down), and the Kubernetes path.
