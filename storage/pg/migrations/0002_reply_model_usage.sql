-- +goose Up

-- This table records provider usage reported by completed responses. It is
-- observability only; this host performs no billing or credit accounting.
CREATE TABLE reply_model_usage (
    id uuid PRIMARY KEY,
    user_id text NOT NULL,
    thread_id text NOT NULL,
    completion_id uuid,
    purpose text NOT NULL,
    event_kind text NOT NULL CHECK (event_kind IN ('completion', 'unreported_attempt')),
    model text,
    prompt_tokens bigint,
    completion_tokens bigint,
    cached_tokens bigint,
    cache_write_tokens bigint,
    provider_cost_usd numeric,
    cost_reported boolean NOT NULL DEFAULT false,
    request_committed boolean,
    created_at timestamptz NOT NULL DEFAULT now()
);

CREATE INDEX reply_model_usage_owner_created_idx ON reply_model_usage(user_id, created_at DESC);
CREATE INDEX reply_model_usage_completion_idx ON reply_model_usage(completion_id) WHERE completion_id IS NOT NULL;

-- +goose Down

DROP TABLE reply_model_usage;
