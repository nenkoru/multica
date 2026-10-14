// @vitest-environment node
import { describe, expect, it } from "vitest";
import { GET } from "./manifest.webmanifest/route";
import { TV_MANIFEST } from "./tv-manifest";

describe("TV manifest", () => {
  it("installs /tv full-screen with its own scope", async () => {
    expect(TV_MANIFEST.start_url).toBe("/tv");
    expect(TV_MANIFEST.scope).toBe("/tv");
    expect(TV_MANIFEST.display).toBe("fullscreen");
    const res = GET();
    expect(res.headers.get("Content-Type")).toBe("application/manifest+json");
    expect((await res.json()).id).toBe("/tv");
  });
});
