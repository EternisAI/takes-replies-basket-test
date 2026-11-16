-- name: GetConversation :one
SELECT c.thread_id::text AS thread_id, c.user_id, c.current_revision, c.draft_state,
       t.metadata
FROM takes_reply_conversations c
JOIN threads t ON t.id = c.thread_id AND t.user_id = c.user_id
WHERE c.thread_id = sqlc.arg('thread_id') AND c.user_id = sqlc.arg('user_id')
  AND t.deleted_at IS NULL;

-- Lock the owning thread with the conversation so a completed-request replay
-- cannot return reply data after a concurrent soft delete.
-- name: LockConversation :one
SELECT c.current_revision, c.draft_state, t.metadata
FROM takes_reply_conversations c
JOIN threads t ON t.id = c.thread_id AND t.user_id = c.user_id
WHERE c.thread_id = sqlc.arg('thread_id') AND c.user_id = sqlc.arg('user_id')
  AND t.deleted_at IS NULL
FOR UPDATE OF c, t;

-- name: GetMessages :many
-- Revision/order are the conversation sequence; created_at is only a tiebreaker.
WITH recent AS (
    SELECT id, role, content, created_at,
           COALESCE((metadata->>'takes_reply_revision')::int, 0) AS reply_revision,
           COALESCE((metadata->>'takes_reply_order')::int, 0) AS reply_order
    FROM messages
    WHERE thread_id = sqlc.arg('thread_id')
      AND metadata->>'takes_reply' = 'v1'
    ORDER BY COALESCE((metadata->>'takes_reply_revision')::int, 0) DESC,
             COALESCE((metadata->>'takes_reply_order')::int, 0) DESC,
             created_at DESC,
             id DESC
    LIMIT 101
)
SELECT id, role, content, created_at
FROM recent
ORDER BY reply_revision, reply_order, created_at, id;

-- name: GetRecentMessages :many
SELECT id, role, content, created_at
FROM messages
WHERE thread_id = sqlc.arg('thread_id')
  AND metadata->>'takes_reply' = 'v1'
ORDER BY COALESCE((metadata->>'takes_reply_revision')::int, 0) DESC,
         COALESCE((metadata->>'takes_reply_order')::int, 0) DESC,
         created_at DESC,
         id DESC
LIMIT 8;

-- name: GetRequestForUpdate :one
SELECT user_id, operation, request_id, thread_id::text AS thread_id, payload_hash,
       status, expected_revision, attempt_id, response, lease_until
FROM takes_reply_requests
WHERE user_id = sqlc.arg('user_id') AND operation = sqlc.arg('operation')
  AND request_id = sqlc.arg('request_id')
FOR UPDATE;

-- name: GetRequest :one
SELECT user_id, operation, request_id, thread_id::text AS thread_id, payload_hash,
       status, expected_revision, attempt_id, response, lease_until
FROM takes_reply_requests
WHERE user_id = sqlc.arg('user_id') AND operation = sqlc.arg('operation')
  AND request_id = sqlc.arg('request_id');

-- name: CreateThread :exec
INSERT INTO threads (id, user_id, title, metadata, thread_kind)
VALUES (sqlc.arg('thread_id'), sqlc.arg('user_id'), 'New Take', sqlc.arg('metadata'), 'TAKES');

-- name: CreateConversation :exec
INSERT INTO takes_reply_conversations (thread_id, user_id, current_revision, draft_state, created_at, updated_at)
VALUES (sqlc.arg('thread_id'), sqlc.arg('user_id'), 0, sqlc.arg('draft_state'), sqlc.arg('created_at'), sqlc.arg('created_at'));

-- name: InsertRequest :exec
INSERT INTO takes_reply_requests (user_id, operation, request_id, thread_id, payload_hash,
    status, expected_revision, attempt_id, response, lease_until, created_at)
VALUES (sqlc.arg('user_id'), sqlc.arg('operation'), sqlc.arg('request_id'), sqlc.arg('thread_id'),
    sqlc.arg('payload_hash'), sqlc.arg('status'), sqlc.arg('expected_revision'), sqlc.arg('attempt_id'),
    sqlc.arg('response'), sqlc.arg('lease_until'), sqlc.arg('created_at'));

-- name: ReopenRequest :execrows
UPDATE takes_reply_requests
SET status = 'RUNNING', attempt_id = sqlc.arg('attempt_id'), response = NULL,
    lease_until = sqlc.arg('lease_until'), completed_at = NULL
WHERE user_id = sqlc.arg('user_id') AND operation = 'TURN' AND request_id = sqlc.arg('request_id')
  AND (status = 'FAILED' OR (status = 'RUNNING' AND (lease_until IS NULL OR lease_until <= now())));

-- name: HasActiveTurn :one
SELECT EXISTS (
    SELECT 1 FROM takes_reply_requests
    WHERE thread_id = sqlc.arg('thread_id') AND operation = 'TURN'
      AND status = 'RUNNING' AND lease_until > now()
);

-- name: InsertMessage :exec
INSERT INTO messages (id, thread_id, role, content, metadata, created_at)
VALUES (sqlc.arg('id'), sqlc.arg('thread_id'), sqlc.arg('role'), sqlc.arg('content'),
        sqlc.arg('metadata'), sqlc.arg('created_at'));

-- name: UpdateConversation :execrows
UPDATE takes_reply_conversations
SET current_revision = sqlc.arg('new_revision'), draft_state = sqlc.arg('draft_state'), updated_at = sqlc.arg('updated_at')
WHERE thread_id = sqlc.arg('thread_id') AND user_id = sqlc.arg('user_id')
  AND current_revision = sqlc.arg('expected_revision');

