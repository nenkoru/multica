package handler

import (
	"context"
	"net/http"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgtype"
	"github.com/multica-ai/multica/server/internal/testutil"
	db "github.com/multica-ai/multica/server/pkg/db/generated"
)

func planningTestEpic(t *testing.T, title string) string {
	t.Helper()
	return dbfx.Insert(t, "epic", testutil.Cols{"workspace_id": testWorkspaceID, "title": title})
}

func planningTestSprint(t *testing.T, name, start, end, status string) string {
	t.Helper()
	return dbfx.Insert(t, "sprint", testutil.Cols{"workspace_id": testWorkspaceID, "name": name, "start_date": start, "end_date": end, "status": status})
}

func planningStatusChange(t *testing.T, issueID, to string, at time.Time) {
	t.Helper()
	dbfx.Insert(t, "activity_log", testutil.Cols{
		"workspace_id": testWorkspaceID, "issue_id": issueID, "actor_type": "member", "actor_id": testUserID,
		"action": "status_changed", "details": `{"from":"todo","to":"` + to + `"}`, "created_at": at,
	})
}

func TestPlanningEpicOverviewAggregates(t *testing.T) {
	epic := planningTestEpic(t, "Overview epic")
	otherEpic := planningTestEpic(t, "Other overview epic")
	sprint := planningTestSprint(t, "Overview sprint", "2026-10-05", "2026-10-19", "active")
	customKey := "ov_qa"
	dbfx.Insert(t, "issue_status", testutil.Cols{"workspace_id": testWorkspaceID, "key": customKey, "name": "Overview QA", "category": "started", "color": "#123456", "position": 99998})

	mine := testutil.Cols{"epic_id": epic, "assignee_type": "member", "assignee_id": testUserID}
	merge := func(base testutil.Cols, over testutil.Cols) testutil.Cols {
		out := testutil.Cols{}
		for k, v := range base {
			out[k] = v
		}
		for k, v := range over {
			out[k] = v
		}
		return out
	}
	dbfx.Issue(t, "Overview done", merge(mine, testutil.Cols{"status": "done", "sprint_id": sprint}))
	dbfx.Issue(t, "Overview in progress", merge(mine, testutil.Cols{"status": "in_progress", "sprint_id": sprint}))
	blocked := dbfx.Issue(t, "Overview blocked", merge(mine, testutil.Cols{"status": "blocked"}))
	review := dbfx.Issue(t, "Overview review", testutil.Cols{"epic_id": epic, "status": "in_review"})
	dbfx.Issue(t, "Overview todo", testutil.Cols{"epic_id": epic, "status": "todo"})
	dbfx.Issue(t, "Overview backlog", testutil.Cols{"epic_id": epic, "status": "backlog"})
	dbfx.Issue(t, "Overview cancelled", testutil.Cols{"epic_id": epic, "status": "cancelled"})
	dbfx.Issue(t, "Overview custom started", testutil.Cols{"epic_id": epic, "status": customKey})
	// Neither another epic's issue nor an unlinked one may leak into the totals.
	outside := dbfx.Issue(t, "Overview other epic", testutil.Cols{"epic_id": otherEpic, "status": "blocked"})
	dbfx.Issue(t, "Overview unlinked", testutil.Cols{"status": "done"})

	now := time.Now().UTC()
	planningStatusChange(t, blocked, "blocked", now.Add(-time.Minute))
	planningStatusChange(t, outside, "blocked", now)

	var got EpicOverviewResponse
	testutil.Call(t, testHandler.GetEpicOverview, withURLParam(newRequest("GET", "/api/epics/"+epic+"/overview", nil), "id", epic)).Want(http.StatusOK).JSON(&got)

	want := PlanningProgress{Total: 8, Done: 1, InProgress: 2, InReview: 1, Blocked: 1, NotStarted: 2, Cancelled: 1}
	if got.Progress != want {
		t.Fatalf("progress = %+v, want %+v", got.Progress, want)
	}
	if got.LastActivityAt == nil {
		t.Fatal("last_activity_at missing for an epic with issues")
	}

	if len(got.Assignees) != 2 {
		t.Fatalf("assignees = %+v, want member and unassigned", got.Assignees)
	}
	member, unassigned := got.Assignees[0], got.Assignees[1]
	if member.AssigneeID == nil || *member.AssigneeID != testUserID || member.Total != 3 || member.Done != 1 || member.Open != 2 {
		t.Fatalf("member breakdown = %+v", member)
	}
	// 5 unassigned: review, todo, backlog, cancelled, custom; cancelled is not open.
	if unassigned.AssigneeID != nil || unassigned.Total != 5 || unassigned.Done != 0 || unassigned.Open != 4 {
		t.Fatalf("unassigned breakdown = %+v", unassigned)
	}

	if len(got.Sprints) != 2 {
		t.Fatalf("sprints = %+v, want the linked sprint and the no-sprint row", got.Sprints)
	}
	if got.Sprints[0].SprintID == nil || *got.Sprints[0].SprintID != sprint || got.Sprints[0].Name != "Overview sprint" || got.Sprints[0].Total != 2 || got.Sprints[0].Done != 1 {
		t.Fatalf("sprint breakdown = %+v", got.Sprints[0])
	}
	if got.Sprints[1].SprintID != nil || got.Sprints[1].Total != 6 || got.Sprints[1].Done != 0 {
		t.Fatalf("no-sprint breakdown = %+v", got.Sprints[1])
	}

	if len(got.Attention) != 2 || got.Attention[0].ID != blocked || got.Attention[0].Bucket != "blocked" || got.Attention[1].ID != review || got.Attention[1].Bucket != "in_review" {
		t.Fatalf("attention = %+v, want blocked then in_review", got.Attention)
	}
	if got.Attention[0].Identifier == "" {
		t.Fatal("attention issue has no identifier")
	}

	if len(got.Activity) != 1 || got.Activity[0].IssueID != blocked || got.Activity[0].Action != "status_changed" {
		t.Fatalf("activity = %+v, want only the epic's own status change", got.Activity)
	}
}

