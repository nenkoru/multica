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
        const d = r + unit * 0.022;
        return { agentId, x: x + d * Math.cos(a), y: y + d * Math.sin(a) * 0.8 };
      });
      planets.push({
        id: epic.id,
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
