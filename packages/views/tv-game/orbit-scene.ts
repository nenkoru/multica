import type { TvGameSpark } from "./controller";
import { isStale, type TvGameSnapshot } from "./model";

// Pure layout of the Orbit stage (RUSL-1786): the Mika orb in the centre,
// epics as planets on three elliptic orbits — the more active, the closer —
// agents as satellites of the planet they work on, and finished issues as
// sparks flying into the orb. The canvas only draws what this returns.

export const RING_CAPACITY = [2, 3, 4] as const;
export const MAX_PLANETS = RING_CAPACITY[0] + RING_CAPACITY[1] + RING_CAPACITY[2];
/** One lap per ring, ms: inner orbits move faster. */
export const RING_PERIOD_MS = [40_000, 70_000, 110_000] as const;
const RING_SIZE = [
  { rx: 0.23, ry: 0.19 },
  { rx: 0.36, ry: 0.3 },
  { rx: 0.465, ry: 0.41 },
] as const;
const SATELLITE_PERIOD_MS = 9_000;
/** Satellite orbit radius beyond the planet, and the satellite size, per unit. */
const SATELLITE_GAP = 0.022;
export const SATELLITE_R = 0.011;
const MAX_SATELLITES = 4;

export interface OrbitRing {
  rx: number;
  ry: number;
}

export interface OrbitSatellite {
  agentId: string;
  x: number;
  y: number;
}

export interface OrbitPlanet {
  id: string;
  title: string;
  label: string;
  open: number;
  ring: 0 | 1 | 2;
  x: number;
  y: number;
  r: number;
  /** No activity for a day: dimmed and sent to the far orbit. */
  dim: boolean;
  hue: number;
  satellites: OrbitSatellite[];
}

export interface OrbitSpark {
  id: string;
  x: number;
  y: number;
  /** 0 just left the planet .. 1 reached the orb. */
  progress: number;
}

export interface OrbitScene {
  width: number;
  height: number;
  cx: number;
  cy: number;
  orbR: number;
  rings: OrbitRing[];
  planets: OrbitPlanet[];
  sparks: OrbitSpark[];
}

export interface BuildOrbitOptions {
  width: number;
  height: number;
  /** Animation time, ms; 0 for the still picture. */
  t: number;
  /** Wall clock, epoch ms; ages sparks and decides staleness. */
  now: number;
  sparks?: TvGameSpark[];
  sparkLifetimeMs?: number;
}

function hash(text: string): number {
  let h = 0;
  for (let i = 0; i < text.length; i++) h = (h * 31 + text.charCodeAt(i)) | 0;
  return Math.abs(h);
}

export function truncate(text: string, max: number): string {
  return text.length <= max ? text : `${text.slice(0, max - 1)}…`;
}

