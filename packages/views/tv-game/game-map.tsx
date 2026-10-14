import { useEffect, useRef } from "react";
import { POPUP_LIFETIME_MS, WRECK_LIFETIME_MS, type TvGameView } from "./controller";
import { buildScene, type SceneModel } from "./scene";
import {
  ALERT_MARK,
  BASE,
  CRYSTAL,
  CRYSTAL_OVERGROWN,
  WORKER_CARRY,
  WORKER_DIG_A,
  WORKER_DIG_B,
  WORKER_DOWN,
  WORKER_IDLE,
  WRECK,
  drawSprite,
  spriteSize,
} from "./sprites";

const FRAME_MS = 1000 / 30;
const DIG_FRAME_MS = 280;

export function usePrefersReducedMotion(): boolean {
  const ref = useRef(false);
  if (typeof window !== "undefined" && typeof window.matchMedia === "function") {
    ref.current = window.matchMedia("(prefers-reduced-motion: reduce)").matches;
  }
  return ref.current;
}

function draw(ctx: CanvasRenderingContext2D, scene: SceneModel, view: TvGameView, t: number, now: number, still: boolean) {
  const { width, height } = scene;
  const scale = Math.max(3, Math.round(height / 60));
  const labelPx = Math.max(14, Math.round(height / 17));
  ctx.clearRect(0, 0, width, height);

  // Ground: two flat bands, no texture, so text stays readable on top.
  ctx.fillStyle = "#15191f";
  ctx.fillRect(0, 0, width, height);
  ctx.fillStyle = "#1b2027";
  ctx.fillRect(0, Math.round(height * 0.5), width, Math.round(height * 0.5));

  ctx.textBaseline = "top";
  ctx.textAlign = "center";

  const base = spriteSize(BASE, scale);
  drawSprite(ctx, BASE, scene.base.x - base.w / 2, scene.base.y - base.h, scale);

  for (const field of scene.fields) {
    const sprite = field.overgrown ? CRYSTAL_OVERGROWN : CRYSTAL;
    const size = spriteSize(sprite, scale);
    // Depletion removes crystals and fades the rest.
    const left = Math.max(1, Math.round(field.crystals * (1 - field.depletion * 0.8)));
    const alpha = field.overgrown ? 0.55 : 1 - field.depletion * 0.35;
    for (let i = 0; i < left; i++) {
      const dx = (i - (left - 1) / 2) * size.w * 0.75;
      const dy = (i % 2) * size.h * 0.25;
      drawSprite(ctx, sprite, field.x + dx - size.w / 2, field.y - size.h + dy, scale, false, alpha);
    }
    ctx.font = `600 ${labelPx}px system-ui, sans-serif`;
    ctx.fillStyle = field.overgrown ? "#7d8590" : "#d6dde6";
    ctx.fillText(field.label, field.x, field.y - size.h * 1.25 - labelPx * 1.2);
  }

  const digFrame = still ? 0 : Math.floor(t / DIG_FRAME_MS) % 2;
  ctx.font = `500 ${Math.round(labelPx * 0.75)}px system-ui, sans-serif`;
  for (const unit of scene.units) {
    const sprite =
      unit.mode === "mining"
        ? digFrame === 0
          ? WORKER_DIG_A
          : WORKER_DIG_B
        : unit.mode === "carrying"
          ? WORKER_CARRY
          : WORKER_IDLE;
    const size = spriteSize(sprite, scale);
    const wrecked = view.wrecks.some((w) => w.agentId === unit.agentId && w.issueId === unit.issueId);
    drawSprite(ctx, wrecked ? WORKER_DOWN : sprite, unit.x - size.w / 2, unit.y - size.h, scale, !unit.facingRight);
    if (unit.mode === "blocked") {
      const mark = spriteSize(ALERT_MARK, scale);
      const bob = still ? 0 : Math.round(Math.sin(t / 300) * scale);
      // Beside the helmet, so it never covers the crystals above.
      drawSprite(ctx, ALERT_MARK, unit.x + size.w * 0.45, unit.y - size.h - mark.h * 0.4 + bob, scale);
    }
    ctx.fillStyle = "#aab4c0";
    ctx.fillText(unit.label, unit.x, unit.y + scale);
  }

  for (const wreck of scene.wrecks) {
    const size = spriteSize(WRECK, scale);
    // A short flash right after the crash, then a dim wreck for the rest of the minute.
    const flash = !still && wreck.age * WRECK_LIFETIME_MS < 1200;
    if (flash) {
      ctx.fillStyle = `rgba(255, 170, 60, ${0.55 * (1 - (wreck.age * WRECK_LIFETIME_MS) / 1200)})`;
      ctx.beginPath();
      ctx.arc(wreck.x, wreck.y - size.h / 2, size.w * 1.6, 0, Math.PI * 2);
      ctx.fill();
    }
    drawSprite(ctx, WRECK, wreck.x - size.w / 2, wreck.y - size.h, scale, false, 1 - wreck.age * 0.6);
    ctx.fillStyle = "#f08a6c";
    ctx.fillText(wreck.label, wreck.x, wreck.y + scale);
  }

  // "+1" floats up from the base for every issue that was just finished.
  ctx.font = `700 ${Math.round(labelPx * 1.4)}px system-ui, sans-serif`;
  view.popups.forEach((popup, i) => {
    const age = Math.max(0, Math.min(1, (now - popup.at) / POPUP_LIFETIME_MS));
    const rise = still ? 0 : age * base.h;
    ctx.fillStyle = `rgba(120, 220, 140, ${still ? 1 : 1 - age})`;
    ctx.fillText("+1", scene.base.x + i * labelPx, scene.base.y - base.h - labelPx * 1.6 - rise);
  });
}

