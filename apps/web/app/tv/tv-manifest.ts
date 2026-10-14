import type { MetadataRoute } from "next";

// Separate installable app for the status screen: a phone home-screen icon
// that opens /tv full-screen, without the address bar. Its own id and scope
// keep it apart from the main Multica app manifest (app/manifest.ts).
export const TV_MANIFEST: MetadataRoute.Manifest = {
  id: "/tv",
  name: "Multica TV",
  short_name: "Multica TV",
  description: "Read-only status screen: sprint, epics, what needs you.",
  start_url: "/tv",
  scope: "/tv",
  display: "fullscreen",
  display_override: ["fullscreen", "standalone"],
  orientation: "any",
  background_color: "#05070b",
  theme_color: "#05070b",
  icons: [
    { src: "/icons/icon-192.png", sizes: "192x192", type: "image/png", purpose: "any" },
    { src: "/icons/icon-512.png", sizes: "512x512", type: "image/png", purpose: "any" },
    { src: "/icons/icon-maskable-512.png", sizes: "512x512", type: "image/png", purpose: "maskable" },
  ],
};
