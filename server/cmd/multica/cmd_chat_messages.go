package main

import (
	"context"
	"fmt"
	"net/url"
	"os"
	"strconv"
	"strings"

	"github.com/spf13/cobra"

	"github.com/multica-ai/multica/server/internal/cli"
	"github.com/multica-ai/multica/server/internal/util"
)

func init() {
	chatCmd.AddCommand(newChatMessagesCmd())
}

// newChatMessagesCmd reads one of the caller's own chat sessions by id — the
// same page the web composer loads. "chat history" and "chat thread" only read
// the conversation an agent task runs for; a member-side process (the voice
// service of RUSL-1806) needs the recent messages of a named session instead.
func newChatMessagesCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use: "messages <session-id>", Short: "Read the recent messages of one of your chat sessions",
		Long: `Read the most recent messages of an existing chat session using its full UUID,
oldest first, exactly as the web chat shows them. The server checks workspace
membership and that the session belongs to the authenticated user; this does not
read other members' chats.

The page holds at most --limit messages (1-100, default 20). When has_more is
true, pass next_cursor's created_at and id as --before-created-at and --before-id
to read the page before it.`,
		Example: `  multica chat list --output json
  multica chat messages <session-id> --limit 20 --output json`,
		Args: exactArgs(1), RunE: runChatMessages,
	}
	cmd.Flags().Int("limit", 20, "Maximum number of messages (1-100)")
	cmd.Flags().String("before-created-at", "", "Cursor: created_at of next_cursor from the previous page")
	cmd.Flags().String("before-id", "", "Cursor: id of next_cursor from the previous page")
	cmd.Flags().String("output", "json", "Output format: json or table")
	return cmd
}

func runChatMessages(cmd *cobra.Command, args []string) error {
	output, err := chatSessionOutput(cmd)
	if err != nil {
		return err
	}
	sessionID, err := util.ParseUUID(args[0])
	if err != nil {
		return fmt.Errorf("session-id must be a full UUID: %w", err)
	}
	limit, _ := cmd.Flags().GetInt("limit")
	if limit < 1 || limit > 100 {
		return fmt.Errorf("--limit must be between 1 and 100")
	}
	beforeAt, _ := cmd.Flags().GetString("before-created-at")
	beforeID, _ := cmd.Flags().GetString("before-id")
	if (beforeAt == "") != (beforeID == "") {
		return fmt.Errorf("--before-created-at and --before-id go together")
	}
	q := url.Values{}
	q.Set("limit", strconv.Itoa(limit))
	if beforeAt != "" {
		q.Set("before_created_at", beforeAt)
		q.Set("before_id", beforeID)
	}
	client, err := newAPIClient(cmd)
	if err != nil {
		return err
	}
	ctx, cancel := cli.APIContext(context.Background())
	defer cancel()
	var page map[string]any
	path := "/api/chat/sessions/" + util.UUIDToString(sessionID) + "/messages/page?" + q.Encode()
	if err := client.GetJSON(ctx, path, &page); err != nil {
		return fmt.Errorf("read chat messages: %w", err)
	}
	if output == "json" {
		return cli.PrintJSON(os.Stdout, page)
	}
	messages, _ := page["messages"].([]any)
	rows := make([][]string, 0, len(messages))
	for _, raw := range messages {
		m, _ := raw.(map[string]any)
		content := strings.Join(strings.Fields(strVal(m, "content")), " ")
		if r := []rune(content); len(r) > 80 {
			content = string(r[:80]) + "…"
		}
		rows = append(rows, []string{strVal(m, "created_at"), strVal(m, "role"), content})
	}
	cli.PrintTable(os.Stdout, []string{"CREATED", "ROLE", "CONTENT"}, rows)
	return nil
}