export interface GameMapProps {
  view: TvGameView;
  /** The controller's clock; the demo runs a virtual one. */
  now: () => number;
  ariaLabel: string;
  noEpicLabel: string;
}

export function GameMap({ view, now: clock, ariaLabel, noEpicLabel }: GameMapProps) {
  const canvasRef = useRef<HTMLCanvasElement | null>(null);
  const viewRef = useRef(view);
  viewRef.current = view;
  const reducedMotion = usePrefersReducedMotion();
  // Without the animation loop the still picture is redrawn on every change.
  const redrawKey = reducedMotion ? view.version : 0;

  useEffect(() => {
    const canvas = canvasRef.current;
    if (!canvas) return;
    const ctx = canvas.getContext("2d");
    if (!ctx) return;

    let frame = 0;
    let last = 0;
    const started = performance.now();
    const render = (stamp: number) => {
      const current = viewRef.current;
      const rect = canvas.getBoundingClientRect();
      const dpr = Math.min(2, window.devicePixelRatio || 1);
      const w = Math.max(1, Math.round(rect.width));
      const h = Math.max(1, Math.round(rect.height));
      if (canvas.width !== w * dpr || canvas.height !== h * dpr) {
        canvas.width = w * dpr;
        canvas.height = h * dpr;
      }
      ctx.setTransform(dpr, 0, 0, dpr, 0, 0);
      ctx.imageSmoothingEnabled = false;
      if (!current.snapshot) {
        ctx.clearRect(0, 0, w, h);
        return;
      }
      const t = reducedMotion ? 0 : stamp - started;
      const now = clock();
      const scene = buildScene(current.snapshot, {
        width: w,
        height: h,
        t,
        now,
        wrecks: current.wrecks,
        noEpicLabel,
      });
      draw(ctx, scene, current, t, now, reducedMotion);
    };

    if (reducedMotion) {
      render(started);
      return;
    }
    const loop = (stamp: number) => {
      frame = requestAnimationFrame(loop);
      // Cap at 30 fps: a TV browser has little CPU to spare.
      if (stamp - last < FRAME_MS) return;
      last = stamp;
      render(stamp);
    };
    frame = requestAnimationFrame(loop);
    return () => cancelAnimationFrame(frame);
  }, [reducedMotion, noEpicLabel, clock, redrawKey]);

  return <canvas ref={canvasRef} role="img" aria-label={ariaLabel} className="block h-full w-full" />;
}
