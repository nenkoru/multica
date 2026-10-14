import { useEffect, useState } from "react";

/** Whether the page is visible; a hidden page stops polling and sockets. */
export function usePageVisible(): boolean {
  const [visible, setVisible] = useState(() => typeof document === "undefined" || document.visibilityState !== "hidden");
  useEffect(() => {
    const update = () => setVisible(document.visibilityState !== "hidden");
    document.addEventListener("visibilitychange", update);
    return () => document.removeEventListener("visibilitychange", update);
  }, []);
  return visible;
}

interface WakeLockSentinelLike {
  released: boolean;
  release(): Promise<void>;
}

/**
 * Keeps the screen on while the page is visible (Screen Wake Lock API). The
 * browser drops the lock when the page is hidden, so it is taken again on
 * every return. Unsupported browsers simply keep their own timeout.
 */
export function useWakeLock(enabled: boolean): void {
  useEffect(() => {
    const wakeLock = (navigator as Navigator & { wakeLock?: { request(type: "screen"): Promise<WakeLockSentinelLike> } }).wakeLock;
    if (!enabled || !wakeLock) return;
    let sentinel: WakeLockSentinelLike | null = null;
    let cancelled = false;
    const acquire = () => {
      if (document.visibilityState !== "visible" || (sentinel && !sentinel.released)) return;
      wakeLock
        .request("screen")
        .then((s) => {
          if (cancelled) void s.release();
          else sentinel = s;
        })
        .catch(() => {
          // Denied (battery saver, no user activation yet): retried on the
          // next visibility change or tap.
        });
    };
    acquire();
    document.addEventListener("visibilitychange", acquire);
    document.addEventListener("pointerdown", acquire);
    return () => {
      cancelled = true;
      document.removeEventListener("visibilitychange", acquire);
      document.removeEventListener("pointerdown", acquire);
      void sentinel?.release();
    };
  }, [enabled]);
}
