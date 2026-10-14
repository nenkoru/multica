import type { Metadata } from "next";
import { TvScreen, tvLang } from "@multica/views/tv";
import { resolveBrowserApiBaseUrl, resolveBrowserWsUrl } from "@/config/runtime-urls";

export const metadata: Metadata = {
  title: "TV",
  robots: { index: false, follow: false },
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
  return (
    <TvScreen
      apiBaseUrl={resolveBrowserApiBaseUrl(process.env) ?? ""}
      wsUrl={resolveBrowserWsUrl(process.env)}
      lang={lang}
    />
  );
}
