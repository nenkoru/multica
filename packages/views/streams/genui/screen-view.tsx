"use client";

import { useMemo, type ReactNode } from "react";
import { Bar, BarChart, CartesianGrid, Line, LineChart, XAxis, YAxis } from "recharts";
import { CircleCheck, ExternalLink, MessageCircleQuestion } from "@multica/ui/icons";
import { cn } from "@multica/ui/lib/utils";
import {
  ChartContainer,
  ChartTooltip,
  ChartTooltipContent,
  type ChartConfig,
} from "@multica/ui/components/ui/chart";
import {
  LIVE_SCREEN_STATUS,
  chartRows,
  formatAmount,
  indexDoc,
  type ReadNode,
  type ScreenBlock,
  type ScreenProps,
} from "@multica/core/streams";
import { useT } from "../../i18n";
import { StatusIcon } from "../../issues/components/status-icon";
import { streamStateDotClass } from "../components/stream-state";
import { ScreenActionButton } from "./screen-actions";
import { ScreenEnvContext, type ScreenEnv } from "./screen-context";

const MAX_DEPTH = 12;
const CHART_COLORS = ["var(--chart-1)", "var(--chart-2)", "var(--chart-3)", "var(--chart-4)", "var(--chart-5)"];
// Each node fades in as its line arrives; nodes already shown keep their key
// and do not animate again.
const APPEAR = "animate-in fade-in slide-in-from-bottom-1 duration-300 motion-reduce:animate-none";

type Nodes = Map<string, ReadNode>;

/** One screen from Мультика's reply, drawn while it is written and live
 *  once it is stored. */
export function ScreenView({ block, onFollowup }: { block: ScreenBlock; onFollowup?: (text: string) => void }) {
  const { t } = useT("streams");
  const doc = block.a2ui;
  const nodes = useMemo(() => (doc ? indexDoc(doc) : new Map<string, ReadNode>()), [doc]);
  const env = useMemo<ScreenEnv>(
    () => ({ interactive: block.status !== LIVE_SCREEN_STATUS, onFollowup }),
    [block.status, onFollowup],
  );
  if (block.status === "failed" || !doc) {
    return <p className="text-caption text-muted-foreground">{t(($) => $.screen.failed)}</p>;
  }
  return (
    <ScreenEnvContext.Provider value={env}>
      <div className="flex flex-col gap-3 rounded-lg border border-border bg-card p-3 text-card-foreground" data-screen={block.id}>
        <NodeView id={doc.root} nodes={nodes} path={[]} />
      </div>
    </ScreenEnvContext.Provider>
  );
}

function NodeView({ id, nodes, path }: { id: string; nodes: Nodes; path: string[] }) {
  const node = nodes.get(id);
  // A node not written yet (live), unknown, or a reference loop.
  if (!node || path.includes(id) || path.length > MAX_DEPTH) return null;
  const sub = [...path, id];
  return <div className={APPEAR}>{renderNode(node, nodes, sub)}</div>;
}

function renderNode(node: ReadNode, nodes: Nodes, path: string[]): ReactNode {
  switch (node.name) {
    case "Stack":
      return (
        <div className={cn("flex gap-3", node.props.direction === "row" ? "flex-row flex-wrap" : "flex-col")}>
          {node.props.children.map((c) => (
            <NodeView key={c} id={c} nodes={nodes} path={path} />
          ))}
        </div>
      );
    case "Text":
      return <ScreenText {...node.props} />;
    case "KpiStrip":
      return (
        <div className="grid grid-cols-[repeat(auto-fit,minmax(8rem,1fr))] gap-2">
          {node.props.items.map((c) => (
            <NodeView key={c} id={c} nodes={nodes} path={path} />
          ))}
        </div>
      );
    case "Kpi":
      return <Kpi {...node.props} />;
    case "TrendChart":
      return <Chart kind="line" {...node.props} />;
    case "StackedBars":
      return <Chart kind="bars" {...node.props} />;
    case "MilestoneCard":
      return <Milestone {...node.props} />;
    case "AskCard":
      return <Ask {...node.props} />;
    case "StreamCard":
      return <StreamCard props={node.props} nodes={nodes} path={path} />;
    case "Table":
      return <ScreenTable {...node.props} />;
    case "IssueRow":
      return <IssueRow {...node.props} />;
    case "AgentStack":
      return (
        <p className="text-caption text-muted-foreground">
          {node.props.label ? `${node.props.label}: ` : ""}
          <span className="text-foreground">{node.props.agents.join(", ")}</span>
        </p>
      );
    case "Actions":
      return (
        <div className="flex flex-wrap gap-2">
          {node.props.buttons.map((b, i) => (
            <ScreenActionButton key={i} label={b.label} actionId={b.action_id} action={b.action} zone={b.zone} />
          ))}
        </div>
      );
  }
}

