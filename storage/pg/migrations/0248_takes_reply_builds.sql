-- +goose Up

CREATE UNIQUE INDEX takes_reply_conversation_owner
    ON takes_reply_conversations (thread_id, user_id);

CREATE TABLE takes_reply_builds (
    id uuid PRIMARY KEY,
    thread_id text NOT NULL,
    user_id text NOT NULL,
    reply_revision integer NOT NULL CHECK (reply_revision >= 0),
    request_id uuid NOT NULL,
    selected_choice_id text NOT NULL,
    snapshot jsonb NOT NULL,
    snapshot_hash text NOT NULL CHECK (length(snapshot_hash) = 64),
    status text NOT NULL CHECK (status IN ('QUEUED', 'RUNNING', 'COMPLETED', 'PARTIAL', 'FAILED')),
    start_snapshot jsonb,
    completion_snapshot jsonb,
    proposal jsonb,
    assessment jsonb,
    missing_requirements jsonb NOT NULL DEFAULT '[]'::jsonb,
    failure_message text,
    created_at timestamptz NOT NULL,
    started_at timestamptz,
    completed_at timestamptz,
    lease_until timestamptz,
    CHECK ((status = 'RUNNING') = (lease_until IS NOT NULL)),
    FOREIGN KEY (thread_id, user_id) REFERENCES takes_reply_conversations (thread_id, user_id) ON DELETE CASCADE,
    UNIQUE (thread_id, reply_revision)
);

CREATE INDEX takes_reply_build_queue ON takes_reply_builds (created_at, id) WHERE status = 'QUEUED';
CREATE UNIQUE INDEX takes_reply_active_user_build ON takes_reply_builds (user_id) WHERE status IN ('QUEUED', 'RUNNING');

-- +goose StatementBegin
CREATE FUNCTION protect_takes_reply_build_input() RETURNS trigger AS $$
BEGIN
    IF ROW(NEW.id, NEW.thread_id, NEW.user_id, NEW.reply_revision, NEW.request_id,
           NEW.selected_choice_id, NEW.snapshot, NEW.snapshot_hash, NEW.created_at)
       IS DISTINCT FROM
       ROW(OLD.id, OLD.thread_id, OLD.user_id, OLD.reply_revision, OLD.request_id,
           OLD.selected_choice_id, OLD.snapshot, OLD.snapshot_hash, OLD.created_at) THEN
        RAISE EXCEPTION 'takes reply build input is immutable';
    END IF;
    IF OLD.start_snapshot IS NOT NULL AND NEW.start_snapshot IS DISTINCT FROM OLD.start_snapshot THEN
        RAISE EXCEPTION 'takes reply build start catalog is immutable';
    END IF;
    RETURN NEW;
END;
$$ LANGUAGE plpgsql;
-- +goose StatementEnd

CREATE TRIGGER takes_reply_build_input_immutable BEFORE UPDATE ON takes_reply_builds
    FOR EACH ROW EXECUTE FUNCTION protect_takes_reply_build_input();

-- +goose Down

DROP TABLE takes_reply_builds;
DROP FUNCTION protect_takes_reply_build_input();
DROP INDEX takes_reply_conversation_owner;
