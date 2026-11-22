package sqlite

import (
	"context"
	"database/sql"
	"io/fs"
	"testing"
	"testing/fstest"
	"time"

	"github.com/pressly/goose/v3"
)

// Legacy review runs map onto the state machine: complete and delivered become
// completed, a superseded failure becomes superseded, failure and cancel text
// move out of body into reason codes, and running rows stay running with the
// launch that served them so the startup sweep can judge them.
func TestMigrationMapsLegacyReviewRunsOntoStateMachine(t *testing.T) {
	db := openMigratedDatabaseCopyNoForeignKeys(t, 20261115072651599)
	at := time.Date(2026, time.November, 13, 12, 0, 0, 0, time.UTC)

	mustExec(t, db, `INSERT INTO projects (id, path, display_name, registered_at) VALUES ('p', '/tmp/p', 'P', ?);`, at)
	mustExec(t, db, `
INSERT INTO sessions (id, project_id, num, harness, activity_last_at, created_at, updated_at)
VALUES ('w-1', 'p', 1, 'claude-code', ?, ?, ?);`, at, at, at)
	mustExec(t, db, `
INSERT INTO review (id, session_id, project_id, harness, pr_url, reviewer_handle_id, reviewer_launch_id, created_at, updated_at)
VALUES ('rev-1', 'w-1', 'p', 'claude-code', '', 'review-w-1', 'launch-7', ?, ?);`, at, at)
	insert := func(id, sha, status, verdict, body string) {
		t.Helper()
		mustExec(t, db, `
INSERT INTO review_run (id, review_id, session_id, harness, pr_url, target_sha, status, verdict, body, created_at)
VALUES (?, 'rev-1', 'w-1', 'claude-code', 'https://github.com/o/r/pull/1', ?, ?, ?, ?, ?);`, id, sha, status, verdict, body, at)
	}
	insert("complete", "s1", "complete", "approved", "looks good")
	insert("delivered", "s2", "delivered", "changes_requested", "fix it")
	insert("superseded", "s3", "failed", "", "superseded by a review trigger for a newer commit")
	insert("exited", "s4", "failed", "", "reviewer process exited before submitting a result")
	insert("chat-turn", "s5", "failed", "", "reviewer Chat turn ended without submitting a result")
	insert("preflight", "s6", "failed", "", "reviewer preflight: claude not found")
	insert("other-failure", "s7", "failed", "", "launch reviewer: boom")
	insert("user-cancel", "s8", "cancelled", "", "cancelled by user")
	insert("lifecycle-cancel", "s9", "cancelled", "", "cancelled by worker session lifecycle")
	insert("running", "s10", "running", "", "")

	upTo(t, db, 20261115072835107)

	want := map[string]struct{ status, reason, cancelledBy, body, launchID string }{
		"complete":         {"completed", "", "", "looks good", ""},
		"delivered":        {"completed", "", "", "fix it", ""},
		"superseded":       {"superseded", "", "", "", ""},
		"exited":           {"failed", "process_exited", "", "", ""},
		"chat-turn":        {"failed", "turn_ended_without_submit", "", "", ""},
		"preflight":        {"failed", "preflight_failed", "", "", ""},
		"other-failure":    {"failed", "launch_failed", "", "", ""},
		"user-cancel":      {"cancelled", "", "user", "", ""},
		"lifecycle-cancel": {"cancelled", "", "worker_lifecycle", "", ""},
		"running":          {"running", "", "", "", "launch-7"},
	}
	for id, w := range want {
		var status, reason, cancelledBy, body, launchID string
		var startedAt sql.NullTime
		if err := db.QueryRow(`SELECT status, failure_reason, cancelled_by, body, launch_id, started_at FROM review_run WHERE id = ?`, id).
			Scan(&status, &reason, &cancelledBy, &body, &launchID, &startedAt); err != nil {
			t.Fatalf("read %s: %v", id, err)
		}
		if status != w.status || reason != w.reason || cancelledBy != w.cancelledBy || body != w.body || launchID != w.launchID {
			t.Errorf("%s = {%s %s %s %q %s}, want %+v", id, status, reason, cancelledBy, body, launchID, w)
		}
		if id == "running" && !startedAt.Valid {
			t.Errorf("running row has no started_at")
		}
	}

	// The lock now covers every active state: a queued pass blocks another
	// active pass on the same head and harness, a final one does not.
	insert("queued", "s11", "queued", "", "")
	if _, err := db.Exec(`
INSERT INTO review_run (id, review_id, session_id, harness, pr_url, target_sha, status, verdict, body, created_at)
VALUES ('dup', 'rev-1', 'w-1', 'claude-code', 'https://github.com/o/r/pull/1', 's11', 'launching', '', '', ?);`, at); err == nil {
		t.Fatal("second active run on the same head and harness was accepted")
	}
	insert("after-final", "s1", "queued", "", "")
}

