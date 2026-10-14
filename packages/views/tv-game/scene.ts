import { isStale, type TvGameSnapshot, type TvGameWorkState } from "./model";

// Pure layout of the map strip: where the base, the crystal fields and every
// worker stand at animation time `t`. The canvas renderer only draws what this
// returns, so the mapping from work to pictures is unit-tested here.

export const MAX_FIELDS = 6;
export const NO_EPIC_FIELD = "__no_epic__";
/** Carrying a load from a field to the base takes this long, then repeats. */
export const CARRY_LOOP_MS = 6_000;

export type UnitMode = "idle" | "mining" | "carrying" | "blocked";

export interface SceneField {
  id: string;
  label: string;
  x: number;
  y: number;
  /** 1..5 crystals, from open issues. */
  crystals: number;
  /** 0 untouched .. 1 mined out, from done in sprint vs open. */
  depletion: number;
  overgrown: boolean;
}

export interface SceneUnit {
  id: string;
  agentId: string;
  label: string;
  issueId: string | null;
  x: number;
  y: number;
  mode: UnitMode;
  /** Facing right (towards the fields) or left (towards the base). */
  facingRight: boolean;
}

export interface SceneWreck {
  issueId: string;
  label: string;
  x: number;
  y: number;
  /** 0 just crashed .. 1 about to disappear. */
  age: number;
}

export interface SceneModel {
  width: number;
  height: number;
  base: { x: number; y: number };
  fields: SceneField[];
  units: SceneUnit[];
  wrecks: SceneWreck[];
  /** Work that did not fit on the map (more runs than drawn slots). */
  hiddenWork: number;
}

export interface SceneWreckInput {
  issueId: string;
  title: string;
  epicId: string | null;
  agentId: string;
  at: number;
}

const MODE_OF: Record<TvGameWorkState, UnitMode> = {
  running: "mining",
  in_review: "carrying",
  blocked: "blocked",
};

export function truncate(text: string, max: number): string {
  return text.length <= max ? text : `${text.slice(0, max - 1)}…`;
}

function hash(text: string): number {
  let h = 0;
  for (let i = 0; i < text.length; i++) h = (h * 31 + text.charCodeAt(i)) | 0;
  return Math.abs(h);
}

export interface BuildSceneOptions {
  width: number;
  height: number;
  /** Animation time, ms; 0 for the static (reduced-motion) picture. */
  t: number;
  /** Wall clock, epoch ms; ages the wrecks. */
  now: number;
  wrecks?: SceneWreckInput[];
  wreckLifetimeMs?: number;
  noEpicLabel?: string;
  /** Share of the width kept free for the alarm plates. */
  reserveRight?: number;
}