export function buildOrbit(snapshot: TvGameSnapshot, options: BuildOrbitOptions): OrbitScene {
  const { width, height, t, now, sparks = [], sparkLifetimeMs = 1_600 } = options;
  const cx = width / 2;
  const cy = height / 2;
  const unit = Math.min(width, height);
  const rings = RING_SIZE.map((s) => ({ rx: width * s.rx, ry: height * s.ry }));

  // Activity: running work counts double; then the most recent activity.
  const weight = new Map<string, number>();
  for (const w of snapshot.work) {
    if (!w.epicId) continue;
    weight.set(w.epicId, (weight.get(w.epicId) ?? 0) + (w.state === "running" ? 2 : 1));
  }
  const last = (iso: string | null) => (iso ? Date.parse(iso) || 0 : 0);
  const epics = snapshot.epics
    .filter((e) => e.open > 0 || weight.has(e.id))
    .map((e) => ({ epic: e, dim: !weight.has(e.id) && isStale(e.lastActivityAt, now) }))
    .sort(
      (a, b) =>
        Number(a.dim) - Number(b.dim) ||
        (weight.get(b.epic.id) ?? 0) - (weight.get(a.epic.id) ?? 0) ||
        last(b.epic.lastActivityAt) - last(a.epic.lastActivityAt) ||
        a.epic.id.localeCompare(b.epic.id),
    )
    .slice(0, MAX_PLANETS);

  // Fill rings inside out; a dimmed epic always goes to the far orbit.
  const byRing: (typeof epics)[] = [[], [], []];
  for (const item of epics) {
    let ring = item.dim ? 2 : byRing.findIndex((list, i) => list.length < RING_CAPACITY[i]!);
    if (ring < 0 || byRing[ring]!.length >= RING_CAPACITY[ring]!) ring = 2;
    if (byRing[ring]!.length >= RING_CAPACITY[ring]!) continue;
    byRing[ring]!.push(item);
  }

  const agentsAt = new Map<string, string[]>();
  for (const w of snapshot.work) {
    if (!w.epicId || w.state !== "running") continue;
    const list = agentsAt.get(w.epicId) ?? [];
    if (!list.includes(w.agentId)) list.push(w.agentId);
    agentsAt.set(w.epicId, list);
  }

  const planets: OrbitPlanet[] = [];
  byRing.forEach((list, ringIndex) => {
    const ring = rings[ringIndex]!;
    list.forEach(({ epic, dim }, i) => {
      const phase = (2 * Math.PI * i) / list.length + (hash(epic.id) % 100) / 100;
      const angle = phase + (2 * Math.PI * t) / RING_PERIOD_MS[ringIndex]!;
      const x = cx + ring.rx * Math.cos(angle);
      const y = cy + ring.ry * Math.sin(angle);
      const r = unit * (0.018 + 0.03 * Math.sqrt(Math.min(epic.open, 30) / 30));
      const agents = (agentsAt.get(epic.id) ?? []).slice(0, MAX_SATELLITES);
      const satellites = agents.map((agentId, n) => {
        const a = (2 * Math.PI * n) / agents.length + (2 * Math.PI * t) / SATELLITE_PERIOD_MS;
        const d = r + unit * SATELLITE_GAP;
        return { agentId, x: x + d * Math.cos(a), y: y + d * Math.sin(a) * 0.8 };
      });
      planets.push({
        id: epic.id,
        title: epic.title,
        label: `${truncate(epic.title, 16)} · ${epic.open}`,
        open: epic.open,
        ring: ringIndex as 0 | 1 | 2,
        x,
        y,
        r,
        dim,
        hue: hash(epic.id) % 360,
        satellites,
      });
    });
  });

  const planetById = new Map(planets.map((p) => [p.id, p]));
  const sceneSparks: OrbitSpark[] = sparks
    .filter((s) => now - s.at < sparkLifetimeMs && now >= s.at)
    .map((s) => {
      const from = (s.epicId && planetById.get(s.epicId)) || { x: width * 0.9, y: height * 0.15 };
      const p = (now - s.at) / sparkLifetimeMs;
      // A gentle curve into the orb.
      const ctrl = { x: (from.x + cx) / 2, y: Math.min(from.y, cy) - unit * 0.08 };
      const x = (1 - p) * (1 - p) * from.x + 2 * (1 - p) * p * ctrl.x + p * p * cx;
      const y = (1 - p) * (1 - p) * from.y + 2 * (1 - p) * p * ctrl.y + p * p * cy;
      return { id: s.id, x, y, progress: p };
    });

  return { width, height, cx, cy, orbR: unit * 0.15, rings, planets, sparks: sceneSparks };
}

export type OrbitLabelSide = "below" | "above" | "right" | "left";
const SIDES: OrbitLabelSide[] = ["below", "above", "right", "left"];

export interface OrbitLabel {
  planetId: string;
  text: string;
  side: OrbitLabelSide;
  /** Left edge and top of the text. */
  x: number;
  y: number;
  width: number;
  height: number;
}

interface Box {
  x0: number;
  x1: number;
  y0: number;
  y1: number;
}

interface Circle {
  x: number;
  y: number;
  r: number;
}

const LABEL_PAD = 2;
const EDGE = 4;

function hitsCircle(b: Box, c: Circle): boolean {
  const dx = c.x - Math.max(b.x0, Math.min(c.x, b.x1));
  const dy = c.y - Math.max(b.y0, Math.min(c.y, b.y1));
  return dx * dx + dy * dy < c.r * c.r;
}

function hitsBox(a: Box, b: Box): boolean {
  return a.x0 < b.x1 && a.x1 > b.x0 && a.y0 < b.y1 && a.y1 > b.y0;
}

/** The planet with the orbit of its satellites: what a label must keep clear of. */
function planetReach(p: OrbitPlanet, unit: number): Circle {
  const r = p.satellites.length ? p.r + unit * (SATELLITE_GAP + SATELLITE_R) : p.r;
  return { x: p.x, y: p.y, r: r + 1 };
}

