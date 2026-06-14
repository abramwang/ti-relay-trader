-- Persist daily trading workflow job runs for status, cron, and recovery.

CREATE TABLE job_runs (
    job_run_pk BIGSERIAL PRIMARY KEY,
    run_id TEXT NOT NULL,
    job_name TEXT NOT NULL,
    trade_date DATE NOT NULL,
    timezone TEXT NOT NULL DEFAULT 'Asia/Shanghai',
    status TEXT NOT NULL,
    trigger TEXT,
    skipped BOOLEAN NOT NULL DEFAULT FALSE,
    started_at TIMESTAMPTZ,
    finished_at TIMESTAMPTZ,
    duration_ms BIGINT NOT NULL DEFAULT 0 CHECK (duration_ms >= 0),
    report_json JSONB NOT NULL DEFAULT '{}'::jsonb,
    error_summary TEXT,
    created_at TIMESTAMPTZ NOT NULL DEFAULT now(),
    updated_at TIMESTAMPTZ NOT NULL DEFAULT now(),
    CONSTRAINT job_runs_run_id_unique UNIQUE (run_id),
    CONSTRAINT job_runs_status_check CHECK (status IN ('running', 'succeeded', 'skipped', 'failed'))
);

CREATE INDEX job_runs_name_date_idx ON job_runs(job_name, trade_date DESC);
CREATE INDEX job_runs_status_idx ON job_runs(status, (COALESCE(finished_at, started_at, updated_at)) DESC);
