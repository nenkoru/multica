import { useEffect, useRef } from "react";
import { SPARK_LIFETIME_MS, type OrbMood, type TvGameView } from "./controller";
import { SATELLITE_R, buildOrbit, layoutLabels, type OrbitLabelPlace, type OrbitScene } from "./orbit-scene";

// The Orbit stage on a canvas. A phone draws it, so the loop is capped at
// 24 fps (12 when calm), pauses with the tab (requestAnimationFrame does), and a reduced
// motion preference gets one still frame per change.

// Calm breathing is slow: 12 fps is enough and halves the work.
const FRAME_MS: Record<OrbMood, number> = { calm: 1000 / 12, call: 1000 / 24, alarm: 1000 / 24, talk: 1000 / 24 };

const MOOD: Record<OrbMood, { core: string; mid: string; edge: string; periodMs: number; amp: number }> = {
  calm: { core: "#bfe3ff", mid: "#4d9de0", edge: "rgba(22,49,79,0)", periodMs: 4000, amp: 0.05 },
  call: { core: "#ffe3b0", mid: "#f0a030", edge: "rgba(79,52,22,0)", periodMs: 1200, amp: 0.07 },
  alarm: { core: "#ffc4b8", mid: "#e5483a", edge: "rgba(90,20,16,0)", periodMs: 600, amp: 0.08 },
  talk: { core: "#bfe3ff", mid: "#4d9de0", edge: "rgba(22,49,79,0)", periodMs: 2000, amp: 0.03 },
};

export function prefersReducedMotion(): boolean {
  return typeof window !== "undefined" && typeof window.matchMedia === "function"
    ? window.matchMedia("(prefers-reduced-motion: reduce)").matches
    : false;
}

function draw(
  ctx: CanvasRenderingContext2D,
  scene: OrbitScene,
  mood: OrbMood,
  t: number,
  orbLabel: string,
  focus: boolean,
  labelPlaces: Map<string, OrbitLabelPlace>,
) {
  const { width, height, cx, cy, orbR } = scene;
  ctx.clearRect(0, 0, width, height);
  const unit = Math.min(width, height);

  if (!focus) {
    ctx.save();
    ctx.setLineDash([2, 5]);
    ctx.strokeStyle = "#1d2a3a";
    ctx.lineWidth = 1;
    for (const ring of scene.rings) {
      ctx.beginPath();
      ctx.ellipse(cx, cy, ring.rx, ring.ry, 0, 0, Math.PI * 2);
      ctx.stroke();
    }
    ctx.restore();

    const labelPx = Math.max(11, Math.round(unit * 0.03));
    for (const p of scene.planets) {
      ctx.globalAlpha = p.dim ? 0.45 : 1;
      ctx.fillStyle = `hsl(${p.hue} 45% ${p.dim ? 40 : 52}%)`;
      ctx.beginPath();
      ctx.arc(p.x, p.y, p.r, 0, Math.PI * 2);
      ctx.fill();
      ctx.fillStyle = "#ffd27a";
      for (const s of p.satellites) {
        ctx.beginPath();
        ctx.arc(s.x, s.y, Math.max(2.5, unit * SATELLITE_R), 0, Math.PI * 2);
        ctx.fill();
      }
      ctx.globalAlpha = 1;
    }

    ctx.textAlign = "left";
    ctx.textBaseline = "top";
    ctx.font = `500 ${labelPx}px system-ui, sans-serif`;
    ctx.fillStyle = "#9fb4c8";
    const dim = new Set(scene.planets.filter((p) => p.dim).map((p) => p.id));
    for (const label of layoutLabels(scene, (text) => ctx.measureText(text).width, labelPx, labelPlaces)) {
      ctx.globalAlpha = dim.has(label.planetId) ? 0.45 : 1;
      ctx.fillText(label.text, label.x, label.y);
    }
    ctx.globalAlpha = 1;
  }

  const m = MOOD[mood];
  const pulse = 1 + m.amp * Math.sin((2 * Math.PI * t) / m.periodMs);
  if (mood === "talk") {
    for (let k = 0; k < 2; k++) {
      const phase = ((t / 1200 + k / 2) % 1 + 1) % 1;
      ctx.strokeStyle = `rgba(111, 182, 240, ${0.7 * (1 - phase)})`;
      ctx.lineWidth = 1.5;
      ctx.beginPath();
      ctx.arc(cx, cy, orbR * (0.8 + 0.55 * phase), 0, Math.PI * 2);
      ctx.stroke();
    }
  }
  const r = orbR * pulse;
  const glow = ctx.createRadialGradient(cx, cy - r * 0.1, 0, cx, cy, r);
  glow.addColorStop(0, m.core);
  glow.addColorStop(0.45, m.mid);
  glow.addColorStop(1, m.edge);
  ctx.fillStyle = glow;
  ctx.beginPath();
  ctx.arc(cx, cy, r, 0, Math.PI * 2);
  ctx.fill();
  ctx.fillStyle = "rgba(240, 248, 255, 0.85)";
  ctx.beginPath();
  ctx.arc(cx, cy, r * 0.48, 0, Math.PI * 2);
  ctx.fill();
  ctx.fillStyle = "#0b1a2a";
  ctx.textAlign = "center";
  ctx.textBaseline = "middle";
  ctx.font = `700 ${Math.max(12, Math.round(orbR * 0.22))}px system-ui, sans-serif`;
  ctx.fillText(orbLabel, cx, cy);

  for (const s of scene.sparks) {
    ctx.fillStyle = `rgba(255, 255, 255, ${s.progress < 0.85 ? 1 : (1 - s.progress) / 0.15})`;
    ctx.beginPath();
    ctx.arc(s.x, s.y, Math.max(2, unit * 0.009), 0, Math.PI * 2);
    ctx.fill();
  }
}

