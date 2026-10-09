package main

import (
	"context"
	"fmt"
	"net/url"
	"os"
	"strings"
	"time"

	"github.com/multica-ai/multica/server/internal/cli"
	"github.com/multica-ai/multica/server/internal/util"
	"github.com/spf13/cobra"
)

func newPlanningCommand(noun string) *cobra.Command {
	parent := &cobra.Command{Use: noun, Short: "Manage workspace " + noun + "s", GroupID: groupCore}
	for _, action := range []string{"list", "get", "create", "update", "delete"} {
		cmd := &cobra.Command{Use: action, Short: action + " " + noun, Args: exactArgs(0)}
		if action == "get" || action == "update" || action == "delete" {
			cmd.Use += " <uuid>"
			cmd.Args = exactArgs(1)
		}
		format := "json"
		if action == "list" {
			format = "table"
		}
		cmd.Flags().String("output", format, "Output format: table or json")
		cmd.Flags().Bool("full-id", false, "Show full UUIDs in table output")
		if action == "list" || action == "update" {
			cmd.Flags().String("status", "", "Lifecycle status")
		}
		if action == "create" || action == "update" {
			fields := []string{"title", "description", "color"}
			if noun == "sprint" {
				fields = []string{"name", "goal", "start-date", "end-date"}
			}
			for _, f := range fields {
				cmd.Flags().String(f, "", f)
			}
		}
		cmd.RunE = func(cmd *cobra.Command, args []string) error { return runPlanning(cmd, args, noun, action) }
		parent.AddCommand(cmd)
	}
	return parent
}

func planningBody(cmd *cobra.Command, noun, action string) (map[string]any, error) {
	body := map[string]any{}
	fields := []string{"title", "description", "color", "status"}
	name := "title"
	if noun == "sprint" {
		name = "name"
		fields = []string{"name", "goal", "start-date", "end-date", "status"}
	}
	for _, field := range fields {
		if cmd.Flags().Lookup(field) == nil || !cmd.Flags().Changed(field) {
			continue
		}
		v, _ := cmd.Flags().GetString(field)
		if field == name && strings.TrimSpace(v) == "" {
			return nil, fmt.Errorf("--%s must not be empty", field)
		}
		if field == "status" {
			allowed := []string{"open", "closed"}
			if noun == "sprint" {
				allowed = []string{"planned", "active", "completed", "cancelled"}
			}
			if err := validateIssueEnum("status", v, allowed); err != nil {
				return nil, err
			}
		}
		if field == "start-date" || field == "end-date" {
			if _, err := time.Parse("2006-01-02", v); err != nil {
				return nil, fmt.Errorf("--%s must use YYYY-MM-DD", field)
			}
		}
		key := strings.ReplaceAll(field, "-", "_")
		if (field == "description" || field == "goal") && v == "" {
			body[key] = nil
		} else {
			body[key] = v
		}
	}
	if action == "create" {
		if _, ok := body[name]; !ok {
			return nil, fmt.Errorf("--%s is required", name)
		}
		if noun == "sprint" {
			for _, f := range []string{"start_date", "end_date"} {
				if _, ok := body[f]; !ok {
					return nil, fmt.Errorf("--%s is required", strings.ReplaceAll(f, "_", "-"))
				}
			}
		}
	}
	if start, ok := body["start_date"].(string); ok {
		if end, ok := body["end_date"].(string); ok && end <= start {
			return nil, fmt.Errorf("end date must be after start date")
		}
	}
	if action == "update" && len(body) == 0 {
		return nil, fmt.Errorf("supply at least one field to update")
	}
	return body, nil
}

func runPlanning(cmd *cobra.Command, args []string, noun, action string) error {
	format, _ := cmd.Flags().GetString("output")
	if format != "json" && format != "table" {
		return fmt.Errorf("--output must be table or json")
	}
	body := map[string]any{}
	var err error
	if action == "create" || action == "update" {
		body, err = planningBody(cmd, noun, action)
		if err != nil {
			return err
		}
	}
	path := "/api/" + noun + "s"
	if len(args) > 0 {
		if _, err := util.ParseUUID(args[0]); err != nil {
			return fmt.Errorf("%s id must be a UUID", noun)
		}
		path += "/" + url.PathEscape(args[0])
	}
	if action == "list" {
		status, _ := cmd.Flags().GetString("status")
		if status != "" {
			path += "?status=" + url.QueryEscape(status)
		}
	}
	client, err := newAPIClient(cmd)
	if err != nil {
		return err
	}
	ctx, cancel := cli.APIContext(context.Background())
	defer cancel()
	var result map[string]any
	switch action {
	case "list", "get":
		err = client.GetJSON(ctx, path, &result)
	case "create":
		err = client.PostJSON(ctx, path, body, &result)
	case "update":
		err = client.PutJSON(ctx, path, body, &result)
	case "delete":
		err = client.DeleteJSON(ctx, path)
		result = map[string]any{"deleted": args[0]}
	}
	if err != nil {
		return fmt.Errorf("%s %s: %w", action, noun, err)
	}
	if action == "list" {
		items, ok := result[noun+"s"].([]any)
		if !ok {
			return fmt.Errorf("invalid %s list response", noun)
		}
		if format == "json" {
			return cli.PrintJSON(os.Stdout, items)
		}
		rows := [][]string{}
		full, _ := cmd.Flags().GetBool("full-id")
		for _, item := range items {
			v, ok := item.(map[string]any)
			if !ok {
				return fmt.Errorf("invalid %s list item", noun)
			}
			name := strVal(v, "title")
			if noun == "sprint" {
				name = strVal(v, "name")
			}
			rows = append(rows, []string{displayID(strVal(v, "id"), full), name, strVal(v, "status"), fmt.Sprint(v["issue_count"]), fmt.Sprint(v["done_count"])})
		}
		cli.PrintTable(os.Stdout, []string{"ID", "NAME", "STATUS", "ISSUES", "DONE"}, rows)
		return nil
	}
	if format == "json" {
		return cli.PrintJSON(os.Stdout, result)
	}
	if action == "delete" {
		fmt.Fprintf(os.Stdout, "Deleted %s %s\n", noun, args[0])
		return nil
	}
	name := strVal(result, "title")
	if noun == "sprint" {
		name = strVal(result, "name")
	}
	cli.PrintTable(os.Stdout, []string{"ID", "NAME", "STATUS"}, [][]string{{strVal(result, "id"), name, strVal(result, "status")}})
	return nil
}
