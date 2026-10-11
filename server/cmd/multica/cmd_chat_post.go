package main

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"os"
	"strings"

	"github.com/spf13/cobra"

	"github.com/multica-ai/multica/server/internal/cli"
	"github.com/multica-ai/multica/server/internal/util"
)

func init() {
	chatCmd.AddCommand(newChatPostCmd())
}

func newChatPostCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use: "post <session-id>", Short: "Post a message into a chat session as this agent, without starting a run",
		Long: `Post one text message into an existing active chat session as the agent
running this task. The message appears in the chat feed on the agent's side,
marked with this agent as its author, and is delivered to the Telegram chat the
session is linked to, if any. No run is started: the session's agent only runs
when the person replies, and is then shown what was posted.

Only an agent task can use this command, and only for a chat session of the
person the task is tied to. The server limits how often one agent may write
into a session and overall; a 429 response carries retry_after_seconds.

The response contains the message_id and a delivery status: "delivered",
"not_bound" (the session has no linked channel — the message is in the feed
only), "unsupported_channel", "unavailable" or "failed". The message stays in
the feed in every case, so never post it again to "fix" delivery. This command
does not retry: after a server error, timeout or lost response, check the
destination before posting again.

Use "multica chat send" instead when the session's agent should answer.`,
		Example: `  multica chat list --title "Daily" --output json
  multica chat post <session-id> --body "Nightly import finished: 1426 issues, 0 errors."`,
		Args: exactArgs(1), RunE: runChatPost,
	}
	cmd.Flags().String("body", "", "Text message (required)")
	cmd.Flags().String("output", "json", "Output format: json or table")
	return cmd
}

func runChatPost(cmd *cobra.Command, args []string) error {
	output, err := chatSessionOutput(cmd)
	if err != nil {
		return err
	}
	sessionID, err := util.ParseUUID(args[0])
	if err != nil {
		return fmt.Errorf("session-id must be a full UUID: %w", err)
	}
	body, _ := cmd.Flags().GetString("body")
	if strings.TrimSpace(body) == "" {
		return fmt.Errorf("--body is required and must contain text")
	}
	client, err := newAPIClient(cmd)
	if err != nil {
		return err
	}
	ctx, cancel := cli.APIContext(context.Background())
	defer cancel()
	var result map[string]any
	path := "/api/chat/sessions/" + util.UUIDToString(sessionID) + "/agent-messages"
	if err := client.PostJSON(ctx, path, map[string]any{"content": body}, &result); err != nil {
		var httpErr *cli.HTTPError
		if !errors.As(err, &httpErr) || httpErr.StatusCode >= http.StatusInternalServerError {
			return cli.WithUserMessage("The message may have been posted. Check the destination before posting again; this command did not retry.", err)
		}
		return fmt.Errorf("post chat message: %w", err)
	}
	if strVal(result, "message_id") == "" {
		return fmt.Errorf("message may have been posted, but the response lacks message_id; check the destination before posting again")
	}
	if output == "json" {
		return cli.PrintJSON(os.Stdout, result)
	}
	delivery, _ := result["delivery"].(map[string]any)
	cli.PrintTable(os.Stdout, []string{"MESSAGE_ID", "DELIVERY", "CHANNEL", "CREATED"}, [][]string{{
		strVal(result, "message_id"), strVal(delivery, "status"), strVal(delivery, "channel_type"), strVal(result, "created_at"),
	}})
	return nil
}