func TestPlanningOverviewEmptyAndBoundaries(t *testing.T) {
	epic := planningTestEpic(t, "Empty overview epic")
	sprint := planningTestSprint(t, "Empty overview sprint", "2026-10-05", "2026-10-19", "active")

	var emptyEpic EpicOverviewResponse
	testutil.Call(t, testHandler.GetEpicOverview, withURLParam(newRequest("GET", "/api/epics/"+epic+"/overview", nil), "id", epic)).Want(http.StatusOK).JSON(&emptyEpic)
	if emptyEpic.Progress.Total != 0 || emptyEpic.Assignees == nil || emptyEpic.Sprints == nil || emptyEpic.Attention == nil || emptyEpic.Activity == nil || len(emptyEpic.Activity) != 0 {
		t.Fatalf("empty epic overview must be zeroes and empty arrays: %+v", emptyEpic)
	}
	var emptySprint SprintOverviewResponse
	testutil.Call(t, testHandler.GetSprintOverview, withURLParam(newRequest("GET", "/api/sprints/"+sprint+"/overview", nil), "id", sprint)).Want(http.StatusOK).JSON(&emptySprint)
	if emptySprint.Progress.Total != 0 || emptySprint.Burndown != nil || emptySprint.Epics == nil || len(emptySprint.Epics) != 0 {
		t.Fatalf("empty sprint overview must carry no burndown: %+v", emptySprint)
	}

	foreign := dbfx.Workspace(t, "Foreign overview", "foreign-planning-overview")
	foreignEpic := dbfx.Insert(t, "epic", testutil.Cols{"workspace_id": foreign, "title": "Private epic"})
	foreignSprint := dbfx.Insert(t, "sprint", testutil.Cols{"workspace_id": foreign, "name": "Private sprint", "start_date": "2026-10-09", "end_date": "2026-10-16"})
	testutil.Call(t, testHandler.GetEpicOverview, withURLParam(newRequest("GET", "/api/epics/"+foreignEpic+"/overview", nil), "id", foreignEpic)).Want(http.StatusNotFound)
	testutil.Call(t, testHandler.GetSprintOverview, withURLParam(newRequest("GET", "/api/sprints/"+foreignSprint+"/overview", nil), "id", foreignSprint)).Want(http.StatusNotFound)
	testutil.Call(t, testHandler.CompleteSprint, withURLParam(newRequest("POST", "/api/sprints/"+foreignSprint+"/complete", map[string]any{"open_issues": "unlink"}), "id", foreignSprint)).Want(http.StatusNotFound)
	// A foreign sprint is never a valid move target either.
	testutil.Call(t, testHandler.CompleteSprint, withURLParam(newRequest("POST", "/api/sprints/"+sprint+"/complete", map[string]any{"open_issues": "move", "target_sprint_id": foreignSprint}), "id", sprint)).Want(http.StatusBadRequest)

	previous := testHandler.cfg.PlanningEnabled
	testHandler.cfg.PlanningEnabled = false
	t.Cleanup(func() { testHandler.cfg.PlanningEnabled = previous })
	testutil.Call(t, testHandler.GetEpicOverview, withURLParam(newRequest("GET", "/api/epics/"+epic+"/overview", nil), "id", epic)).Want(http.StatusNotFound)
	testutil.Call(t, testHandler.GetSprintOverview, withURLParam(newRequest("GET", "/api/sprints/"+sprint+"/overview", nil), "id", sprint)).Want(http.StatusNotFound)
	testutil.Call(t, testHandler.CompleteSprint, withURLParam(newRequest("POST", "/api/sprints/"+sprint+"/complete", map[string]any{"open_issues": "keep"}), "id", sprint)).Want(http.StatusNotFound)
}

