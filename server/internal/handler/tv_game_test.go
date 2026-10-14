package handler

import (
	"net/http"
	"testing"
	"time"

	"github.com/multica-ai/multica/server/internal/testutil"
)

func TestTVGameAggregate(t *testing.T) {
	var created CreateKioskTokenResponse
	testutil.Call(t, testHandler.CreateKioskToken, newRequest("POST", "/api/kiosk-tokens", map[string]string{
		"name": "TV game", "owner_label": "Owner",
	})).Want(http.StatusCreated).JSON(&created)
	dbfx.Cleanup(t, `DELETE FROM kiosk_token WHERE id = $1`, created.ID)

	epic := dbfx.Insert(t, "epic", testutil.Cols{"workspace_id": testWorkspaceID, "title": "TV game epic"})
	runtime := dbfx.Runtime(t, "tv game runtime")
	digger := dbfx.Agent(t, "TV digger", runtime, testutil.Cols{"max_concurrent_tasks": 3})
	reviewer := dbfx.Agent(t, "TV carrier", runtime)

	parent := dbfx.Issue(t, "TV game parent", testutil.Cols{"epic_id": epic, "status": "in_progress"})
	child := dbfx.Issue(t, "TV game child", testutil.Cols{"parent_issue_id": parent, "status": "in_progress"})
	dbfx.Task(t, digger, testutil.Cols{"issue_id": child, "status": "running", "runtime_id": runtime, "started_at": time.Now().Add(-time.Minute)})
	dbfx.Task(t, digger, testutil.Cols{"issue_id": parent, "status": "queued", "runtime_id": runtime})

	review := dbfx.Issue(t, "TV game review", testutil.Cols{
		"epic_id": epic, "status": "in_review", "assignee_type": "agent", "assignee_id": reviewer,
	})
	dbfx.Task(t, reviewer, testutil.Cols{"issue_id": review, "status": "completed", "completed_at": time.Now().Add(-time.Hour)})

	failedIssue := dbfx.Issue(t, "TV game failed", testutil.Cols{"epic_id": epic, "status": "todo"})
	failedTask := dbfx.Task(t, reviewer, testutil.Cols{
		"issue_id": failedIssue, "status": "failed", "completed_at": time.Now().Add(-2 * time.Minute), "error": "boom",
	})
	done := dbfx.Issue(t, "TV game done", testutil.Cols{"epic_id": epic, "status": "done"})

	// The game aggregate is behind the kiosk token like the overview.
	testutil.Call(t, testHandler.GetTVGame, tvRequest("GET", "/api/tv/game", "", nil)).Want(http.StatusUnauthorized)

	var got TVGameResponse
	testutil.Call(t, testHandler.GetTVGame, tvRequest("GET", "/api/tv/game", created.Token, nil)).Want(http.StatusOK).JSON(&got)

	if got.HostCeiling != 12 || got.Running < 1 || got.Queued < 1 {
		t.Fatalf("counters: ceiling=%d running=%d queued=%d", got.HostCeiling, got.Running, got.Queued)
	}
	slots := map[string]int32{}
	for _, a := range got.Agents {
		slots[a.ID] = a.Slots
	}
	if slots[digger] != 3 || slots[reviewer] != 1 {
		t.Fatalf("agents: %+v", got.Agents)
	}

	states := map[string]TVGameWork{}
	for _, w := range got.Work {
		states[w.IssueID+"/"+w.AgentID] = w
	}
	// The child's run shows on its top-level parent, with the parent's epic.
	if w, ok := states[parent+"/"+digger]; !ok || w.State != "running" || w.EpicID == nil || *w.EpicID != epic {
		t.Fatalf("running work on parent missing: %+v", got.Work)
	}
	if _, ok := states[child+"/"+digger]; ok {
		t.Fatal("a sub-issue must not show separately")
	}
	if w, ok := states[review+"/"+reviewer]; !ok || w.State != "in_review" {
		t.Fatalf("review work missing: %+v", got.Work)
	}

	var epicRow *TVGameEpic
	for i := range got.Epics {
		if got.Epics[i].ID == epic {
			epicRow = &got.Epics[i]
		}
	}
	if epicRow == nil || epicRow.Open != 3 {
		t.Fatalf("epic: %+v", got.Epics)
	}

	foundDone := false
	for _, d := range got.RecentDone {
		foundDone = foundDone || d.IssueID == done
	}
	if !foundDone {
		t.Fatalf("recent done misses %s: %+v", done, got.RecentDone)
	}
	foundFailed := false
	for _, f := range got.RecentFailed {
		if f.TaskID == failedTask {
			foundFailed = f.IssueID == failedIssue && f.AgentName == "TV carrier" && f.EpicID != nil && *f.EpicID == epic
		}
	}
	if !foundFailed {
		t.Fatalf("recent failed misses %s: %+v", failedTask, got.RecentFailed)
	}
}
