import type { TvIssue } from "@multica/core/tv";
import type { AlertPlate } from "./alerts";
import type { OrbMood, TvGameView } from "./controller";
import type { OrbitCopy, OrbitLabels } from "./copy";

// What the Orbit screen says: one line under the orb, one hint, one card.
// Pure, so every mood is unit-tested without a canvas.

export interface OrbitText {
  say: string;
  hint: string;
  cardLead: string;
  card: string;
}

export interface OrbitTextInput {
  view: TvGameView;
  mood: OrbMood;
  needsYou: TvIssue[];
  needsYouTotal: number;
  copy: OrbitCopy;
  labels: OrbitLabels;
  duration: (ms: number) => string;
  now: number;
}

/** Distinct agents with a run going. */
export function workingAgents(view: TvGameView): number {
  return new Set((view.snapshot?.work ?? []).filter((w) => w.state === "running").map((w) => w.agentId)).size;
}

function topPlate(view: TvGameView, level?: "warning" | "caution"): AlertPlate | undefined {
  return view.plates.find((p) => !level || p.level === level);
}

export function orbitText(input: OrbitTextInput): OrbitText {
  const { view, mood, copy, labels, duration, now } = input;
  const agents = new Map((view.snapshot?.agents ?? []).map((a) => [a.id, a.name]));

  if (mood === "talk") {
    return { say: copy.talking, hint: copy.talkingHint, cardLead: copy.mainNow, card: mainWork(input, agents) };
  }
  if (mood === "alarm") {
    const plate = topPlate(view, "warning")!;
    // "0 мин" says nothing: the duration shows from the first full minute.
    const lasting = now - plate.since >= 60_000 ? ` ${duration(now - plate.since)}` : "";
    const what = `${copy.plate(plate, labels)}${lasting}`;
    return {
      say: copy.calling(what),
      hint: plate.flashing ? copy.alarmHint : copy.alarmAcked,
      cardLead: copy.mainNow,
      card: what,
    };
  }
  if (mood === "call") {
    const oldest = input.needsYou[0];
    const flashing = view.plates.find((p) => p.flashing);
    const what = input.needsYouTotal > 0 ? copy.needsYouCount(input.needsYouTotal) : flashing ? copy.plate(flashing, labels) : "";
    return {
      say: copy.calling(what),
      hint: copy.callHint,
      cardLead: oldest ? copy.oneDecision : copy.mainNow,
      card: oldest ? oldest.title : flashing ? copy.plate(flashing, labels) : mainWork(input, agents),
    };
  }
  return { say: copy.calm(workingAgents(view)), hint: copy.calmHint, cardLead: copy.mainNow, card: mainWork(input, agents) };
}

/** The longest-running run: "<title> — <agent>, 3 ч". */
function mainWork(input: OrbitTextInput, agents: Map<string, string>): string {
  const running = (input.view.snapshot?.work ?? []).filter((w) => w.state === "running" && w.since);
  if (running.length === 0) return input.copy.nothingRunning;
  const longest = running.reduce((a, b) => (Date.parse(a.since!) <= Date.parse(b.since!) ? a : b));
  return input.copy.workingOn(longest.title, agents.get(longest.agentId) ?? "—", input.duration(input.now - Date.parse(longest.since!)));
}