export interface OrbitCanvasProps {
  view: TvGameView;
  mood: OrbMood;
  /** The controller's clock; the demo runs a virtual one. */
  now: () => number;
  ariaLabel: string;
  orbLabel: string;
  focus: boolean;
  onOrbTap?: () => void;
}

export function OrbitCanvas({ view, mood, now: clock, ariaLabel, orbLabel, focus, onOrbTap }: OrbitCanvasProps) {
  const canvasRef = useRef<HTMLCanvasElement | null>(null);
  const stateRef = useRef({ view, mood, focus });
  stateRef.current = { view, mood, focus };
  const reducedMotion = prefersReducedMotion();
  // Without the animation loop the still picture is redrawn on every change.
  const redrawKey = reducedMotion ? `${view.version}:${mood}:${focus}` : "";

  useEffect(() => {
    const canvas = canvasRef.current;
    const ctx = canvas?.getContext("2d");
    if (!canvas || !ctx) return;
    let frame = 0;
    let last = 0;
    const labelPlaces = new Map<string, OrbitLabelPlace>();
    const started = performance.now();
    const render = (stamp: number) => {
      const { view: current, mood: currentMood, focus: currentFocus } = stateRef.current;
      const rect = canvas.getBoundingClientRect();
      const dpr = Math.min(2, window.devicePixelRatio || 1);
      const w = Math.max(1, Math.round(rect.width));
      const h = Math.max(1, Math.round(rect.height));
      if (canvas.width !== w * dpr || canvas.height !== h * dpr) {
        canvas.width = w * dpr;
        canvas.height = h * dpr;
      }
      ctx.setTransform(dpr, 0, 0, dpr, 0, 0);
      if (!current.snapshot) {
        ctx.clearRect(0, 0, w, h);
        return;
      }
      const t = reducedMotion ? 0 : stamp - started;
      const scene = buildOrbit(current.snapshot, {
        width: w,
        height: h,
        t,
        now: clock(),
        sparks: reducedMotion ? [] : current.sparks,
        sparkLifetimeMs: SPARK_LIFETIME_MS,
      });
      draw(ctx, scene, currentMood, t, orbLabel, currentFocus, labelPlaces);
    };
    if (reducedMotion) {
      render(started);
      return;
    }
    const loop = (stamp: number) => {
      frame = requestAnimationFrame(loop);
      if (stamp - last < FRAME_MS[stateRef.current.mood] && stateRef.current.view.sparks.length === 0) return;
      if (stamp - last < FRAME_MS.call) return;
      last = stamp;
      render(stamp);
    };
    frame = requestAnimationFrame(loop);
    return () => cancelAnimationFrame(frame);
  }, [reducedMotion, clock, orbLabel, redrawKey]);

  return (
    <canvas
      ref={canvasRef}
      role="img"
      aria-label={ariaLabel}
      className="block h-full w-full"
      onClick={(event) => {
        if (!onOrbTap) return;
        const rect = event.currentTarget.getBoundingClientRect();
        const dx = event.clientX - rect.left - rect.width / 2;
        const dy = event.clientY - rect.top - rect.height / 2;
        if (Math.hypot(dx, dy) <= Math.min(rect.width, rect.height) * 0.17) onOrbTap();
      }}
    />
  );
}