func TestPlanningSprintOverviewAndBurndown(t *testing.T) {
	epic := planningTestEpic(t, "Burndown epic")
	sprint := planningTestSprint(t, "Burndown sprint", "2026-10-05", "2026-10-12", "active")
	day := func(s string) time.Time {
		parsed, err := time.Parse(time.RFC3339, s)
		if err != nil {
			t.Fatal(err)
		}
		return parsed
	}
	in := testutil.Cols{"sprint_id": sprint}
	with := func(over testutil.Cols) testutil.Cols {
		out := testutil.Cols{}
		for k, v := range in {
			out[k] = v
		}
		for k, v := range over {
			out[k] = v
		}
		return out
	}
	// Closed before the sprint started: already off the chart on day one.
	early := dbfx.Issue(t, "Burndown early", with(testutil.Cols{"status": "done"}))
	planningStatusChange(t, early, "done", day("2026-10-03T10:00:00Z"))
	// Reopened and closed again: only the newest closing counts.
	second := dbfx.Issue(t, "Burndown reclosed", with(testutil.Cols{"status": "done", "epic_id": epic}))
	planningStatusChange(t, second, "done", day("2026-10-05T09:00:00Z"))
	planningStatusChange(t, second, "done", day("2026-10-06T09:00:00Z"))
	// No recorded status change: falls back to the last update.
	dbfx.Issue(t, "Burndown imported", with(testutil.Cols{"status": "cancelled", "updated_at": day("2026-10-07T23:30:00Z")}))
	dbfx.Issue(t, "Burndown open", with(testutil.Cols{"status": "in_progress", "epic_id": epic}))
	dbfx.Issue(t, "Burndown todo", with(testutil.Cols{"status": "todo"}))

	var got SprintOverviewResponse
	testutil.Call(t, testHandler.GetSprintOverview, withURLParam(newRequest("GET", "/api/sprints/"+sprint+"/overview", nil), "id", sprint)).Want(http.StatusOK).JSON(&got)
	want := PlanningProgress{Total: 5, Done: 2, InProgress: 1, NotStarted: 1, Cancelled: 1}
	if got.Progress != want {
		t.Fatalf("progress = %+v, want %+v", got.Progress, want)
	}
	if len(got.Epics) != 2 || got.Epics[0].EpicID == nil || *got.Epics[0].EpicID != epic || got.Epics[0].Total != 2 || got.Epics[0].Done != 1 || got.Epics[1].EpicID != nil || got.Epics[1].Total != 3 {
		t.Fatalf("epic breakdown = %+v", got.Epics)
	}

	// The handler stops at "today"; drive the series with a fixed clock so the
	// assertion does not depend on when the suite runs.
	sprintRow, err := testHandler.Queries.GetSprintInWorkspace(context.Background(), db.GetSprintInWorkspaceParams{ID: parseUUID(sprint), WorkspaceID: parseUUID(testWorkspaceID)})
	if err != nil {
		t.Fatal(err)
	}
	burndown, err := testHandler.loadSprintBurndown(context.Background(), sprintRow, 5, day("2026-10-08T00:00:00Z"))
	if err != nil {
		t.Fatal(err)
	}
	if burndown == nil || burndown.Total != 5 || burndown.StartDate != "2026-10-05" || burndown.EndDate != "2026-10-12" {
		t.Fatalf("burndown header = %+v", burndown)
	}
	wantDays := []PlanningBurndownDay{
		{Date: "2026-10-05", Remaining: 4},
		{Date: "2026-10-06", Remaining: 3},
		{Date: "2026-10-07", Remaining: 2},
		{Date: "2026-10-08", Remaining: 2},
	}
	if len(burndown.Days) != len(wantDays) {
		t.Fatalf("burndown days = %+v, want %+v", burndown.Days, wantDays)
	}
	for i := range wantDays {
		if burndown.Days[i] != wantDays[i] {
			t.Fatalf("burndown day %d = %+v, want %+v", i, burndown.Days[i], wantDays[i])
		}
	}

	// The series never runs past the sprint's end, and a sprint that has not
	// started has no chart at all.
	late, err := testHandler.loadSprintBurndown(context.Background(), sprintRow, 5, day("2026-11-01T00:00:00Z"))
	if err != nil || late == nil || len(late.Days) != 8 || late.Days[7].Date != "2026-10-12" {
		t.Fatalf("burndown after the end = %+v, err %v", late, err)
	}
	future, err := testHandler.loadSprintBurndown(context.Background(), sprintRow, 5, day("2026-10-01T00:00:00Z"))
	if err != nil || future != nil {
		t.Fatalf("burndown before the start = %+v, err %v", future, err)
	}
}

