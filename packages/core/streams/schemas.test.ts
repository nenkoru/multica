// @vitest-environment node
import { describe, expect, it } from "vitest";
import { ListStreamsResponseSchema, MultikaSchema, TranscriptionSchema } from "./schemas";
import { parseWithFallback } from "../api/schema";

describe("streams schemas", () => {
  it("defaults a malformed list to no streams", () => {
    expect(ListStreamsResponseSchema.parse({}).streams).toEqual([]);
    expect(
      parseWithFallback("nope", ListStreamsResponseSchema, { streams: [] }, { endpoint: "GET /api/streams" }).streams,
    ).toEqual([]);
  });

  it("reads a missing or malformed state as unknown, not running", () => {
    const [a, b] = ListStreamsResponseSchema.parse({
      streams: [{ id: "1", slug: "a" }, { id: "2", slug: "b", state: 7 }],
    }).streams;
    expect(a?.state).toBe("unknown");
    expect(b?.state).toBe("unknown");
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

  it("reads voice as off unless the server says so", () => {
    expect(MultikaSchema.parse({ enabled: true }).voice).toBe(false);
    expect(MultikaSchema.parse({ enabled: true, voice: true }).voice).toBe(true);
  });

  it("reads a transcription without text as empty", () => {
    expect(TranscriptionSchema.parse({}).text).toBe("");
    expect(TranscriptionSchema.parse({ text: "привет", extra: 1 }).text).toBe("привет");
  });
});
