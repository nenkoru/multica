// @vitest-environment node
import { describe, expect, it } from "vitest";
import { ListStreamsResponseSchema, MultikaSchema } from "./schemas";
import { parseWithFallback } from "../api/schema";

describe("streams schemas", () => {
  it("defaults a malformed list to no streams", () => {
    expect(ListStreamsResponseSchema.parse({}).streams).toEqual([]);
    expect(
      parseWithFallback("nope", ListStreamsResponseSchema, { streams: [] }, { endpoint: "GET /api/streams" }).streams,
    ).toEqual([]);
  });

  it("keeps unknown stream states and fills missing fields", () => {
    const [s] = ListStreamsResponseSchema.parse({
      streams: [{ id: "1", slug: "tern", state: "hibernating" }],
    }).streams;
    expect(s).toMatchObject({ slug: "tern", state: "hibernating", title: "", state_label: "", active: true });
  });

  it("reads a disabled Мультика as enabled=false", () => {
    expect(MultikaSchema.parse({}).enabled).toBe(false);
    expect(MultikaSchema.parse({ enabled: true, chat_session_id: "c", focus_slug: "tern" })).toMatchObject({
      enabled: true,
      chat_session_id: "c",
      focus_slug: "tern",
    });
  });
});
