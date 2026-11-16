-- +goose Up

-- The source replies feature stores its conversations and ordered messages in
-- Axion's shared thread tables. This small parent schema gives the extracted
-- feature the same ownership and soft-delete checks in an isolated database.
CREATE TABLE threads (
    id text PRIMARY KEY,
    user_id text NOT NULL,
    title text NOT NULL DEFAULT 'New Take',
    metadata jsonb NOT NULL DEFAULT '{}'::jsonb,
    thread_kind text NOT NULL DEFAULT 'TAKES',
    deleted_at timestamptz,
    created_at timestamptz NOT NULL DEFAULT now()
);

CREATE INDEX threads_user_id_idx ON threads(user_id);

CREATE TABLE messages (
    id uuid PRIMARY KEY,
    thread_id text NOT NULL REFERENCES threads(id) ON DELETE CASCADE,
    role text NOT NULL CHECK (role IN ('user', 'assistant', 'system')),
    content text NOT NULL,
    metadata jsonb NOT NULL DEFAULT '{}'::jsonb,
    created_at timestamptz NOT NULL DEFAULT now()
);

CREATE INDEX messages_thread_created_idx ON messages(thread_id, created_at, id);

-- +goose Down

DROP TABLE messages;
DROP TABLE threads;