func TestPlanningListProgress(t *testing.T) {
	epic := planningTestEpic(t, "List progress epic")
	emptyEpic := planningTestEpic(t, "List progress empty epic")
	sprint := planningTestSprint(t, "List progress sprint", "2026-10-05", "2026-10-19", "planned")
	dbfx.Issue(t, "List progress done", testutil.Cols{"epic_id": epic, "sprint_id": sprint, "status": "done"})
	dbfx.Issue(t, "List progress blocked", testutil.Cols{"epic_id": epic, "sprint_id": sprint, "status": "blocked"})
	dbfx.Issue(t, "List progress todo", testutil.Cols{"epic_id": epic, "status": "todo"})

	var epics struct {
		Epics []EpicResponse `json:"epics"`
	}
	testutil.Call(t, testHandler.ListEpics, newRequest("GET", "/api/epics", nil)).Want(http.StatusOK).JSON(&epics)
	seen := 0
	for _, e := range epics.Epics {
		switch e.ID {
		case epic:
			seen++
			if want := (PlanningProgress{Total: 3, Done: 1, Blocked: 1, NotStarted: 1}); e.Progress != want {
				t.Fatalf("epic list progress = %+v, want %+v", e.Progress, want)
			}
		case emptyEpic:
			seen++
			if e.Progress != (PlanningProgress{}) {
				t.Fatalf("empty epic list progress = %+v", e.Progress)
			}
		}
	}
	if seen != 2 {
		t.Fatalf("list returned %d of the 2 test epics", seen)
	}

	var gotEpic EpicResponse
	testutil.Call(t, testHandler.GetEpic, withURLParam(newRequest("GET", "/api/epics/"+epic, nil), "id", epic)).Want(http.StatusOK).JSON(&gotEpic)
	if gotEpic.Progress.Total != 3 || gotEpic.IssueCount != 3 || gotEpic.DoneCount != 1 {
		t.Fatalf("epic detail = %+v", gotEpic)
	}

	var sprints struct {
		Sprints []SprintResponse `json:"sprints"`
	}
	testutil.Call(t, testHandler.ListSprints, newRequest("GET", "/api/sprints", nil)).Want(http.StatusOK).JSON(&sprints)
	for _, s := range sprints.Sprints {
		if s.ID == sprint {
			if want := (PlanningProgress{Total: 2, Done: 1, Blocked: 1}); s.Progress != want {
				t.Fatalf("sprint list progress = %+v, want %+v", s.Progress, want)
			}
			return
		}
	}
	t.Fatal("test sprint missing from the list")
}

func planningSprintOf(t *testing.T, issueID string) *string {
	t.Helper()
	var sprintID pgtype.UUID
	dbfx.QueryRow(t, `SELECT sprint_id FROM issue WHERE id = $1`, issueID).Scan(&sprintID)
	return uuidToPtr(sprintID)
}

