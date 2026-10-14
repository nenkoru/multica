// Pixel-art sprites drawn for this project (RUSL-1786): each sprite is a
// character matrix, one character per pixel, "." transparent. No third-party
// art is used anywhere in the game layer; see ASSETS.md.

export type Sprite = readonly string[];

export const PALETTE: Record<string, string> = {
  // worker
  h: "#f2b134", // helmet
  s: "#e8c39e", // face
  b: "#4a7bd0", // overalls
  d: "#2f4f8a", // overalls shade
  k: "#3a2c22", // boots
  t: "#9aa3ad", // pickaxe head
  w: "#8a5a32", // pickaxe handle
  o: "#c98b2b", // carried crystal sack
  // crystal field
  c: "#59d0e8",
  C: "#b8f1fa",
  m: "#2b8aa6",
  g: "#4f7f3a", // overgrowth
  G: "#6fa64f",
  // base
  p: "#5d6673",
  P: "#8892a0",
  q: "#3b424c",
  y: "#e6c84f", // base light
  // wreck
  r: "#d9472b",
  R: "#f28a3c",
  x: "#555555",
  // marker
  a: "#ffcc33",
  A: "#1d1d1d",
};

export const WORKER_IDLE: Sprite = [
  "..hhhh..",
  ".hhhhhh.",
  "..ssss..",
  "..s..s..",
  ".bbbbbb.",
  "s.bddb.s",
  "..b..b..",
  "..k..k..",
];

export const WORKER_DIG_A: Sprite = [
  "..hhhh.t",
  ".hhhhhhw",
  "..ssssw.",
  "..s..w..",
  ".bbbbwb.",
  "..bddb..",
  "..b..b..",
  "..k..k..",
];

export const WORKER_DIG_B: Sprite = [
  "..hhhh..",
  ".hhhhhh.",
  "..ssss..",
  "..s..s..",
  ".bbbbbbw",
  "..bddb.w",
  "..b..b.t",
  "..k..ktt",
];

export const WORKER_CARRY: Sprite = [
  "..hhhhoo",
  ".hhhhhoo",
  "..ssssoo",
  "..s..so.",
  ".bbbbbb.",
  "..bddb..",
  "..b..b..",
  ".k....k.",
];

export const WORKER_DOWN: Sprite = [
  "........",
  "........",
  "........",
  "........",
  "hh.R..r.",
  "hhsbbbbk",
  "hhsbddbk",
  "xxxxxxxx",
];

export const CRYSTAL: Sprite = [
  "...C...",
  "..cCc..",
  "..cCm..",
  ".ccCcm.",
  ".cCcmm.",
  ".ccCcm.",
  "..ccm..",
];

export const CRYSTAL_OVERGROWN: Sprite = [
  "...g...",
  "..gGg..",
  "..cgm..",
  ".gcGcg.",
  ".GcgmG.",
  "gggGggg",
  ".gGgGg.",
];

export const BASE: Sprite = [
  "......yy......",
  ".....pPPp.....",
  "....pPPPPp....",
  "...pPPPPPPp...",
  "..pPPqqqqPPp..",
  ".pPPPqyyqPPPp.",
  "pPPPPqqqqPPPPp",
  "pPPPPPPPPPPPPp",
  "pqqPPPPPPPPqqp",
  "pqqPPPqqPPPqqp",
  "pPPPPPqqPPPPPp",
  "pppppppppppppp",
];

export const ALERT_MARK: Sprite = [
  ".aaa.",
  ".aAa.",
  ".aAa.",
  ".aAa.",
  ".aaa.",
  ".aAa.",
  ".aaa.",
];

export const WRECK: Sprite = [
  "..R.r..",
  ".rRRr..",
  "xrRRRrx",
  "xxrRrxx",
  ".xxxxx.",
];

/** Draws `sprite` with its top-left corner at (x, y), `scale` px per pixel. */
export function drawSprite(
  ctx: CanvasRenderingContext2D,
  sprite: Sprite,
  x: number,
  y: number,
  scale: number,
  flip = false,
  alpha = 1,
): void {
  const prevAlpha = ctx.globalAlpha;
  ctx.globalAlpha = alpha;
  for (let row = 0; row < sprite.length; row++) {
    const line = sprite[row]!;
    for (let col = 0; col < line.length; col++) {
      const color = PALETTE[line[col]!];
      if (!color) continue;
      const cx = flip ? line.length - 1 - col : col;
      ctx.fillStyle = color;
      ctx.fillRect(Math.round(x + cx * scale), Math.round(y + row * scale), scale, scale);
    }
  }
  ctx.globalAlpha = prevAlpha;
}

export function spriteSize(sprite: Sprite, scale: number): { w: number; h: number } {
  return { w: (sprite[0]?.length ?? 0) * scale, h: sprite.length * scale };
}
