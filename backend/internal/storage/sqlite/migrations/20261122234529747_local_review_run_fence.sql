-- Preserve the local delivery identity and the run supervisor's lock across
-- every active state. The previously shipped migrations may be applied in
-- either order when upgrading existing dev and local-review installations.
-- +goose Up
DROP INDEX idx_review_run_session_pr_sha_harness;
CREATE UNIQUE INDEX idx_review_run_session_pr_sha_harness
    ON review_run (session_id, target_kind, pr_url, delivery_id, target_sha, harness)
    WHERE target_sha != '' AND status IN ('queued', 'launching', 'running', 'cancelling');

-- +goose Down
DROP INDEX idx_review_run_session_pr_sha_harness;
CREATE UNIQUE INDEX idx_review_run_session_pr_sha_harness
    ON review_run (session_id, target_kind, pr_url, delivery_id, target_sha, harness)
    WHERE target_sha != '' AND status = 'running';
