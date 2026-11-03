import { useEffect, useRef, useState } from "react";

const DEBOUNCE_MS = 750;
// A steady stream of events must not postpone the refresh forever.
const MAX_WAIT_MS = 3_000;
const RECONNECT_MIN_MS = 2_000;
const RECONNECT_MAX_MS = 30_000;

/**
 * Listens to the kiosk WebSocket and calls onChange, debounced, whenever the
 * server signals that the TV aggregate may have changed. Reconnects with
 * backoff; returns whether the socket is currently open.
 */
export function useTvRealtime(url: string | null, onChange: () => void): boolean {
  const [connected, setConnected] = useState(false);
  const onChangeRef = useRef(onChange);
  onChangeRef.current = onChange;

  useEffect(() => {
    if (!url) return;
    let socket: WebSocket | null = null;
    let closed = false;
    let retryDelay = RECONNECT_MIN_MS;
    let retryTimer: ReturnType<typeof setTimeout> | undefined;
    let debounceTimer: ReturnType<typeof setTimeout> | undefined;
    let firstPending: number | null = null;

    const fire = () => {
      firstPending = null;
      onChangeRef.current();
    };
    const schedule = () => {
      clearTimeout(debounceTimer);
      const now = Date.now();
      firstPending ??= now;
      const wait = Math.max(0, Math.min(DEBOUNCE_MS, firstPending + MAX_WAIT_MS - now));
      debounceTimer = setTimeout(fire, wait);
    };

    const connect = () => {
      socket = new WebSocket(url);
      socket.onopen = () => {
        retryDelay = RECONNECT_MIN_MS;
        setConnected(true);
        // Anything may have changed while the socket was down.
        schedule();
      };
      socket.onmessage = (event) => {
        if (typeof event.data === "string" && event.data.includes('"tv:changed"')) schedule();
      };
      socket.onclose = () => {
        setConnected(false);
        if (closed) return;
        retryTimer = setTimeout(connect, retryDelay);
        retryDelay = Math.min(retryDelay * 2, RECONNECT_MAX_MS);
      };
    };
    connect();

    return () => {
      closed = true;
      clearTimeout(retryTimer);
      clearTimeout(debounceTimer);
      socket?.close();
    };
  }, [url]);

  return connected;
}
