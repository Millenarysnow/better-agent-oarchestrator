package sqlite

import (
	"strings"
	"testing"

	"github.com/pressly/goose/v3"
)

const codeBuddyMigrationVersion int64 = 20261115044553091

func TestCodeBuddyMigrationPreservesExistingHarnessesAndGuardsRollback(t *testing.T) {
	const previous = codeBuddyMigrationVersion - 1
	db := openMigratedDatabaseCopy(t, previous)
	var before string
	if err := db.QueryRow(`SELECT sql FROM sqlite_master WHERE name = 'sessions'`).Scan(&before); err != nil {
		t.Fatal(err)
	}
	mustExec(t, db, `INSERT INTO projects (id, path, registered_at) VALUES ('cb-project', '/tmp/cb-project', CURRENT_TIMESTAMP)`)
	insert := `INSERT INTO sessions (id, project_id, num, harness, created_at, updated_at, activity_last_at) VALUES (?, 'cb-project', ?, ?, CURRENT_TIMESTAMP, CURRENT_TIMESTAMP, CURRENT_TIMESTAMP)`
	mustExec(t, db, insert, "existing-opencode", 1, "opencode-v2")
	upTo(t, db, codeBuddyMigrationVersion)
	mustExec(t, db, insert, "cb-session", 2, "codebuddy")
	if _, err := db.Exec(insert, "unknown-session", 3, "unknown-agent"); err == nil {
		t.Fatal("unknown harness bypassed the CHECK constraint")
	}
	gooseMu.Lock()
	err := goose.DownTo(db, "migrations", previous)
	gooseMu.Unlock()
	if err == nil || !strings.Contains(err.Error(), "CHECK constraint failed") {
		t.Fatalf("rollback with existing CodeBuddy row=%v, want refusal", err)
	}
	mustExec(t, db, `UPDATE sessions SET harness = '' WHERE harness = 'codebuddy'`)
	downTo(t, db, previous)
	var after string
	if err := db.QueryRow(`SELECT sql FROM sqlite_master WHERE name = 'sessions'`).Scan(&after); err != nil || after != before {
		t.Fatalf("downgrade did not restore original schema: %v", err)
	}
	if _, err := db.Exec(insert, "cb-after-down", 4, "codebuddy"); err == nil {
		t.Fatal("CodeBuddy remained allowed after downgrade")
	}
	var integrity string
	if err := db.QueryRow(`PRAGMA integrity_check`).Scan(&integrity); err != nil || integrity != "ok" {
		t.Fatalf("integrity=%q, err=%v", integrity, err)
	}
}

func TestCodeBuddyMigrationSurvivesLegacyHarnessRepair(t *testing.T) {
	db := openMigratedDatabaseCopy(t, codeBuddyMigrationVersion-1)
	mustExec(t, db, `PRAGMA writable_schema = ON`)
	mustExec(t, db, `UPDATE sqlite_master SET sql = replace(sql, '''command-code'', ', '') WHERE type = 'table' AND name = 'sessions'`)
	mustExec(t, db, `PRAGMA writable_schema = RESET`)
	upTo(t, db, codeBuddyMigrationVersion)
	if err := reconcileHarnessConstraint(db); err != nil {
		t.Fatal(err)
	}
	var schema string
	if err := db.QueryRow(`SELECT sql FROM sqlite_master WHERE name = 'sessions'`).Scan(&schema); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(schema, "'codebuddy'") || !strings.Contains(schema, "'command-code'") {
		t.Fatalf("legacy repair lost a harness: %s", schema)
	}
}

func TestCodeBuddyMigrationUpgradesExistingForkDatabase(t *testing.T) {
	db := openMigratedDatabaseCopy(t, codeBuddyMigrationVersion)
	mustExec(t, db, `INSERT INTO projects (id, path, registered_at) VALUES ('fork-project', '/tmp/fork-project', CURRENT_TIMESTAMP)`)
	insert := `INSERT INTO sessions (id, project_id, num, harness, created_at, updated_at, activity_last_at) VALUES (?, 'fork-project', ?, ?, CURRENT_TIMESTAMP, CURRENT_TIMESTAMP, CURRENT_TIMESTAMP)`
	mustExec(t, db, insert, "old-codebuddy", 1, "codebuddy")
	if err := migrate(db); err != nil {
		t.Fatalf("upgrade existing CodeBuddy database: %v", err)
	}
	var harness string
	if err := db.QueryRow(`SELECT harness FROM sessions WHERE id = 'old-codebuddy'`).Scan(&harness); err != nil || harness != "codebuddy" {
		t.Fatalf("existing session harness = %q, err = %v", harness, err)
	}
	for i, harness := range []string{"codebuddy", "stakpak", "docker-agent", "minimax-code", "open-interpreter", "tau", "mastra-code"} {
		mustExec(t, db, insert, harness, i+2, harness)
	}
}
