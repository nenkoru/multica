import type { Metadata, Viewport } from "next";
import { TvScreen, tvLang } from "@multica/views/tv";
import { TvOrbitDemo } from "@multica/views/tv-game";
import { resolveBrowserApiBaseUrl, resolveBrowserWsUrl } from "@/config/runtime-urls";

export const metadata: Metadata = {
  title: "TV",
  robots: { index: false, follow: false },
  // Installable on a phone's home screen, full screen without the address bar.
  manifest: "/tv/manifest.webmanifest",
  appleWebApp: { capable: true, title: "TV", statusBarStyle: "black-translucent" },
  icons: { apple: "/tv/apple-touch-icon.png" },
};

export const viewport: Viewport = {
  width: "device-width",
  initialScale: 1,
  viewportFit: "cover",
  themeColor: "#07090c",
};

// Kiosk page: no member session. The kiosk token is exchanged for an
// HttpOnly cookie by TvScreen itself (see server/internal/handler/tv.go).
export default async function TvPage({
  searchParams,
}: {
  searchParams: Promise<Record<string, string | string[] | undefined>>;
}) {
  const params = await searchParams;
  const lang = tvLang(typeof params.lang === "string" ? params.lang : undefined);
  const view = params.view === "board" ? "board" : "orbit";
  // The demo needs no kiosk token: it plays a script, not workspace data.
  if (params.demo === "1") {
    return (
      <div className="dark fixed inset-0 bg-background">
        <TvOrbitDemo lang={lang} apiBaseUrl={resolveBrowserApiBaseUrl(process.env) ?? ""} />
      </div>
    );
  }
  return (
    <TvScreen
      apiBaseUrl={resolveBrowserApiBaseUrl(process.env) ?? ""}
      wsUrl={resolveBrowserWsUrl(process.env)}
      lang={lang}
      view={view}
    />
  );
}