func TestSprintCompleteIsExplicit(t *testing.T) {
	complete := func(t *testing.T, id string, body map[string]any, want int) CompleteSprintResponse {
		t.Helper()
		var out CompleteSprintResponse
		resp := testutil.Call(t, testHandler.CompleteSprint, withURLParam(newRequest("POST", "/api/sprints/"+id+"/complete", body), "id", id)).Want(want)
		if want == http.StatusOK {
			resp.JSON(&out)
		}
		return out
	}
	seed := func(t *testing.T, sprint string) (open, review, done, cancelled string) {
		t.Helper()
		open = dbfx.Issue(t, "Complete open", testutil.Cols{"sprint_id": sprint, "status": "in_progress"})
		review = dbfx.Issue(t, "Complete review", testutil.Cols{"sprint_id": sprint, "status": "in_review"})
		done = dbfx.Issue(t, "Complete done", testutil.Cols{"sprint_id": sprint, "status": "done"})
		cancelled = dbfx.Issue(t, "Complete cancelled", testutil.Cols{"sprint_id": sprint, "status": "cancelled"})
		return
	}
	isOn := func(t *testing.T, issue string, sprint *string) {
		t.Helper()
		got := planningSprintOf(t, issue)
		if (got == nil) != (sprint == nil) || (got != nil && *got != *sprint) {
			t.Fatalf("issue %s sprint = %v, want %v", issue, got, sprint)
		}
	}

	t.Run("status update alone moves nothing", func(t *testing.T) {
		sprint := planningTestSprint(t, "Complete via PUT", "2026-10-05", "2026-10-12", "active")
		planningTestSprint(t, "Complete via PUT next", "2026-10-12", "2026-10-19", "planned")
		open, review, done, cancelled := seed(t, sprint)
		testutil.Call(t, testHandler.UpdateSprint, withURLParam(newRequest("PUT", "/api/sprints/"+sprint, map[string]any{"status": "completed"}), "id", sprint)).Want(http.StatusOK)
		for _, issue := range []string{open, review, done, cancelled} {
			isOn(t, issue, &sprint)
		}
	})

	t.Run("choice is required and validated before any write", func(t *testing.T) {
		sprint := planningTestSprint(t, "Complete validation", "2026-10-05", "2026-10-12", "active")
		open, _, _, _ := seed(t, sprint)
		complete(t, sprint, map[string]any{}, http.StatusBadRequest)
		complete(t, sprint, map[string]any{"open_issues": "delete"}, http.StatusBadRequest)
		complete(t, sprint, map[string]any{"open_issues": "unlink", "target_sprint_id": sprint}, http.StatusBadRequest)
		complete(t, sprint, map[string]any{"open_issues": "move", "target_sprint_id": sprint}, http.StatusBadRequest)
		complete(t, sprint, map[string]any{"open_issues": "move", "target_sprint_id": "not-a-uuid"}, http.StatusBadRequest)
		finished := planningTestSprint(t, "Complete validation finished", "2026-09-01", "2026-09-08", "completed")
		complete(t, sprint, map[string]any{"open_issues": "move", "target_sprint_id": finished}, http.StatusBadRequest)
		isOn(t, open, &sprint)
		var status string
		dbfx.QueryRow(t, `SELECT status FROM sprint WHERE id = $1`, sprint).Scan(&status)
		if status != "active" {
			t.Fatalf("rejected completion changed the sprint to %q", status)
		}
	})

	t.Run("keep leaves every issue on the sprint", func(t *testing.T) {
		sprint := planningTestSprint(t, "Complete keep", "2026-10-05", "2026-10-12", "active")
		open, review, done, cancelled := seed(t, sprint)
		got := complete(t, sprint, map[string]any{"open_issues": "keep"}, http.StatusOK)
		if got.Sprint.Status != "completed" || got.KeptCount != 2 || got.MovedCount != 0 || got.UnlinkedCount != 0 || got.TargetSprint != nil {
			t.Fatalf("keep result = %+v", got)
		}
		for _, issue := range []string{open, review, done, cancelled} {
			isOn(t, issue, &sprint)
		}
		// A finished sprint cannot be completed again with another choice.
		complete(t, sprint, map[string]any{"open_issues": "unlink"}, http.StatusConflict)
		isOn(t, open, &sprint)
	})

	t.Run("unlink detaches only unfinished issues", func(t *testing.T) {
		sprint := planningTestSprint(t, "Complete unlink", "2026-10-05", "2026-10-12", "active")
		open, review, done, cancelled := seed(t, sprint)
		var revisionBefore int64
		dbfx.QueryRow(t, `SELECT revision FROM issue WHERE id = $1`, open).Scan(&revisionBefore)
		got := complete(t, sprint, map[string]any{"open_issues": "unlink"}, http.StatusOK)
		if got.UnlinkedCount != 2 || got.MovedCount != 0 || got.KeptCount != 0 || got.Sprint.IssueCount != 2 {
			t.Fatalf("unlink result = %+v", got)
		}
		isOn(t, open, nil)
		isOn(t, review, nil)
		isOn(t, done, &sprint)
		isOn(t, cancelled, &sprint)
		var revisionAfter int64
		dbfx.QueryRow(t, `SELECT revision FROM issue WHERE id = $1`, open).Scan(&revisionAfter)
		if revisionAfter != revisionBefore+1 {
			t.Fatalf("unlinked issue revision %d -> %d, want +1", revisionBefore, revisionAfter)
		}
		if dbfx.Count(t, `SELECT count(*) FROM issue WHERE id::text = ANY($1::text[])`, []string{open, review, done, cancelled}) != 4 {
			t.Fatal("completion deleted an issue")
		}
	})

	t.Run("move goes to the named sprint", func(t *testing.T) {
		sprint := planningTestSprint(t, "Complete move named", "2026-10-05", "2026-10-12", "active")
		target := planningTestSprint(t, "Complete move named target", "2026-11-02", "2026-11-09", "planned")
		open, review, done, cancelled := seed(t, sprint)
		got := complete(t, sprint, map[string]any{"open_issues": "move", "target_sprint_id": target}, http.StatusOK)
		if got.MovedCount != 2 || got.TargetSprint == nil || got.TargetSprint.ID != target || got.TargetSprint.IssueCount != 2 {
			t.Fatalf("move result = %+v", got)
		}
		isOn(t, open, &target)
		isOn(t, review, &target)
		isOn(t, done, &sprint)
		isOn(t, cancelled, &sprint)
	})
}

