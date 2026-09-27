-- forecast.retrain: the retrain queue. One row per trained panel (per org) and
-- one per worker-owned baseline hash.
--
-- The plugin owns this table outright: the baselines worker inserts, claims and
-- finishes its own rows here but never provisions them, so a deployment that runs
-- the worker without ever loading the plugin would not get the table.
--
-- It carries no secondary index on purpose: every plugin read is the
-- (scope, org_id, key) key, and the claim scan orders a table that stays in the
-- hundreds of rows — an index would only add write cost to each retrain.
--
-- org_id is part of the key because a panel's cache key is org-independent: the
-- same dashboard and datasource uids hash the same in two orgs, so a (scope, key)
-- key would put two orgs on one row, where either org's fit would rewrite the
-- other's cron and stored query objects. See scheduleKeySQL.
--
-- A migration file must stay transactional: the engine runs each file in one
-- transaction, so CREATE INDEX CONCURRENTLY and other non-transactional
-- statements do not belong here. Never edit a file that has been applied — add
-- a new one.
CREATE TABLE IF NOT EXISTS forecast.retrain (
  id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
  scope TEXT NOT NULL,                -- 'panel' | 'baseline'
  key TEXT NOT NULL,                  -- panel: cache_key, baseline: metric_hash
  org_id BIGINT NOT NULL DEFAULT 0,
  cron TEXT NOT NULL,
  timezone TEXT NOT NULL DEFAULT 'UTC',
  enabled BOOLEAN NOT NULL DEFAULT true,
  spec JSONB,                         -- panel: opaque datasource query objects; baseline: model spec
  next_run_at TIMESTAMPTZ,
  last_run_at TIMESTAMPTZ,
  last_status TEXT,
  claimed_by TEXT,
  claimed_until TIMESTAMPTZ,
  superseded_at TIMESTAMPTZ,          -- panel: the row's key is no longer the panel's current one
  updated_at TIMESTAMPTZ NOT NULL DEFAULT now(),
  CONSTRAINT retrain_scope_org_key_unique UNIQUE (scope, org_id, key)
);
-- A table created before supersede existed gets the column here; IF NOT EXISTS
-- makes it a no-op on the CREATE above and on every later run.
ALTER TABLE forecast.retrain ADD COLUMN IF NOT EXISTS superseded_at TIMESTAMPTZ;
-- The legacy-key block below widens the key to (scope, org_id, key), so the org
-- column has to exist first: a table old enough to be keyed (scope, key) may also
-- predate org_id entirely. Rows from that far back land at org 0, the fleet-wide
-- slot, rather than failing the migration.
ALTER TABLE forecast.retrain ADD COLUMN IF NOT EXISTS org_id BIGINT NOT NULL DEFAULT 0;
-- legacy-key: (scope, key) -> (scope, org_id, key)
DO $$
DECLARE pk_name TEXT; pk_cols TEXT;
BEGIN
  SELECT c.conname, string_agg(a.attname, ',' ORDER BY a.attnum)
    INTO pk_name, pk_cols
  FROM pg_constraint c
  JOIN pg_class t ON t.oid = c.conrelid
  JOIN pg_namespace n ON n.oid = t.relnamespace
  JOIN pg_attribute a ON a.attrelid = t.oid AND a.attnum = ANY (c.conkey)
  WHERE n.nspname = 'forecast' AND t.relname = 'retrain' AND c.contype = 'p'
  GROUP BY c.conname;
  IF pk_cols = 'scope,key' THEN
    -- A table created before org_id joined the key. Existing rows keep their
    -- org: a panel row was written with the org that fitted it.
    EXECUTE format('ALTER TABLE forecast.retrain DROP CONSTRAINT %I', pk_name);
    ALTER TABLE forecast.retrain ADD PRIMARY KEY (scope, org_id, key);
  END IF;
END $$;
-- uuid-key: surrogate primary key; the natural key keeps its uniqueness
ALTER TABLE forecast.retrain ADD COLUMN IF NOT EXISTS id UUID;
UPDATE forecast.retrain SET id = gen_random_uuid() WHERE id IS NULL;
ALTER TABLE forecast.retrain ALTER COLUMN id SET DEFAULT gen_random_uuid();
DO $$
BEGIN
  IF NOT EXISTS (
    SELECT 1
    FROM pg_constraint c
    JOIN pg_class t ON t.oid = c.conrelid
    JOIN pg_namespace n ON n.oid = t.relnamespace
    WHERE n.nspname = 'forecast' AND t.relname = 'retrain' AND c.contype = 'u'
      AND array_length(c.conkey, 1) = 3
      AND c.conkey @> ARRAY[(SELECT a.attnum FROM pg_attribute a WHERE a.attrelid = t.oid AND a.attname = 'scope')]
      AND c.conkey @> ARRAY[(SELECT a.attnum FROM pg_attribute a WHERE a.attrelid = t.oid AND a.attname = 'org_id')]
      AND c.conkey @> ARRAY[(SELECT a.attnum FROM pg_attribute a WHERE a.attrelid = t.oid AND a.attname = 'key')]
  ) THEN
    ALTER TABLE forecast.retrain ADD CONSTRAINT retrain_scope_org_key_unique UNIQUE (scope, org_id, key);
  END IF;
END $$;
DO $$
DECLARE pk_name TEXT; pk_cols TEXT;
BEGIN
  SELECT c.conname, string_agg(a.attname, ',' ORDER BY a.attnum)
    INTO pk_name, pk_cols
  FROM pg_constraint c
  JOIN pg_class t ON t.oid = c.conrelid
  JOIN pg_namespace n ON n.oid = t.relnamespace
  JOIN pg_attribute a ON a.attrelid = t.oid AND a.attnum = ANY (c.conkey)
  WHERE n.nspname = 'forecast' AND t.relname = 'retrain' AND c.contype = 'p'
  GROUP BY c.conname;
  IF pk_cols IS NULL THEN
    ALTER TABLE forecast.retrain ADD PRIMARY KEY (id);
  ELSIF pk_cols <> 'id' THEN
    EXECUTE format('ALTER TABLE forecast.retrain DROP CONSTRAINT %I', pk_name);
    ALTER TABLE forecast.retrain ADD PRIMARY KEY (id);
  END IF;
END $$;
