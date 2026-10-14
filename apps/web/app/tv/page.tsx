import type { Metadata, Viewport } from "next";
import { TvScreen, tvLang } from "@multica/views/tv";
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
  return (
    <TvScreen
      apiBaseUrl={resolveBrowserApiBaseUrl(process.env) ?? ""}
      wsUrl={resolveBrowserWsUrl(process.env)}
      lang={lang}
    />
  );
}
