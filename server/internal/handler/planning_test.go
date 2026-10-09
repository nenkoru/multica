package handler

import (
	"net/http"
	"testing"

	"github.com/multica-ai/multica/server/internal/middleware"
	"github.com/multica-ai/multica/server/internal/testutil"
)

func TestPlanningIssueRelationsPreserveHierarchy(t *testing.T) {
	epic := dbfx.Insert(t, "epic", testutil.Cols{"workspace_id": testWorkspaceID, "title": "Planning epic"})
	sprint := dbfx.Insert(t, "sprint", testutil.Cols{"workspace_id": testWorkspaceID, "name": "Planning sprint", "start_date": "2026-10-09", "end_date": "2026-10-16"})
	parent := dbfx.Issue(t, "Planning parent")
	project := dbfx.Project(t, "Planning project")
	var issue IssueResponse
	testutil.Call(t, testHandler.CreateIssue, newRequest("POST", "/api/issues", map[string]any{
		"title": "Planning child", "parent_issue_id": parent, "stage": 2, "project_id": project,
		"epic_id": epic, "sprint_id": sprint,
	})).Want(http.StatusCreated).JSON(&issue)
	t.Cleanup(func() { dbfx.Exec(t, "DELETE FROM issue WHERE id = $1", issue.ID) })
	if issue.EpicID == nil || *issue.EpicID != epic || issue.SprintID == nil || *issue.SprintID != sprint || issue.Stage == nil || *issue.Stage != 2 || issue.ParentIssueID == nil || *issue.ParentIssueID != parent {
		t.Fatalf("create lost independent planning/hierarchy: %+v", issue)
	}
	var filtered struct {
		Issues []IssueResponse `json:"issues"`
		Total  int             `json:"total"`
	}
	testutil.Call(t, testHandler.ListIssues, newRequest("GET", "/api/issues?epic_id="+epic+"&sprint_id="+sprint, nil)).Want(200).JSON(&filtered)
	if filtered.Total != 1 || len(filtered.Issues) != 1 || filtered.Issues[0].ID != issue.ID {
		t.Fatalf("combined planning filter: %+v", filtered)
	}
	request := func(body map[string]any) *IssueResponse {
		var updated IssueResponse
		testutil.Call(t, testHandler.UpdateIssue, withURLParam(newRequest("PUT", "/api/issues/"+issue.ID, body), "id", issue.ID)).Want(200).JSON(&updated)
		return &updated
	}
	updated := request(map[string]any{"priority": "high"})
	if updated.EpicID == nil || updated.SprintID == nil {
		t.Fatal("ordinary update cleared planning")
	}
	updated = request(map[string]any{"epic_id": nil})
	if updated.EpicID != nil || updated.SprintID == nil || updated.ParentIssueID == nil || updated.Stage == nil {
		t.Fatal("clearing epic changed other relations")
	}
	updated = request(map[string]any{"epic_id": epic})
	if updated.EpicID == nil {
		t.Fatal("reattach failed")
	}
	testutil.Call(t, testHandler.DeleteEpic, withURLParam(newRequest("DELETE", "/api/epics/"+epic, nil), "id", epic)).Want(204)
	testutil.Call(t, testHandler.DeleteSprint, withURLParam(newRequest("DELETE", "/api/sprints/"+sprint, nil), "id", sprint)).Want(204)
	var got IssueResponse
	testutil.Call(t, testHandler.GetIssue, withURLParam(newRequest("GET", "/api/issues/"+issue.ID, nil), "id", issue.ID)).Want(200).JSON(&got)
	if got.EpicID != nil || got.SprintID != nil || got.ProjectID == nil || *got.ProjectID != project || got.ParentIssueID == nil || *got.ParentIssueID != parent || got.Stage == nil || *got.Stage != 2 {
		t.Fatalf("delete lost issue/hierarchy: %+v", got)
	}
}

func TestPlanningWorkspaceBoundary(t *testing.T) {
	foreign := dbfx.Workspace(t, "Foreign planning", "foreign-planning-boundary")
	epic := dbfx.Insert(t, "epic", testutil.Cols{"workspace_id": foreign, "title": "Private epic"})
	sprint := dbfx.Insert(t, "sprint", testutil.Cols{"workspace_id": foreign, "name": "Private sprint", "start_date": "2026-10-09", "end_date": "2026-10-16"})
	for _, tc := range []struct {
		noun, id            string
		get, update, delete http.HandlerFunc
	}{
		{"epic", epic, testHandler.GetEpic, testHandler.UpdateEpic, testHandler.DeleteEpic},
		{"sprint", sprint, testHandler.GetSprint, testHandler.UpdateSprint, testHandler.DeleteSprint},
	} {
		t.Run(tc.noun, func(t *testing.T) {
			for _, action := range []struct {
				method string
				fn     http.HandlerFunc
			}{{"GET", tc.get}, {"PUT", tc.update}, {"DELETE", tc.delete}} {
				testutil.Call(t, action.fn, withURLParam(newRequest(action.method, "/api/"+tc.noun+"s/"+tc.id, map[string]any{"status": "closed"}), "id", tc.id)).Want(404)
			}
			testutil.Call(t, testHandler.CreateIssue, newRequest("POST", "/api/issues", map[string]any{"title": "Forged planning", tc.noun + "_id": tc.id})).Want(400)
			issue := dbfx.Issue(t, "Boundary issue "+tc.noun)
			testutil.Call(t, testHandler.UpdateIssue, withURLParam(newRequest("PUT", "/api/issues/"+issue, map[string]any{tc.noun + "_id": tc.id}), "id", issue)).Want(400)
			// Membership middleware gates every real planning route, including task tokens.
			req := newRequest("GET", "/api/"+tc.noun+"s/"+tc.id, nil)
			req.Header.Set("X-Workspace-ID", foreign)
			testutil.Call(t, middleware.RequireWorkspaceMember(testHandler.Queries)(tc.get).ServeHTTP, req).Want(404)
			req = newRequest("GET", "/api/"+tc.noun+"s", nil)
			req.Header.Del("X-User-ID")
			testutil.Call(t, middleware.RequireWorkspaceMember(testHandler.Queries)(tc.get).ServeHTTP, req).Want(401)
		})
	}
}

