package main

import (
	"context"
	"math/rand/v2"
	"slices"
	"testing"
)

func TestPlanningMigrationsPreserveImportedHierarchyAndRollback(t *testing.T) {
	ctx := context.Background()
	admin := openTestPool(t)
	schema := createScratchSchema(t, ctx, admin, "planning_migration_")
	pool := openTestPoolWithSearchPath(t, schema+", public")
	_, err := pool.Exec(ctx, `CREATE TABLE issue (id uuid PRIMARY KEY, workspace_id uuid, parent_issue_id uuid, stage integer, metadata jsonb);
INSERT INTO issue VALUES ('00000000-0000-0000-0000-000000000001', '00000000-0000-0000-0000-000000000002', '00000000-0000-0000-0000-000000000003', 2, '{"source_id":"imported"}');`)
	if err != nil {
		t.Fatal(err)
	}
	versions := []string{"573_epics_and_sprints", "574_planning_idx_epic_workspace", "575_planning_idx_sprint_workspace", "576_planning_idx_sprint_status", "577_planning_idx_issue_epic", "578_planning_idx_issue_sprint"}
	lockKey := int64(rand.Uint64()&0x7fffffffffffffff) | 1
	apply := func(direction string) {
		t.Helper()
		steps := slices.Clone(versions)
		if direction == "down" {
			slices.Reverse(steps)
		}
		if err := runMigrations(ctx, pool, runOptions{Direction: direction, Files: realMigrationFiles(t, steps, direction), SchemaMigrationsTable: schema + ".schema_migrations", AdvisoryLockKey: lockKey, Hooks: hooksForDirection(direction)}); err != nil {
			t.Fatal(err)
		}
	}
	apply("up")
	apply("up") // A repeat is a no-op against the migration ledger.
	var nulls bool
	if err := pool.QueryRow(ctx, "SELECT epic_id IS NULL AND sprint_id IS NULL FROM issue").Scan(&nulls); err != nil || !nulls {
		t.Fatalf("imported relations must default null: %v", err)
	}
	for _, index := range []string{"idx_epic_workspace", "idx_sprint_workspace", "idx_sprint_status", "idx_issue_epic", "idx_issue_sprint"} {
		assertIndexExists(t, pool, schema, index, true)
		assertIndexValidity(t, pool, schema, index, true)
	}
	apply("down")
	var count int
	if err := pool.QueryRow(ctx, "SELECT count(*) FROM issue WHERE stage = 2 AND parent_issue_id = '00000000-0000-0000-0000-000000000003' AND metadata->>'source_id' = 'imported'").Scan(&count); err != nil || count != 1 {
		t.Fatalf("rollback damaged imported issues: count=%d err=%v", count, err)
	}
	apply("up") // The explicit rollback remains recoverable.
}
