import { TV_MANIFEST } from "../tv-manifest";

export function GET() {
  return new Response(JSON.stringify(TV_MANIFEST), {
    headers: { "Content-Type": "application/manifest+json", "Cache-Control": "public, max-age=3600" },
  });
}
