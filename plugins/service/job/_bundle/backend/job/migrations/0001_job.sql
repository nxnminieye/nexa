CREATE TABLE job_schedules (
    id BIGSERIAL PRIMARY KEY,
    key TEXT NOT NULL UNIQUE,
    cron TEXT NOT NULL,
    task_id TEXT NOT NULL,
    payload BYTEA,
    enabled BOOLEAN NOT NULL DEFAULT TRUE
);

CREATE TABLE job_runs (
    id BIGSERIAL PRIMARY KEY,
    run_id TEXT NOT NULL UNIQUE,
    schedule_id TEXT,
    task_id TEXT NOT NULL,
    status TEXT NOT NULL,
    payload BYTEA,
    output BYTEA,
    error_code TEXT,
    started_at TIMESTAMPTZ NOT NULL,
    completed_at TIMESTAMPTZ
);

CREATE INDEX job_runs_schedule_id_idx ON job_runs (schedule_id);
CREATE INDEX job_runs_started_at_idx ON job_runs (started_at);

CREATE TABLE job_leases (
    id BIGSERIAL PRIMARY KEY,
    key TEXT NOT NULL UNIQUE,
    run_id TEXT NOT NULL,
    expires_at TIMESTAMPTZ NOT NULL
);

CREATE INDEX job_leases_expires_at_idx ON job_leases (expires_at);
