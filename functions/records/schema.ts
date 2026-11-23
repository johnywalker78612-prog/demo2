// Additive, versioned schema; existing Go records are never imported implicitly.
export const migration = `
CREATE SCHEMA IF NOT EXISTS private_records;
CREATE TABLE IF NOT EXISTS private_records.people (
 id text PRIMARY KEY,
 data jsonb NOT NULL CHECK (jsonb_typeof(data) = 'object'),
 photo_key text,
 photo_type text,
 created_at timestamptz NOT NULL DEFAULT now(),
 updated_at timestamptz NOT NULL DEFAULT now()
);
CREATE TABLE IF NOT EXISTS private_records.storage_cleanup (
 object_key text PRIMARY KEY, created_at timestamptz NOT NULL DEFAULT now()
);
CREATE TABLE IF NOT EXISTS private_records.migrations (version integer PRIMARY KEY, applied_at timestamptz NOT NULL DEFAULT now());
INSERT INTO private_records.migrations(version) VALUES (1) ON CONFLICT DO NOTHING;
`;
