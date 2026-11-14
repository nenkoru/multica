import React, { useEffect, useState } from "react";
import { createRoot } from "react-dom/client";
import { QueryClient, QueryClientProvider } from "@tanstack/react-query";
import { I18nProvider } from "../../packages/core/i18n/react";
import { TaskScreens } from "../../packages/views/streams/genui/task-screens";
import { stripScreenSource, applyScreenLine, applyScreenFinal } from "../../packages/core/streams/screens";
import { streamKeys } from "../../packages/core/streams/queries";
import { ScreenBlockSchema } from "../../packages/core/streams/schemas";
import en from "../../packages/views/locales/en/streams.json";
import original from "../../server/internal/streams/genui/testdata/table-message.md?raw";
import result from "./result.json";
import baseline from "./baseline.json";
import "./style.css";

const client = new QueryClient({ defaultOptions: { queries: { staleTime: Infinity, retry: false } } });
const block = { id: "stored-message", task_id: "task", chat_session_id: "session", seq: 10, kind: "model", status: result.Status, issues: result.Issues?.length ?? 0, created_at: "", a2ui: result.Doc };
const mode = new URLSearchParams(location.search).get("mode") ?? "table";
const key = streamKeys.screens("ws", "session");
let content = original;
let initial = { blocks: [ScreenBlockSchema.parse(block)] };
if (mode === "old") initial = { blocks: [ScreenBlockSchema.parse({ ...block, status: baseline.Status, a2ui: baseline.Doc })] };
if (mode === "unknown" || mode === "broken") {
  content = 'before\n```openui\nroot = Unknown("<script>window.pwned = true</script>")\n```\nafter';
  initial = { blocks: [ScreenBlockSchema.parse({ ...block, a2ui: mode === "broken" ? "bad doc" : { root: "root", components: [{ id: "root", component: { Unknown: {} } }] } })] };
}
if (mode === "missing") initial = { blocks: [] };
const emptyLeaves: Record<string, { name: string; props: Record<string, unknown>; source: string }> = {
  "empty-kpi": { name: "Kpi", props: { label: "", value: "", hint: "tooltip only" }, source: 'Kpi("", "")' },
  "empty-issue": { name: "IssueRow", props: { identifier: "", title: "", link: "/issues/one" }, source: 'IssueRow("", "")' },
  "empty-text": { name: "Text", props: { text: " " }, source: 'Text(" ")' },
  "empty-table": { name: "Table", props: { columns: [" "], rows: [[" "]] }, source: 'Table([" "], [[" "]])' },
  "empty-agents": { name: "AgentStack", props: { agents: [" "] }, source: 'AgentStack([" "])' },
  "empty-actions": { name: "Actions", props: { buttons: [{ label: " ", action: { name: "followup", args: { text: "hidden" } } }] }, source: 'Actions([Button(" ", followup("hidden"))])' },
  "empty-milestone": { name: "MilestoneCard", props: { text: "", stream: "hidden" }, source: 'MilestoneCard("")' },
  "empty-ask": { name: "AskCard", props: { question: " ", options: [" "] }, source: 'AskCard(" ", [" "])' },
  "empty-stream": { name: "StreamCard", props: { title: "", state: "", stream: "hidden" }, source: 'StreamCard("hidden", "", "")' },
  "empty-trend": { name: "TrendChart", props: { title: "", labels: [], series: [{ name: "tooltip only", values: [] }] }, source: 'TrendChart("", [], [Series("tooltip only", [])])' },
  "empty-bars": { name: "StackedBars", props: { title: "", labels: [], series: [] }, source: 'StackedBars("", [], [])' },
};
if (emptyLeaves[mode]) {
  const { name, props, source } = emptyLeaves[mode];
  content = `before\n\`\`\`openui\nroot = ${source}\n\`\`\`\nafter`;
  initial = { blocks: [ScreenBlockSchema.parse({ ...block, status: "valid", a2ui: { root: "root", components: [{ id: "root", component: { [name]: props } }] } })] };
}
if (mode === "order") {
  content = 'before\n```openui\nroot = First()\n```\n```openui\nroot = Second()\n```\nafter';
  initial = { blocks: [ScreenBlockSchema.parse({ ...block, seq: 11, status: baseline.Status, a2ui: baseline.Doc })] };
}
if (mode === "stream") initial = applyScreenLine(undefined, { task_id: "task", chat_session_id: "session", block: 0, line: 0, root: "root", nodes: [{ id: "root", component: { Stack: { children: ["history"] } } }] });
client.setQueryData(key, initial);
function App() {
  const [complete, setComplete] = useState(mode !== "stream");
  useEffect(() => {
    function finish() {
      client.setQueryData(key, applyScreenFinal(initial, { task_id: "task", chat_session_id: "session", block: ScreenBlockSchema.parse(block) }));
      setComplete(true);
    }
    window.addEventListener("complete", finish);
    return () => window.removeEventListener("complete", finish);
  }, []);
  return <main><p data-message>{stripScreenSource(content)}</p><TaskScreens wsId="ws" sessionId="session" taskId="task" content={complete ? content : undefined} /></main>;
}
createRoot(document.getElementById("root")!).render(
  <I18nProvider locale="en" resources={{ en: { streams: en } }}>
    <QueryClientProvider client={client}>
      <App />
    </QueryClientProvider>
  </I18nProvider>,
);
