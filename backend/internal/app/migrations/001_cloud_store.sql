CREATE TABLE IF NOT EXISTS private_records_cloud_state (
    id smallint PRIMARY KEY CHECK (id = 1),
    payload bytea NOT NULL,
    version bigint NOT NULL DEFAULT 1 CHECK (version > 0)
);
