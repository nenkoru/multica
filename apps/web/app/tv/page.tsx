import type { Metadata, Viewport } from "next";
import { tvLang } from "@multica/views/tv";
import { TvOrbitDemo, TvOrbitPage } from "@multica/views/tv-game";
import { resolveBrowserApiBaseUrl, resolveBrowserWsUrl } from "@/config/runtime-urls";

export const metadata: Metadata = {
  title: "TV",
  robots: { index: false, follow: false },
  manifest: "/tv/manifest.webmanifest",
  appleWebApp: { capable: true, title: "Multica TV", statusBarStyle: "black-translucent" },
};

export const viewport: Viewport = {
  width: "device-width",
  initialScale: 1,
  viewportFit: "cover",
  themeColor: "#05070b",
};

// Kiosk page: no member session. A one-time pairing link (#pair=) is
// redeemed for an HttpOnly cookie by TvScreen itself (see
// server/internal/handler/tv.go). Installable on a phone through its own
// manifest.
export default async function TvPage({
  searchParams,
}: {
  searchParams: Promise<Record<string, string | string[] | undefined>>;
}) {
  const params = await searchParams;
  const lang = tvLang(typeof params.lang === "string" ? params.lang : undefined);
  const apiBaseUrl = resolveBrowserApiBaseUrl(process.env) ?? "";
  // The demo plays a script, not workspace data, so it needs no pairing.
  if (params.demo === "1") return <TvOrbitDemo lang={lang} apiBaseUrl={apiBaseUrl} />;
  // Orbit (RUSL-1786) is the main view; the board stays at ?view=board.
  return (
    <TvOrbitPage
      apiBaseUrl={apiBaseUrl}
      wsUrl={resolveBrowserWsUrl(process.env)}
      lang={lang}
      view={params.view === "board" ? "board" : "orbit"}
    />
  );
}
