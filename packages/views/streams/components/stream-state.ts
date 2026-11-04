import type { Stream } from "@multica/core/streams";

// Dot colour per stream state (harness-design §7: green = running, amber =
// waiting on time or budget, blue = waiting on Руслан, red = broken).
export function streamStateDotClass(state: Stream["state"]): string {
  switch (state) {
    case "running":
      return "bg-success";
    case "asking":
      return "bg-info";
    case "waiting":
    case "budget":
      return "bg-warning";
    case "broken":
      return "bg-destructive";
    default:
      return "bg-muted-foreground";
  }
}

/** The chat route for Мультика's one chat, or the plain chat page. */
export function multikaChatHref(chatBase: string, sessionId: string | undefined): string {
  return sessionId ? `${chatBase}?session=${encodeURIComponent(sessionId)}` : chatBase;
}