// The old fork shipped local deliveries without the upstream run supervisor.
// Exercise the out-of-order upgrade with an existing approved delivery and
// the new deduplication fence rather than testing only a fresh database.
func TestMigrationPreservesLegacyLocalReview(t *testing.T) {
	db := openMigratedDatabaseCopy(t, 20261115072651599)
	const localMigration = "migrations/20261117040215519_local_delivery_review.sql"
	sqlText, err := fs.ReadFile(migrationsFS, localMigration)
	if err != nil {
		t.Fatal(err)
	}
	func() {
		gooseMu.Lock()
		defer gooseMu.Unlock()
		goose.SetBaseFS(fstest.MapFS{localMigration: {Data: sqlText}})
		defer goose.SetBaseFS(migrationsFS)
		if err := goose.Up(db, "migrations"); err != nil {
			t.Fatalf("apply the fork's local migration without the state machine: %v", err)
		}
	}()

	const legacy = "2026-11-17 09:00:00 +0530 IST"
	mustExec(t, db, "INSERT INTO projects (id,path,registered_at) VALUES ('p','/tmp/p',CURRENT_TIMESTAMP)")
	mustExec(t, db, "INSERT INTO sessions (id,project_id,num,harness,activity_last_at,created_at,updated_at,auto_local_review_enabled) VALUES ('w','p',1,'codex',CURRENT_TIMESTAMP,CURRENT_TIMESTAMP,CURRENT_TIMESTAMP,1)")
	mustExec(t, db, "INSERT INTO reports (id,session_id,project_id,state,note,created_at,available_at,settlement_deadline) VALUES ('d','w','p','done','Delivered',?,?,?)", legacy, legacy, legacy)
	mustExec(t, db, "INSERT INTO local_deliveries (id,session_id,base_sha,target_sha,decision,created_at,decided_at) VALUES ('d','w','base','head','accepted',?,?)", legacy, legacy)
	mustExec(t, db, "INSERT INTO review (id,session_id,project_id,harness,created_at,updated_at) VALUES ('r','w','p','codex',CURRENT_TIMESTAMP,CURRENT_TIMESTAMP)")
	const insertRun = "INSERT INTO review_run (id,review_id,session_id,harness,target_kind,delivery_id,base_sha,target_sha,status,verdict,created_at) VALUES (?,'r','w','codex','local','d','base','head',?,?,CURRENT_TIMESTAMP)"
	mustExec(t, db, insertRun, "legacy", "complete", "approved")
	if err := migrate(db); err != nil {
		t.Fatalf("upgrade the local-review database: %v", err)
	}
	var auto bool
	if err := db.QueryRow("SELECT auto_local_review_enabled FROM sessions WHERE id='w'").Scan(&auto); err != nil || !auto {
		t.Fatalf("existing automatic review = %v, err=%v", auto, err)
	}
	var status, verdict, kind, delivery, base string
	if err := db.QueryRow("SELECT status,verdict,target_kind,delivery_id,base_sha FROM review_run WHERE id='legacy'").
		Scan(&status, &verdict, &kind, &delivery, &base); err != nil || status != "completed" || verdict != "approved" || kind != "local" || delivery != "d" || base != "base" {
		t.Fatalf("existing local run = %s %s %s %s %s, err=%v", status, verdict, kind, delivery, base, err)
	}
	mustExec(t, db, insertRun, "queued", "queued", "")
	if _, err := db.Exec(insertRun, "duplicate", "launching", ""); err == nil {
		t.Fatal("two active local passes on the same delivery, commit and harness")
	}

	changeLogState := func() [2]int64 {
		t.Helper()
		var state [2]int64
		if err := db.QueryRow("SELECT (SELECT count(*) FROM change_log), COALESCE((SELECT seq FROM sqlite_sequence WHERE name='change_log'), -1)").Scan(&state[0], &state[1]); err != nil {
			t.Fatal(err)
		}
		return state
	}
	before := changeLogState()
	if err := repairTimestampTables(context.Background(), db, true); err != nil {
		t.Fatal(err)
	}
	var decision, created, decided string
	if err := db.QueryRow("SELECT decision,CAST(created_at AS TEXT),CAST(decided_at AS TEXT) FROM local_deliveries WHERE id='d'").Scan(&decision, &created, &decided); err != nil ||
		decision != "accepted" || created != "2026-11-17 03:30:00 +0000 UTC" || decided != created {
		t.Fatalf("existing delivery = %s %s %s, err=%v", decision, created, decided, err)
	}
	if after := changeLogState(); after != before {
		t.Fatalf("timestamp normalization published a change: %v -> %v", before, after)
	}
}
