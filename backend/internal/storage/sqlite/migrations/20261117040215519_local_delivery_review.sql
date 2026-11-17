-- +goose Up
ALTER TABLE sessions ADD COLUMN auto_local_review_enabled BOOLEAN NOT NULL DEFAULT 0;
ALTER TABLE review_run ADD COLUMN target_kind TEXT NOT NULL DEFAULT 'pr' CHECK (target_kind IN ('pr', 'local'));
ALTER TABLE review_run ADD COLUMN base_sha TEXT NOT NULL DEFAULT '';
ALTER TABLE review_run ADD COLUMN delivery_id TEXT NOT NULL DEFAULT '';
DROP INDEX idx_review_run_session_pr_sha_harness;
CREATE UNIQUE INDEX idx_review_run_session_pr_sha_harness ON review_run (session_id, target_kind, pr_url, delivery_id, target_sha, harness) WHERE target_sha != '' AND status = 'running';
CREATE TABLE local_deliveries (
    id TEXT PRIMARY KEY REFERENCES reports(id),
    session_id TEXT NOT NULL REFERENCES sessions(id) ON DELETE CASCADE,
    base_sha TEXT NOT NULL DEFAULT '',
    target_sha TEXT NOT NULL DEFAULT '',
    target_branch TEXT NOT NULL DEFAULT '',
    input_error TEXT NOT NULL DEFAULT '',
    decision TEXT NOT NULL DEFAULT '' CHECK (decision IN ('', 'accepted', 'changes_requested', 'needs_input')),
    decision_note TEXT NOT NULL DEFAULT '',
    decided_by TEXT NOT NULL DEFAULT '',
    created_at TIMESTAMP NOT NULL,
    decided_at TIMESTAMP
);
CREATE INDEX idx_local_deliveries_session ON local_deliveries(session_id, created_at DESC, id DESC);
-- +goose StatementBegin
CREATE TRIGGER local_deliveries_insert AFTER INSERT ON local_deliveries BEGIN
    INSERT INTO change_log(project_id, session_id, event_type, payload, created_at)
    SELECT project_id, NEW.session_id, 'session_updated', json_object('id', NEW.session_id), CURRENT_TIMESTAMP FROM sessions WHERE id = NEW.session_id;
END;
-- +goose StatementEnd
-- +goose StatementBegin
CREATE TRIGGER local_deliveries_update AFTER UPDATE ON local_deliveries BEGIN
    INSERT INTO change_log(project_id, session_id, event_type, payload, created_at)
    SELECT project_id, NEW.session_id, 'session_updated', json_object('id', NEW.session_id), CURRENT_TIMESTAMP FROM sessions WHERE id = NEW.session_id;
END;
-- +goose StatementEnd

-- +goose Down
DELETE FROM review_run WHERE target_kind = 'local';
DROP INDEX idx_review_run_session_pr_sha_harness;
CREATE UNIQUE INDEX idx_review_run_session_pr_sha_harness ON review_run (session_id, pr_url, target_sha, harness) WHERE target_sha != '' AND status = 'running';
DROP TRIGGER local_deliveries_update;
DROP TRIGGER local_deliveries_insert;
DROP TABLE local_deliveries;
ALTER TABLE review_run DROP COLUMN delivery_id;
ALTER TABLE review_run DROP COLUMN base_sha;
ALTER TABLE review_run DROP COLUMN target_kind;
ALTER TABLE sessions DROP COLUMN auto_local_review_enabled;
