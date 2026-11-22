-- name: InsertLocalDelivery :exec
INSERT INTO local_deliveries(id, session_id, base_sha, target_sha, target_branch, input_error, created_at)
VALUES (?, ?, ?, ?, ?, ?, ?) ON CONFLICT(id) DO NOTHING;

-- name: GetLocalDelivery :one
SELECT * FROM local_deliveries WHERE session_id = ? ORDER BY created_at DESC, id DESC LIMIT 1;

-- name: ListLocalDeliveriesForSessions :many
SELECT d.* FROM local_deliveries d
WHERE d.session_id IN (sqlc.slice('session_ids'))
  AND NOT EXISTS (SELECT 1 FROM local_deliveries newer WHERE newer.session_id = d.session_id
    AND (newer.created_at > d.created_at OR (newer.created_at = d.created_at AND newer.id > d.id)));

-- name: ListLocalReviewRunsForSessions :many
SELECT r.* FROM review_run r JOIN local_deliveries d ON d.id = r.delivery_id
WHERE r.session_id IN (sqlc.slice('session_ids')) AND r.target_kind = 'local'
  AND NOT EXISTS (SELECT 1 FROM local_deliveries newer WHERE newer.session_id = d.session_id
    AND (newer.created_at > d.created_at OR (newer.created_at = d.created_at AND newer.id > d.id)))
ORDER BY r.created_at DESC, r.id DESC;

-- name: LocalReworkCounts :many
SELECT session_id, COUNT(*) AS reworks FROM local_deliveries
WHERE session_id IN (sqlc.slice('session_ids')) AND decision = 'changes_requested' GROUP BY session_id;

-- name: DecideLocalDelivery :execrows
UPDATE local_deliveries SET decision = ?, decision_note = ?, decided_by = ?, decided_at = ?
WHERE local_deliveries.id = ? AND local_deliveries.session_id = ? AND decision IN ('', 'needs_input')
  AND NOT EXISTS (SELECT 1 FROM local_deliveries newer WHERE newer.session_id = local_deliveries.session_id
    AND (newer.created_at > local_deliveries.created_at OR (newer.created_at = local_deliveries.created_at AND newer.id > local_deliveries.id)));

-- name: EnqueueLocalReviewReport :execrows
INSERT INTO reports(id, session_id, project_id, state, note, message, created_at, available_at, repeat_count)
SELECT sqlc.arg(id), sessions.id, sessions.project_id, 'checkpoint', sqlc.arg(note), '', sqlc.arg(created_at), sqlc.arg(created_at), 1
FROM sessions WHERE sessions.id = sqlc.arg(session_id)
ON CONFLICT(id) DO NOTHING;

-- name: UndeliveredLocalReviewRuns :many
SELECT r.* FROM review_run r WHERE r.target_kind = 'local' AND r.status IN ('completed', 'failed', 'cancelled', 'superseded')
AND NOT EXISTS (SELECT 1 FROM reports WHERE reports.id = 'local-review-' || r.id);
