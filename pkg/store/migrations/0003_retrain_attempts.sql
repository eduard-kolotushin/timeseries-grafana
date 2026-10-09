-- forecast.retrain.attempts: how many consecutive retrains of this row failed.
--
-- Both writers of this table (this plugin's scheduler and the baselines worker)
-- increment it on a failed retrain and reset it to 0 on a success, then use it to
-- space the retries: a failure is due again after
-- min(base * 2^(attempts-1), cap). Without it a broken row re-entered the queue at
-- the fixed base delay forever, so a Druid or Grafana overload produced a retry
-- storm that competed with the legitimate backlog instead of backing off.
--
-- DEFAULT 0 backfills every existing row (a row that has never been retried has
-- no failed attempts). IF NOT EXISTS keeps this idempotent for a deployment whose
-- table an older release created and for a re-run after a partial failure; the
-- engine also runs each file in one transaction, so a failure leaves neither the
-- column nor a ledger row.
ALTER TABLE forecast.retrain
  ADD COLUMN IF NOT EXISTS attempts INT NOT NULL DEFAULT 0;
