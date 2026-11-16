-- +goose Up

CREATE TABLE takes_reply_conversations (
    thread_id text PRIMARY KEY REFERENCES threads(id) ON DELETE CASCADE,
    user_id text NOT NULL,
    current_revision integer NOT NULL DEFAULT 0 CHECK (current_revision >= 0),
    draft_state jsonb NOT NULL,
    created_at timestamptz NOT NULL DEFAULT now(),
    updated_at timestamptz NOT NULL DEFAULT now()
);

CREATE TABLE takes_reply_requests (
    user_id text NOT NULL,
    operation text NOT NULL CHECK (operation IN ('CREATE', 'TURN')),
    request_id uuid NOT NULL,
    thread_id text NOT NULL REFERENCES threads(id) ON DELETE CASCADE,
    payload_hash text NOT NULL CHECK (length(payload_hash) = 64),
    status text NOT NULL CHECK (status IN ('RUNNING', 'COMPLETED', 'FAILED')),
    expected_revision integer NOT NULL DEFAULT 0 CHECK (expected_revision >= 0),
    attempt_id uuid NOT NULL,
    response jsonb,
    lease_until timestamptz,
    created_at timestamptz NOT NULL DEFAULT now(),
    completed_at timestamptz,
    PRIMARY KEY (user_id, operation, request_id)
);

CREATE INDEX takes_reply_active_turns
    ON takes_reply_requests (thread_id, lease_until)
    WHERE operation = 'TURN' AND status = 'RUNNING';

-- +goose Down

DROP TABLE takes_reply_requests;
DROP TABLE takes_reply_conversations;
