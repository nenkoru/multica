package daemon

import (
	"fmt"

	"github.com/multica-ai/multica/server/pkg/redact"
)

// Known-value redaction at the daemon -> server boundary (RUSL-2266).
//
// redact.Text only recognises secrets by shape; a bare token without a known
// prefix (e.g. a hex bearer token echoed by a tool) passes through it. These
// helpers mask the literal values the daemon knows to be secret — secret-named
// keys of env files under ~/.config (and their backups) and of its own
// environment, its own auth token (Client.SetToken), and for each running task
// its MULTICA_TOKEN and the agent's secret-named custom_env values
// (registerTaskSecretValues, scoped to handleTask) — in every text field
// the daemon reports for a task: transcript messages (text/thinking/error
// Content, tool_result Output, nested tool_use Input), the task result and
// error text, progress summaries, supplement-ack and cancel-ack errors.
// The hooks sit in Client so every caller is covered regardless of provider
// (Claude, Codex, DSH, OpenCode, Pi, ...). MULTICA_REDACT_SECRET_VALUES=0
// turns them into no-ops.

// scrubSecretValues masks known secret values in s.
func scrubSecretValues(s string) string {
	return redact.Values(s)
}

// scrubTaskMessagesSecretValues returns a copy of messages with known secret
// values masked in Content, Output and (recursively) Input. The input slice is
// not modified.
func scrubTaskMessagesSecretValues(messages []TaskMessageData) []TaskMessageData {
	if len(messages) == 0 || !redact.SecretValuesEnabled() {
		return messages
	}
	out := make([]TaskMessageData, len(messages))
	for i, m := range messages {
		m.Content = redact.Values(m.Content)
		m.Output = redact.Values(m.Output)
		if m.Input != nil {
			m.Input = redact.InputMapValues(m.Input)
		}
		out[i] = m
	}
	return out
}

// registerClientTokenSecretValue keeps the client's current auth token (the
// daemon token from the profile config, already in memory) in the known set.
func registerClientTokenSecretValue(c *Client, token string) {
	var values []string
	if token != "" {
		values = []string{token}
	}
	redact.SetNamedValues(fmt.Sprintf("daemon-client-token:%p", c), values)
}

// taskSecretValues lists the secrets the daemon hands a task: its task-scoped
// MULTICA_TOKEN and the agent's custom_env values under secret-named keys (or
// URL passwords) — the same key filter as for env files, so a custom_env model
// name or path is not masked.
func taskSecretValues(task Task) []string {
	var values []string
	if tok := task.AuthToken; tok != "" {
		values = append(values, tok)
	}
	if task.Agent != nil && len(task.Agent.CustomEnv) > 0 {
		values = append(values, redact.SecretValuesFromPairs(task.Agent.CustomEnv)...)
	}
	return values
}

// registerTaskSecretValues adds taskSecretValues(task) to the known set and
// returns the function that removes them again.
func registerTaskSecretValues(task Task) (unregister func()) {
	values := taskSecretValues(task)
	if len(values) == 0 {
		return func() {}
	}
	return redact.RegisterValues(values)
}