export function buildScene(snapshot: TvGameSnapshot, options: BuildSceneOptions): SceneModel {
  const {
    width,
    height,
    t,
    now,
    wrecks = [],
    wreckLifetimeMs = 60_000,
    noEpicLabel = "—",
    reserveRight = 0.3,
  } = options;
  // Rows, top to bottom: field labels, crystals, workers, wrecks.
  const crystalY = Math.round(height * 0.44);
  const workerY = Math.round(height * 0.7);
  const wreckY = Math.round(height * 0.9);
  const base = { x: Math.round(width * 0.07), y: Math.round(height * 0.5) };

  // Epics with work on the map come first, then the busiest by open issues.
  const busy = new Set(snapshot.work.map((w) => w.epicId ?? NO_EPIC_FIELD));
  const epics = [...snapshot.epics]
    .filter((e) => e.open > 0 || busy.has(e.id))
    .sort((a, b) => Number(busy.has(b.id)) - Number(busy.has(a.id)) || b.open - a.open);
  const hasNoEpic = busy.has(NO_EPIC_FIELD);
  const slots = MAX_FIELDS - (hasNoEpic ? 1 : 0);
  const shown = epics.slice(0, slots);

  const fieldInputs = shown.map((e) => ({
    id: e.id,
    label: truncate(e.title, 16),
    open: e.open,
    done: e.doneInSprint,
    overgrown: isStale(e.lastActivityAt, now),
  }));
  if (hasNoEpic) fieldInputs.push({ id: NO_EPIC_FIELD, label: noEpicLabel, open: 1, done: 0, overgrown: false });

  // The right part of the strip is left to the alarm plates.
  const left = width * 0.22;
  const right = width * (1 - reserveRight);
  const n = fieldInputs.length;
  const gap = n > 0 ? (right - left) / n : 0;
  const fields: SceneField[] = fieldInputs.map((f, i) => ({
    id: f.id,
    label: f.label,
    x: Math.round(left + gap * (i + 0.5)),
    y: crystalY,
    crystals: Math.max(1, Math.min(4, Math.ceil(Math.sqrt(f.open)))),
    depletion: f.open + f.done === 0 ? 0 : f.done / (f.open + f.done),
    overgrown: f.overgrown,
  }));
  const fieldById = new Map(fields.map((f) => [f.id, f]));

  // Who works where, before positions: workers at one field share its row.
  type Placed = { id: string; agentId: string; label: string; work: (typeof snapshot.work)[number] | null };
  const atField = new Map<string, Placed[]>();
  const idle: Placed[] = [];
  let hiddenWork = 0;
  for (const agent of snapshot.agents) {
    const mine = snapshot.work.filter((w) => w.agentId === agent.id);
    const slotCount = Math.max(1, agent.slots);
    hiddenWork += Math.max(0, mine.length - slotCount);
    const label = truncate(agent.name, 11);
    const drawn = mine.slice(0, slotCount).filter((work) => {
      if (fieldById.has(work.epicId ?? NO_EPIC_FIELD)) return true;
      hiddenWork += 1;
      return false;
    });
    if (drawn.length === 0) {
      // An agent with nothing on the map shows one worker at the base.
      idle.push({ id: `${agent.id}:0`, agentId: agent.id, label, work: null });
      continue;
    }
    drawn.forEach((work, slot) => {
      const key = work.epicId ?? NO_EPIC_FIELD;
      const list = atField.get(key) ?? [];
      list.push({ id: `${agent.id}:${slot}`, agentId: agent.id, label, work });
      atField.set(key, list);
    });
  }

  const units: SceneUnit[] = [];
  const maxPerField = 3;
  for (const field of fields) {
    const list = atField.get(field.id) ?? [];
    hiddenWork += Math.max(0, list.length - maxPerField);
    const shownHere = list.slice(0, maxPerField);
    const step = Math.min(gap / maxPerField, height * 0.3);
    shownHere.forEach((placed, i) => {
      const work = placed.work!;
      const home = { x: Math.round(field.x + (i - (shownHere.length - 1) / 2) * step), y: workerY };
      const mode = MODE_OF[work.state];
      let x = home.x;
      let y = home.y;
      let facingRight = true;
      if (mode === "carrying") {
        // Walk to the base and back; the phase is per unit so loads stagger.
        const phase = ((t + (hash(placed.id) % CARRY_LOOP_MS)) % CARRY_LOOP_MS) / CARRY_LOOP_MS;
        const k = phase < 0.5 ? phase * 2 : 2 - phase * 2;
        x = Math.round(home.x + (base.x + width * 0.04 - home.x) * k);
        y = Math.round(home.y + (base.y + height * 0.12 - home.y) * k);
        facingRight = phase >= 0.5;
      }
      units.push({ id: placed.id, agentId: placed.agentId, label: placed.label, issueId: work.issueId, x, y, mode, facingRight });
    });
  }
  const idleCols = 3;
  const idleStep = Math.min(width * 0.05, height * 0.3);
  const shownIdle = idle.slice(0, idleCols * 2);
  shownIdle.forEach((placed, i) => {
    units.push({
      id: placed.id,
      agentId: placed.agentId,
      label: placed.label,
      issueId: null,
      x: Math.round(base.x + (i % idleCols - 0.5) * idleStep),
      y: Math.round(height * (i < idleCols ? 0.72 : 0.92)),
      mode: "idle",
      facingRight: true,
    });
  });

  const live = wrecks.filter((w) => now - w.at < wreckLifetimeMs);
  const perField = new Map<string, number>();
  const sceneWrecks: SceneWreck[] = live.map((w) => {
    const field = fieldById.get(w.epicId ?? NO_EPIC_FIELD);
    const key = field?.id ?? "";
    const index = perField.get(key) ?? 0;
    perField.set(key, index + 1);
    return {
      issueId: w.issueId,
      label: truncate(w.title, 18),
      x: field ? Math.round(field.x + (index % 2 === 0 ? -1 : 1) * Math.ceil(index / 2) * gap * 0.45) : base.x,
      y: field ? wreckY : Math.round(height * 0.92),
      age: Math.max(0, Math.min(1, (now - w.at) / wreckLifetimeMs)),
    };
  });

  return { width, height, base, fields, units, wrecks: sceneWrecks, hiddenWork };
}