function ScreenText({ text, tone }: ScreenProps<"Text">) {
  if (tone === "heading") return <h3 className="text-title-sm font-semibold">{text}</h3>;
  return <p className={cn("text-body whitespace-pre-wrap", tone === "muted" && "text-muted-foreground")}>{text}</p>;
}

function Kpi({ label, value, delta, hint }: ScreenProps<"Kpi">) {
  return (
    <div className="rounded-md bg-muted/50 px-3 py-2" title={hint}>
      <div className="truncate text-caption text-muted-foreground">{label}</div>
      <div className="font-mono text-title-sm font-semibold tabular-nums">{value}</div>
      {delta ? <div className="text-caption text-muted-foreground">{delta}</div> : null}
    </div>
  );
}

function Chart({ kind, title, labels, series, unit }: ScreenProps<"TrendChart"> & { kind: "line" | "bars" }) {
  const config = useMemo(
    () =>
      Object.fromEntries(
        series.map((s, i) => [`s${i}`, { label: s.name, color: CHART_COLORS[i % CHART_COLORS.length] }]),
      ) satisfies ChartConfig,
    [series],
  );
  const rows = useMemo(() => chartRows(labels, series), [labels, series]);
  const fmt = (v: number) => formatAmount(v, unit);
  const tooltip = (
    <ChartTooltip
      content={
        <ChartTooltipContent
          formatter={(value, name) => `${typeof value === "number" ? fmt(value) : value} ${config[String(name)]?.label ?? name}`}
        />
      }
    />
  );
  const axes = (
    <>
      <CartesianGrid vertical={false} />
      <XAxis dataKey="label" tickLine={false} axisLine={false} tickMargin={8} interval="preserveStartEnd" />
      <YAxis tickLine={false} axisLine={false} tickMargin={8} width="auto" tickFormatter={(v: number) => fmt(v)} />
    </>
  );
  return (
    <figure className="flex flex-col gap-2">
      <figcaption className="text-label font-medium">{title}</figcaption>
      <ChartContainer config={config} className="aspect-[3/1] w-full">
        {kind === "line" ? (
          <LineChart data={rows} margin={{ left: 0, right: 8, top: 4, bottom: 0 }}>
            {axes}
            {tooltip}
            {series.map((_, i) => (
              <Line key={i} dataKey={`s${i}`} type="monotone" stroke={`var(--color-s${i})`} strokeWidth={2} dot={false} />
            ))}
          </LineChart>
        ) : (
          <BarChart data={rows} margin={{ left: 0, right: 0, top: 4, bottom: 0 }}>
            {axes}
            {tooltip}
            {series.map((_, i) => (
              <Bar key={i} dataKey={`s${i}`} stackId="a" fill={`var(--color-s${i})`} />
            ))}
          </BarChart>
        )}
      </ChartContainer>
      {series.length > 1 ? (
        <div className="flex flex-wrap gap-3 text-caption text-muted-foreground">
          {series.map((s, i) => (
            <span key={i} className="inline-flex items-center gap-1.5">
              <span className="size-2 rounded-sm" style={{ background: CHART_COLORS[i % CHART_COLORS.length] }} />
              {s.name}
            </span>
          ))}
        </div>
      ) : null}
    </figure>
  );
}

function Milestone({ text, evidence, at }: ScreenProps<"MilestoneCard">) {
  const { t } = useT("streams");
  return (
    <div className="flex gap-2 rounded-md border border-border px-3 py-2">
      <CircleCheck className="mt-0.5 size-4 shrink-0 text-success" />
      <div className="min-w-0 flex-1">
        <p className="text-body whitespace-pre-wrap">{text}</p>
        <div className="flex flex-wrap gap-3 text-caption text-muted-foreground">
          {at ? <span>{at}</span> : null}
          {evidence ? (
            <a href={evidence} target="_blank" rel="noopener noreferrer" className="inline-flex items-center gap-1 hover:text-foreground">
              {t(($) => $.screen.evidence)}
              <ExternalLink className="size-3" />
            </a>
          ) : null}
        </div>
      </div>
    </div>
  );
}

