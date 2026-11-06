import { z } from "zod";
import type { ScreenDoc, ScreenNode } from "./schemas";

// Props of each catalog component as the server stores them. The server has
// already checked them; these schemas only keep a newer or broken record from
// crashing the chat — a node that does not fit is simply not drawn.

const str = z.string().default("");
const strs = z.array(z.string()).default([]);

export const ScreenActionSchema = z.object({
  name: z.string(),
  args: z.record(z.string(), z.unknown()).default({}),
});

export const ScreenButtonSchema = z.object({
  label: z.string(),
  action: ScreenActionSchema,
  zone: z.string().default("normal"),
  action_id: z.string().optional(),
});

const SeriesSchema = z.object({ name: str, values: z.array(z.number()).default([]) });

export const screenPropSchemas = {
  Stack: z.object({ children: strs, direction: z.enum(["column", "row"]).catch("column").default("column") }),
  Text: z.object({ text: str, tone: z.enum(["default", "muted", "heading"]).catch("default").default("default") }),
  KpiStrip: z.object({ items: strs }),
  Kpi: z.object({ label: str, value: str, delta: z.string().optional(), hint: z.string().optional() }),
  TrendChart: z.object({ title: str, labels: strs, series: z.array(SeriesSchema).default([]), unit: z.string().optional() }),
  StackedBars: z.object({ title: str, labels: strs, series: z.array(SeriesSchema).default([]), unit: z.string().optional() }),
  MilestoneCard: z.object({ text: str, evidence: z.string().optional(), at: z.string().optional(), stream: z.string().optional() }),
  AskCard: z.object({
    question: str,
    options: strs,
    ask_id: z.string().optional(),
    default: z.number().optional(),
    zone: z.string().default("normal"),
    option_actions: z.array(z.string()).optional(),
  }),
  StreamCard: z.object({
    stream: str,
    title: str,
    state: str,
    goal: z.string().optional(),
    kpis: z.string().optional(),
    trend: z.string().optional(),
    now: z.string().optional(),
    next: strs,
    link: z.string().optional(),
  }),
  Table: z.object({ columns: strs, rows: z.array(z.array(z.string())).default([]) }),
  IssueRow: z.object({ identifier: str, title: str, status: z.string().optional(), link: z.string().optional() }),
  AgentStack: z.object({ agents: strs, label: z.string().optional() }),
  Actions: z.object({ buttons: z.array(ScreenButtonSchema).default([]) }),
} as const;

export type ScreenComponentName = keyof typeof screenPropSchemas;
export type ScreenProps<N extends ScreenComponentName> = z.infer<(typeof screenPropSchemas)[N]>;
export type ScreenButton = z.infer<typeof ScreenButtonSchema>;
export type ScreenAction = z.infer<typeof ScreenActionSchema>;

export type ReadNode = { [N in ScreenComponentName]: { id: string; name: N; props: ScreenProps<N> } }[ScreenComponentName];

function isComponentName(name: string): name is ScreenComponentName {
  return Object.hasOwn(screenPropSchemas, name);
}

/** A stored node as a known component with checked props, or null. */
export function readNode(node: ScreenNode): ReadNode | null {
  const [name, props] = Object.entries(node.component)[0] ?? [];
  if (!name || !isComponentName(name)) return null;
  const parsed = screenPropSchemas[name].safeParse(props ?? {});
  if (!parsed.success) return null;
  return { id: node.id, name, props: parsed.data } as ReadNode;
}

/** Index of a doc's nodes by id, readable ones only. */
export function indexDoc(doc: ScreenDoc): Map<string, ReadNode> {
  const out = new Map<string, ReadNode>();
  for (const n of doc.components) {
    const r = readNode(n);
    if (r) out.set(n.id, r);
  }
  return out;
}

/** Rows for a recharts chart: one object per label, one key per series. */
export function chartRows(labels: string[], series: { name: string; values: number[] }[]) {
  return labels.map((label, i) => {
    const row: Record<string, string | number> = { label };
    series.forEach((s, j) => {
      row[`s${j}`] = s.values[i] ?? 0;
    });
    return row;
  });
}

/** Money and counts the way the screens write them. */
export function formatAmount(v: number, unit?: string): string {
  const s = Number.isInteger(v) ? String(v) : v.toFixed(2);
  if (unit === "$" || unit === "USD") return `$${s}`;
  return unit ? `${s} ${unit}` : s;
}