// The default move target is workspace state shared with other tests, so this
// case owns a workspace of its own.
func TestSprintCompleteDefaultTarget(t *testing.T) {
	ws := dbfx.Workspace(t, "Sprint default target", "sprint-complete-default-target")
	dbfx.Member(t, ws, testUserID, "owner")
	sprintIn := func(name, start, end, status string) string {
		return dbfx.Insert(t, "sprint", testutil.Cols{"workspace_id": ws, "name": name, "start_date": start, "end_date": end, "status": status})
	}
	request := func(id string, body map[string]any) *http.Request {
		req := withURLParam(newRequest("POST", "/api/sprints/"+id+"/complete", body), "id", id)
		req.Header.Set("X-Workspace-ID", ws)
		return req
	}
	current := sprintIn("Current", "2026-10-05", "2026-10-12", "active")
	open := dbfx.Issue(t, "Default target open", testutil.Cols{"workspace_id": ws, "sprint_id": current, "status": "todo"})

	// Without a planned sprint the move is refused and nothing changes.
	testutil.Call(t, testHandler.CompleteSprint, request(current, map[string]any{"open_issues": "move"})).Want(http.StatusConflict)
	if got := planningSprintOf(t, open); got == nil || *got != current {
		t.Fatalf("refused move changed the issue's sprint to %v", got)
	}
	var status string
	dbfx.QueryRow(t, `SELECT status FROM sprint WHERE id = $1`, current).Scan(&status)
	if status != "active" {
		t.Fatalf("refused move changed the sprint to %q", status)
	}

	sprintIn("Earlier planned", "2026-09-01", "2026-09-08", "planned")
	sprintIn("Later planned", "2026-11-02", "2026-11-09", "planned")
	next := sprintIn("Next planned", "2026-10-12", "2026-10-19", "planned")
	sprintIn("Next but active", "2026-10-06", "2026-10-13", "active")

	var got CompleteSprintResponse
	testutil.Call(t, testHandler.CompleteSprint, request(current, map[string]any{"open_issues": "move"})).Want(http.StatusOK).JSON(&got)
	if got.TargetSprint == nil || got.TargetSprint.ID != next || got.MovedCount != 1 {
		t.Fatalf("default target = %+v, want the next planned sprint %s", got.TargetSprint, next)
	}
	if moved := planningSprintOf(t, open); moved == nil || *moved != next {
		t.Fatalf("issue sprint = %v, want %s", moved, next)
	}
}