/** Where a planet's label sat last frame: its side and how short its text was. */
export interface OrbitLabelPlace {
  side: OrbitLabelSide;
  variant: number;
  /** When the label took this place, ms on the caller's clock. */
  since: number;
}

/** Extra clearance a label needs to move somewhere better than where it sits. */
const SETTLE_PX = 8;
/** A label that fits where it is stays there at least this long. */
export const SETTLE_MS = 4_000;

/**
 * Places planet labels (RUSL-1818): inside the stage, clear of the orb, of
 * every planet with its satellites' orbit and of the labels already placed.
 * Each label tries below, above, right and left of its planet, a centred
 * label slides sideways to stay on screen, and a long title is shortened
 * before the label is given up. Inner (busier) planets choose first.
 * `memory` keeps each label where it was between frames while that place is
 * free; it moves or grows back to a longer text only after SETTLE_MS there
 * and with room to spare, so labels do not flicker as planets pass each other.
 */
export function layoutLabels(
  scene: OrbitScene,
  measure: (text: string) => number,
  fontPx: number,
  memory?: Map<string, OrbitLabelPlace>,
  now = 0,
): OrbitLabel[] {
  const { width, height } = scene;
  const unit = Math.min(width, height);
  const orb: Circle = { x: scene.cx, y: scene.cy, r: scene.orbR * 1.1 };
  const reaches = new Map(scene.planets.map((p) => [p.id, planetReach(p, unit)]));
  const placed: Box[] = [];
  const labels: OrbitLabel[] = [];
  const h = fontPx + 2 * LABEL_PAD;
  // The margin applies to everything but the label's own planet, which the
  // candidate boxes already sit next to.
  const fits = (b: Box, m: number, own: string) => {
    const grown = { x0: b.x0 - m, x1: b.x1 + m, y0: b.y0 - m, y1: b.y1 + m };
    return (
      b.x0 >= EDGE &&
      b.x1 <= width - EDGE &&
      b.y0 >= EDGE &&
      b.y1 <= height - EDGE &&
      !hitsCircle(grown, orb) &&
      ![...reaches].some(([id, c]) => hitsCircle(id === own ? b : grown, c)) &&
      !placed.some((o) => hitsBox(grown, o))
    );
  };

  for (const p of scene.planets) {
    const reach = reaches.get(p.id)!.r + 2;
    const texts = [p.label, `${truncate(p.title, 10)} · ${p.open}`, `${truncate(p.title, 6)} · ${p.open}`];
    const boxAt = (variant: number, side: OrbitLabelSide): Box => {
      const w = measure(texts[variant]!) + 2 * LABEL_PAD;
      const slide = (x0: number) => Math.min(Math.max(x0, EDGE), width - EDGE - w);
      const x0 = side === "right" ? p.x + reach : side === "left" ? p.x - reach - w : slide(p.x - w / 2);
      const y0 = side === "below" ? p.y + reach : side === "above" ? p.y - reach - h : p.y - h / 2;
      return { x0, y0, x1: x0 + w, y1: y0 + h };
    };
    const last = memory?.get(p.id);
    const order = last ? [last.side, ...SIDES.filter((side) => side !== last.side)] : SIDES;
    const search = (variants: number, margin: number): OrbitLabelPlace | null => {
      for (let variant = 0; variant < variants; variant++) {
        for (const side of order) if (fits(boxAt(variant, side), margin, p.id)) return { side, variant, since: now };
      }
      return null;
    };
    const stays = last && fits(boxAt(last.variant, last.side), 0, p.id);
    const settled = !last || now - last.since >= SETTLE_MS;
    const place =
      (last && settled && last.variant > 0 ? search(last.variant, SETTLE_PX) : null) ??
      (stays ? last : null) ??
      search(texts.length, 0);
    if (!place) {
      memory?.delete(p.id);
      continue;
    }
    memory?.set(p.id, place);
    const box = boxAt(place.variant, place.side);
    placed.push(box);
    labels.push({
      planetId: p.id,
      text: texts[place.variant]!,
      side: place.side,
      x: box.x0 + LABEL_PAD,
      y: box.y0 + LABEL_PAD,
      width: box.x1 - box.x0,
      height: h,
    });
  }
  return labels;
}