-- name: CompleteRequest :execrows
UPDATE takes_reply_requests
SET status = 'COMPLETED', response = sqlc.arg('response'), lease_until = NULL,
    completed_at = sqlc.arg('completed_at')
WHERE user_id = sqlc.arg('user_id') AND operation = 'TURN' AND request_id = sqlc.arg('request_id')
  AND attempt_id = sqlc.arg('attempt_id') AND status = 'RUNNING' AND lease_until > now();

-- name: FailRequest :execrows
UPDATE takes_reply_requests
SET status = 'FAILED', response = NULL, lease_until = NULL, completed_at = sqlc.arg('completed_at')
WHERE user_id = sqlc.arg('user_id') AND operation = 'TURN' AND request_id = sqlc.arg('request_id')
  AND attempt_id = sqlc.arg('attempt_id') AND status = 'RUNNING';

-- name: GetBuildMessages :many
SELECT id, role, content, created_at
FROM messages
WHERE thread_id = sqlc.arg('thread_id') AND metadata->>'takes_reply' = 'v1'
  AND COALESCE((metadata->>'takes_reply_revision')::int, 0) <= sqlc.arg('reply_revision')::integer
ORDER BY COALESCE((metadata->>'takes_reply_revision')::int, 0),
         COALESCE((metadata->>'takes_reply_order')::int, 0), created_at, id;

-- name: GetBuildForRevision :one
SELECT * FROM takes_reply_builds
WHERE thread_id = sqlc.arg('thread_id') AND user_id = sqlc.arg('user_id')
  AND reply_revision = sqlc.arg('reply_revision');

-- name: GetLatestBuild :one
SELECT * FROM takes_reply_builds
WHERE thread_id = sqlc.arg('thread_id') AND user_id = sqlc.arg('user_id')
ORDER BY reply_revision DESC LIMIT 1;

-- name: HasActiveBuild :one
SELECT EXISTS (SELECT 1 FROM takes_reply_builds
WHERE user_id = sqlc.arg('user_id') AND status IN ('QUEUED', 'RUNNING'));

-- name: InsertBuild :one
INSERT INTO takes_reply_builds (id, thread_id, user_id, reply_revision, request_id,
    selected_choice_id, snapshot, snapshot_hash, status, created_at)
VALUES (sqlc.arg('id'), sqlc.arg('thread_id'), sqlc.arg('user_id'), sqlc.arg('reply_revision'),
    sqlc.arg('request_id'), sqlc.arg('selected_choice_id'), sqlc.arg('snapshot'),
    sqlc.arg('snapshot_hash'), 'QUEUED', sqlc.arg('created_at'))
RETURNING *;

-- name: ClaimBuild :one
WITH candidate AS (
    SELECT b.id FROM takes_reply_builds b
    JOIN threads t ON t.id = b.thread_id AND t.user_id = b.user_id
    WHERE b.status = 'QUEUED' AND t.deleted_at IS NULL AND t.metadata->>'takes_reply' = 'v1'
    ORDER BY b.created_at, b.id FOR UPDATE OF b SKIP LOCKED LIMIT 1
)
UPDATE takes_reply_builds b SET status = 'RUNNING', started_at = sqlc.arg('started_at'), lease_until = sqlc.arg('lease_until')
FROM candidate WHERE b.id = candidate.id RETURNING b.*;

-- name: SaveBuildStartSnapshot :execrows
UPDATE takes_reply_builds SET start_snapshot = sqlc.arg('start_snapshot')
WHERE id = sqlc.arg('id') AND status = 'RUNNING' AND start_snapshot IS NULL
  AND lease_until > now();

-- name: CompleteBuild :execrows
UPDATE takes_reply_builds SET status = sqlc.arg('status'), proposal = sqlc.arg('proposal'),
    assessment = sqlc.arg('assessment'), completion_snapshot = sqlc.arg('completion_snapshot'),
    missing_requirements = sqlc.arg('missing_requirements'), failure_message = sqlc.arg('failure_message'),
    completed_at = sqlc.arg('completed_at'), lease_until = NULL
WHERE id = sqlc.arg('id') AND status = 'RUNNING' AND lease_until > now();

-- A claimed build is never retried after a crash: provider work may have run.
-- name: ExpireBuilds :exec
WITH candidates AS (
    SELECT b.id FROM takes_reply_builds b
    WHERE (b.status = 'RUNNING' AND b.lease_until <= now())
       OR (b.status = 'QUEUED' AND NOT EXISTS (
           SELECT 1 FROM takes_reply_conversations c
           JOIN threads t ON t.id = c.thread_id AND t.user_id = c.user_id
           WHERE c.thread_id = b.thread_id AND c.user_id = b.user_id
             AND t.deleted_at IS NULL AND t.metadata->>'takes_reply' = 'v1'
       ))
    ORDER BY b.created_at, b.id FOR UPDATE OF b SKIP LOCKED LIMIT 100
)
UPDATE takes_reply_builds b SET status = 'FAILED', lease_until = NULL,
    completed_at = sqlc.arg('completed_at'), failure_message = CASE
        WHEN b.status = 'QUEUED' THEN 'The source reply conversation is no longer available. No research was started.'
        ELSE 'The build was interrupted. Its provider work was not repeated.' END
FROM candidates WHERE b.id = candidates.id;