function Ask({ question, options, option_actions, zone }: ScreenProps<"AskCard">) {
  return (
    <div className="flex flex-col gap-2 rounded-md border border-info/40 bg-info/5 px-3 py-2">
      <div className="flex gap-2">
        <MessageCircleQuestion className="mt-0.5 size-4 shrink-0 text-info" />
        <p className="text-body whitespace-pre-wrap">{question}</p>
      </div>
      <div className="flex flex-wrap gap-2">
        {options.map((o, i) => (
          <ScreenActionButton key={i} label={o} actionId={option_actions?.[i]} zone={zone} />
        ))}
      </div>
    </div>
  );
}

function StreamCard({ props, nodes, path }: { props: ScreenProps<"StreamCard">; nodes: Nodes; path: string[] }) {
  const { t } = useT("streams");
  const stateWord = useStateWord(props.state);
  return (
    <div className="flex flex-col gap-3">
      <div className="flex items-center gap-2">
        <span className={cn("size-2 shrink-0 rounded-full", streamStateDotClass(props.state))} />
        <h3 className="min-w-0 truncate text-title-sm font-semibold">{props.title}</h3>
        <span className="text-caption text-muted-foreground">{stateWord}</span>
        {props.link ? (
          <a href={props.link} target="_blank" rel="noopener noreferrer" className="ml-auto text-muted-foreground hover:text-foreground">
            <ExternalLink className="size-4" />
          </a>
        ) : null}
      </div>
      {props.goal ? <Labeled label={t(($) => $.screen.goal)}>{props.goal}</Labeled> : null}
      {props.kpis ? <NodeView id={props.kpis} nodes={nodes} path={path} /> : null}
      {props.trend ? <NodeView id={props.trend} nodes={nodes} path={path} /> : null}
      {props.now ? <Labeled label={t(($) => $.screen.now)}>{props.now}</Labeled> : null}
      {props.next.length > 0 ? <Labeled label={t(($) => $.screen.next)}>{props.next.join("; ")}</Labeled> : null}
    </div>
  );
}

function useStateWord(state: string): string {
  const { t } = useT("streams");
  switch (state) {
    case "running":
      return t(($) => $.screen.state.running);
    case "waiting":
      return t(($) => $.screen.state.waiting);
    case "asking":
      return t(($) => $.screen.state.asking);
    case "budget":
      return t(($) => $.screen.state.budget);
    case "paused":
      return t(($) => $.screen.state.paused);
    case "broken":
      return t(($) => $.screen.state.broken);
    case "milestone":
      return t(($) => $.screen.state.milestone);
    default:
      return state;
  }
}

function Labeled({ label, children }: { label: string; children: ReactNode }) {
  return (
    <p className="text-body">
      <span className="text-muted-foreground">{label}: </span>
      {children}
    </p>
  );
}

function ScreenTable({ columns, rows }: ScreenProps<"Table">) {
  return (
    <div className="overflow-x-auto">
      <table className="w-full text-label">
        <thead>
          <tr className="text-left text-muted-foreground">
            {columns.map((c, i) => (
              <th key={i} className="px-2 py-1 font-medium">
                {c}
              </th>
            ))}
          </tr>
        </thead>
        <tbody>
          {rows.map((r, i) => (
            <tr key={i} className="border-t border-border/60">
              {r.map((cell, j) => (
                <td key={j} className="px-2 py-1 tabular-nums">
                  {cell}
                </td>
              ))}
            </tr>
          ))}
        </tbody>
      </table>
    </div>
  );
}

function IssueRow({ identifier, title, status, link }: ScreenProps<"IssueRow">) {
  const body = (
    <>
      {status ? <StatusIcon status={status} className="size-4 shrink-0" /> : null}
      <span className="shrink-0 text-muted-foreground">{identifier}</span>
      <span className="min-w-0 truncate">{title}</span>
    </>
  );
  return link ? (
    <a href={link} target="_blank" rel="noopener noreferrer" className="flex items-center gap-2 text-label hover:underline">
      {body}
    </a>
  ) : (
    <div className="flex items-center gap-2 text-label">{body}</div>
  );
}
