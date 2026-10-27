package daemon

import "github.com/multica-ai/multica/server/pkg/redact"

// Known-value redaction at the daemon -> server boundary (RUSL-2266).
//
// redact.Text only recognises secrets by shape; a bare token without a known
// prefix (e.g. a hex bearer token echoed by a tool) passes through it. These
// helpers mask the literal values the daemon knows to be secret — *.env files
// under ~/.config and its own secret-named environment — in every text field
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