func TestPlanningImportedNullRelationsAndDisabledFlag(t *testing.T) {
	parent := dbfx.Issue(t, "Imported parent")
	child := dbfx.Issue(t, "Imported child", testutil.Cols{"parent_issue_id": parent, "stage": 3, "metadata": testutil.Raw(`'{"source_id":"legacy-child"}'::jsonb`)})
	var got IssueResponse
	testutil.Call(t, testHandler.GetIssue, withURLParam(newRequest("GET", "/api/issues/"+child, nil), "id", child)).Want(200).JSON(&got)
	if got.EpicID != nil || got.SprintID != nil || got.ParentIssueID == nil || *got.ParentIssueID != parent || got.Stage == nil || *got.Stage != 3 || got.Metadata["source_id"] != "legacy-child" {
		t.Fatalf("import compatibility: %+v", got)
	}
	previous := testHandler.cfg.PlanningEnabled
	testHandler.cfg.PlanningEnabled = false
	t.Cleanup(func() { testHandler.cfg.PlanningEnabled = previous })
	testutil.Call(t, testHandler.ListEpics, newRequest("GET", "/api/epics", nil)).Want(404)
	testutil.Call(t, testHandler.ListSprints, newRequest("GET", "/api/sprints", nil)).Want(404)
	testutil.Call(t, testHandler.UpdateIssue, withURLParam(newRequest("PUT", "/api/issues/"+child, map[string]any{"title": "Still editable"}), "id", child)).Want(200)
}

func TestPlanningTableFiltersAndRowRelations(t *testing.T) {
	epic := dbfx.Insert(t, "epic", testutil.Cols{"workspace_id": testWorkspaceID, "title": "Table epic"})
	sprint := dbfx.Insert(t, "sprint", testutil.Cols{"workspace_id": testWorkspaceID, "name": "Table sprint", "start_date": "2026-10-09", "end_date": "2026-10-16"})
	both := dbfx.Issue(t, "Both planning relations", testutil.Cols{"epic_id": epic, "sprint_id": sprint})
	dbfx.Issue(t, "Epic only", testutil.Cols{"epic_id": epic})
	fetch := func(filters map[string]any, status int) issueTableRowsResponse {
		t.Helper()
		var response issueTableRowsResponse
		call := testutil.Call(t, testHandler.ListIssueTableRows, newRequest("POST", "/api/issues/table/rows", map[string]any{
			"query": map[string]any{"scope": map[string]any{"kind": "workspace"}, "filters": filters, "sort": map[string]any{"field": "position", "direction": "asc"}},
			"group": map[string]any{"kind": "none"}, "hierarchy": map[string]any{"enabled": false}, "page": map[string]any{"limit": 10},
		})).Want(status)
		if status == 200 {
			call.JSON(&response)
		}
		return response
	}
	result := fetch(map[string]any{"epic_ids": []string{epic}, "sprint_ids": []string{sprint}}, 200)
	if result.Total != 1 || len(result.Rows) != 1 || result.Rows[0].Issue.ID != both || result.Rows[0].Issue.EpicID == nil || result.Rows[0].Issue.SprintID == nil {
		t.Fatalf("table query lost planning: %+v", result)
	}
	result = fetch(map[string]any{"epic_ids": []string{epic}, "include_no_sprint": true}, 200)
	if result.Total != 1 {
		t.Fatalf("null sprint filter: %+v", result)
	}
	fetch(map[string]any{"epic_ids": []string{"invalid"}}, 400)
}

func TestPlanningBatchRejectsUnsupportedRelation(t *testing.T) {
	issue := dbfx.Issue(t, "Batch planning issue")
	testutil.Call(t, testHandler.BatchUpdateIssues, newRequest("PATCH", "/api/issues/batch", map[string]any{"issue_ids": []string{issue}, "updates": map[string]any{"epic_id": nil}})).Want(400)
}
