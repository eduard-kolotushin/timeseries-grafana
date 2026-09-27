-- forecast.snapshots: one fitted snapshot per (org, cache key).
--
-- A migration file must stay transactional: the engine runs each file in one
-- transaction, so CREATE INDEX CONCURRENTLY and other non-transactional
-- statements do not belong here. Never edit a file that has been applied — add
-- a new one.
CREATE SCHEMA IF NOT EXISTS forecast;
CREATE TABLE IF NOT EXISTS forecast.snapshots (
  id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
  org_id BIGINT NOT NULL,
  cache_key CHAR(64) NOT NULL,
  snapshot JSONB NOT NULL,
  updated_at TIMESTAMPTZ NOT NULL DEFAULT now(),
  CONSTRAINT snapshots_org_cache_key_unique UNIQUE (org_id, cache_key)
);
-- Adopt a table created before the surrogate key. On the CREATE above ADD COLUMN
-- IF NOT EXISTS is a no-op; on an upgraded table it adds the column for the
-- backfill below.
ALTER TABLE forecast.snapshots ADD COLUMN IF NOT EXISTS id UUID;
UPDATE forecast.snapshots SET id = gen_random_uuid() WHERE id IS NULL;
ALTER TABLE forecast.snapshots ALTER COLUMN id SET DEFAULT gen_random_uuid();
-- The natural key must stay unique: the plugin's Put upserts ON CONFLICT
-- (org_id, cache_key). A pre-uuid table carries that uniqueness as its primary
-- key, which the swap below drops.
DO $$
BEGIN
  IF NOT EXISTS (
    SELECT 1
    FROM pg_constraint c
    JOIN pg_class t ON t.oid = c.conrelid
    JOIN pg_namespace n ON n.oid = t.relnamespace
    WHERE n.nspname = 'forecast' AND t.relname = 'snapshots' AND c.contype = 'u'
      AND array_length(c.conkey, 1) = 2
      AND c.conkey @> ARRAY[(SELECT a.attnum FROM pg_attribute a WHERE a.attrelid = t.oid AND a.attname = 'org_id')]
      AND c.conkey @> ARRAY[(SELECT a.attnum FROM pg_attribute a WHERE a.attrelid = t.oid AND a.attname = 'cache_key')]
  ) THEN
    ALTER TABLE forecast.snapshots ADD CONSTRAINT snapshots_org_cache_key_unique UNIQUE (org_id, cache_key);
  END IF;
END $$;
-- uuid-key: the primary key itself must be the uuid column.
DO $$
DECLARE pk_name TEXT; pk_cols TEXT;
BEGIN
  SELECT c.conname, string_agg(a.attname, ',' ORDER BY a.attnum)
    INTO pk_name, pk_cols
  FROM pg_constraint c
  JOIN pg_class t ON t.oid = c.conrelid
  JOIN pg_namespace n ON n.oid = t.relnamespace
  JOIN pg_attribute a ON a.attrelid = t.oid AND a.attnum = ANY (c.conkey)
  WHERE n.nspname = 'forecast' AND t.relname = 'snapshots' AND c.contype = 'p'
  GROUP BY c.conname;
  IF pk_cols IS NULL THEN
    ALTER TABLE forecast.snapshots ADD PRIMARY KEY (id);
  ELSIF pk_cols <> 'id' THEN
    EXECUTE format('ALTER TABLE forecast.snapshots DROP CONSTRAINT %I', pk_name);
    ALTER TABLE forecast.snapshots ADD PRIMARY KEY (id);
  END IF;
END $$;
