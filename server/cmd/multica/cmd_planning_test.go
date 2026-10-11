package main

import (
	"encoding/json"
	"github.com/spf13/cobra"
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestPlanningCLIContracts(t *testing.T) {
	const id = "00000000-0000-0000-0000-000000000001"
	for _, noun := range []string{"epic", "sprint"} {
		t.Run(noun, func(t *testing.T) {
			for _, action := range []string{"list", "get", "create", "update", "delete"} {
				t.Run(action, func(t *testing.T) {
					var method, path string
					var body map[string]any
					srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
						method, path = r.Method, r.URL.Path
						if r.Header.Get("X-Workspace-ID") != id {
							t.Error("missing workspace binding")
						}
						if r.Body != nil {
							_ = json.NewDecoder(r.Body).Decode(&body)
						}
						if action == "delete" {
							w.WriteHeader(204)
							return
						}
						if action == "list" {
							json.NewEncoder(w).Encode(map[string]any{noun + "s": []any{}, "total": 0})
							return
						}
						json.NewEncoder(w).Encode(map[string]any{"id": id, "title": "Epic", "name": "Sprint", "status": "planned"})
					}))
					defer srv.Close()
					t.Setenv("MULTICA_SERVER_URL", srv.URL)
					t.Setenv("MULTICA_WORKSPACE_ID", id)
					t.Setenv("MULTICA_TOKEN", "mat_planning-test")
					root := newPlanningCommand(noun)
					cmd, _, err := root.Find([]string{action})
					if err != nil {
						t.Fatal(err)
					}
					args := []string{}
					if action == "get" || action == "update" || action == "delete" {
						args = append(args, id)
					}
					if action == "create" || action == "update" {
						name := "title"
						if noun == "sprint" {
							name = "name"
						}
						cmd.Flags().Set(name, "Planning")
						if action == "create" && noun == "sprint" {
							cmd.Flags().Set("start-date", "2026-10-09")
							cmd.Flags().Set("end-date", "2026-10-16")
						}
						if action == "update" {
							field := "description"
							if noun == "sprint" {
								field = "goal"
							}
							cmd.Flags().Set(field, "")
						}
					}
					if err := runPlanning(cmd, args, noun, action); err != nil {
						t.Fatal(err)
					}
					wantMethod := map[string]string{"list": "GET", "get": "GET", "create": "POST", "update": "PUT", "delete": "DELETE"}[action]
					wantPath := "/api/" + noun + "s"
					if len(args) > 0 {
						wantPath += "/" + id
					}
					if method != wantMethod || path != wantPath {
						t.Fatalf("request %s %s; want %s %s", method, path, wantMethod, wantPath)
					}
					if action == "update" {
						field := "description"
						if noun == "sprint" {
							field = "goal"
						}
						v, exists := body[field]
						if !exists || v != nil {
							t.Errorf("clear %s did not send null: %#v", field, body)
						}
					}
				})
			}
		})
	}
}

func TestPlanningCLIRejectsInvalidDatesBeforeRequest(t *testing.T) {
	root := newPlanningCommand("sprint")
	cmd, _, _ := root.Find([]string{"create"})
	cmd.Flags().Set("name", "Sprint")
	cmd.Flags().Set("start-date", "2026-10-16")
	cmd.Flags().Set("end-date", "2026-10-09")
	if _, err := planningBody(cmd, "sprint", "create"); err == nil {
		t.Fatal("reversed dates accepted")
	}
	cmd.Flags().Set("start-date", "invalid")
	if _, err := planningBody(cmd, "sprint", "create"); err == nil {
		t.Fatal("malformed date accepted")
	}
}

func TestPlanningCLIIssueRelations(t *testing.T) {
	const id = "11111111-1111-4111-8111-111111111111"
	for _, action := range []string{"create", "update", "list"} {
		t.Run(action, func(t *testing.T) {
			var body map[string]any
			var epicFilter, sprintFilter string
			srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				if r.URL.Path == "/api/config" {
					json.NewEncoder(w).Encode(map[string]any{"planning_enabled": true})
					return
				}
				epicFilter, sprintFilter = r.URL.Query().Get("epic_id"), r.URL.Query().Get("sprint_id")
				if r.Method == "POST" || r.Method == "PUT" {
					_ = json.NewDecoder(r.Body).Decode(&body)
				}
				if action == "list" {
					json.NewEncoder(w).Encode(map[string]any{"issues": []any{}, "total": 0})
					return
				}
				json.NewEncoder(w).Encode(map[string]any{"id": id, "identifier": "TEST-1", "title": "Issue"})
			}))
			defer srv.Close()
			t.Setenv("MULTICA_SERVER_URL", srv.URL)
			t.Setenv("MULTICA_WORKSPACE_ID", id)
			t.Setenv("MULTICA_TOKEN", "mat_planning-test")
			t.Setenv("MULTICA_QUICK_CREATE_TASK_ID", "")
			var cmd *cobra.Command
			switch action {
			case "create":
				cmd = newIssueCreateTestCmd()
				cmd.Flags().Set("title", "Issue")
			case "update":
				cmd = newIssueUpdateTestCmd()
			case "list":
				cmd = newIssueListTestCmd()
			}
			cmd.Flags().String("epic", "", "")
			cmd.Flags().String("sprint", "", "")
			cmd.Flags().Set("epic", id)
			sprint := id
			if action == "update" {
				sprint = ""
			}
			cmd.Flags().Set("sprint", sprint)
			var err error
			switch action {
			case "create":
				err = runIssueCreate(cmd, nil)
			case "update":
				err = runIssueUpdate(cmd, []string{id})
			case "list":
				err = runIssueList(cmd, nil)
			}
			if err != nil {
				t.Fatal(err)
			}
			if action == "list" {
				if epicFilter != id || sprintFilter != id {
					t.Fatalf("missing filters: %s %s", epicFilter, sprintFilter)
				}
				return
			}
			if body["epic_id"] != id {
				t.Fatalf("missing epic relation: %#v", body)
			}
			if action == "update" {
				value, exists := body["sprint_id"]
				if !exists || value != nil {
					t.Fatalf("clear did not send null: %#v", body)
				}
			} else if body["sprint_id"] != id {
				t.Fatalf("missing sprint relation: %#v", body)
			}
		})
	}
}

func TestPlanningCLIFailsClosedOnOldServer(t *testing.T) {
	posts := 0
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != "GET" {
			posts++
		}
		json.NewEncoder(w).Encode(map[string]any{})
	}))
	defer srv.Close()
	t.Setenv("MULTICA_SERVER_URL", srv.URL)
	t.Setenv("MULTICA_WORKSPACE_ID", "11111111-1111-4111-8111-111111111111")
	t.Setenv("MULTICA_TOKEN", "mat_planning-test")
	cmd := newIssueCreateTestCmd()
	cmd.Flags().String("epic", "", "")
	cmd.Flags().Set("epic", "11111111-1111-4111-8111-111111111111")
	cmd.Flags().Set("title", "Old server")
	if err := runIssueCreate(cmd, nil); err == nil {
		t.Fatal("old server silently ignored relation")
	}
	if posts != 0 {
		t.Fatal("created issue before checking support")
	}
	cmd.Flags().Set("epic", "invalid")
	if err := runIssueCreate(cmd, nil); err == nil {
		t.Fatal("invalid UUID accepted")
	}
}
