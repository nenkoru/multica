"use client";

import { useCallback, useEffect, useRef, useState, type CSSProperties, type FormEvent, type ReactNode } from "react";
import { useQuery, useQueryClient } from "@tanstack/react-query";
import {
  Bot,
  CircleAlert,
  CircleCheck,
  CircleDot,
  CircleX,
  Clock3,
  Eye,
  Hand,
  OctagonPause,
  WifiOff,
} from "lucide-react";
import { sprintTimeline } from "@multica/core/planning";
import {
  TV_POLL_INTERVAL_MS,
  TvUnauthorizedError,
  createTvSession,
  diffTvOverview,
  tvCredential,
  tvKeys,
  tvOverviewOptions,
  tvWsUrl,
  type TvEpic,
  type TvEvent,
  type TvIssue,
  type TvOverview,
  type TvSprint,
  type TvStreamEvent,
} from "@multica/core/tv";
import { Button } from "@multica/ui/components/ui/button";
import { Input } from "@multica/ui/components/ui/input";
import { cn } from "@multica/ui/lib/utils";
import { TV_COPY, type TvCopy, type TvLang } from "./copy";
import { elapsedMs, epicSegments, percent, truncateTitle, tvDate, tvTime, type TvSegmentKey, type TvTone } from "./model";
import { usePageVisible, useWakeLock } from "./use-device";
import { useTvRealtime } from "./use-tv-realtime";

// Phone first. Portrait is the main mode: numbers, "needs you", then epics,
// sized from the phone's width. Landscape is the 16:9 wall picture (a monitor
// fed by the phone, or a TV), sized from whichever of width or height binds,
// so 1080p, 4K and a phone on its side all show the same picture. The app's
// --text-* scale stops at 36 px, below what a number needs across a room.
const TV_CSS = `
[data-tv-root] {
  --tv-num: min(11vw, 72px);
  --tv-title: min(5vw, 28px);
  --tv-body: min(4.2vw, 22px);
  --tv-small: min(3.5vw, 18px);
  --tv-good: var(--success);
  --tv-warning: var(--warning);
  --tv-serious: oklch(0.72 0.17 55);
  --tv-critical: var(--destructive);
  --tv-neutral: var(--info);
}
@media (orientation: landscape) {
  [data-tv-root] {
    --tv-u: min(1vw, 1.7778vh);
    --tv-num: calc(3.4 * var(--tv-u));
    --tv-title: calc(1.45 * var(--tv-u));
    --tv-body: calc(1.2 * var(--tv-u));
    --tv-small: calc(0.95 * var(--tv-u));
  }
}`;

const TONE_VAR: Record<TvTone, string> = {
  neutral: "var(--tv-neutral)",
  good: "var(--tv-good)",
  warning: "var(--tv-warning)",
  serious: "var(--tv-serious)",
  // Red is reserved for the "needs you" column.
  critical: "var(--tv-critical)",
};

const SEGMENT_ICON: Record<TvSegmentKey, typeof CircleDot> = {
  in_progress: CircleDot,
  in_review: Eye,
  blocked: OctagonPause,
  done: CircleCheck,
};

const fs = (size: "num" | "title" | "body" | "small"): CSSProperties => ({
  fontSize: `var(--tv-${size})`,
  lineHeight: size === "num" ? 1 : 1.3,
});

function useNow(intervalMs: number, running: boolean): Date {
  const [now, setNow] = useState(() => new Date());
  useEffect(() => {
    if (!running) return;
    setNow(new Date());
    const id = setInterval(() => setNow(new Date()), intervalMs);
    return () => clearInterval(id);
  }, [intervalMs, running]);
  return now;
}

/** Reads a one-time pairing code from the address fragment and clears it. */
function takePairingCode(): string | null {
  const match = /(?:^#|&)pair=([^&]+)/.exec(window.location.hash);
  if (!match?.[1]) return null;
  window.history.replaceState(null, "", window.location.pathname + window.location.search);
  return decodeURIComponent(match[1]);
}

export interface TvScreenProps {
  /** API origin; empty when the API is served from the page origin. */
  apiBaseUrl?: string;
  /** Explicit realtime URL (…/ws) when the deployment sets one. */
  wsUrl?: string;
  lang?: TvLang;
  /**
   * An optional layer (map, animation) given ~38 % of the landscape height and
   * the top of the portrait screen. It receives the current snapshot.
   */
  layer?: (overview: TvOverview) => ReactNode;
  /** Discrete changes between consecutive snapshots, oldest first. */
  onEvents?: (events: TvStreamEvent[], overview: TvOverview) => void;
}

/** Full-screen, read-only status screen authenticated by a kiosk token. */
export function TvScreen({ apiBaseUrl = "", wsUrl, lang = "ru", layer, onEvents }: TvScreenProps) {
  const copy = TV_COPY[lang];
  const queryClient = useQueryClient();
  const visible = usePageVisible();
  const [pairing, setPairing] = useState<"idle" | "exchanging" | "rejected">("idle");

  // A pairing link carries a one-time code in the fragment, which never
  // reaches a server log. Redeem it once for the cookie and drop it.
  useEffect(() => {
    const code = takePairingCode();
    if (!code) return;
    setPairing("exchanging");
    createTvSession(apiBaseUrl, { code })
      .catch(() => false)
      .then((ok) => {
        setPairing(ok ? "idle" : "rejected");
        void queryClient.invalidateQueries({ queryKey: tvKeys.overview(apiBaseUrl) });
      });
  }, [apiBaseUrl, queryClient]);

  const query = useQuery({
    ...tvOverviewOptions(apiBaseUrl),
    enabled: pairing !== "exchanging",
    refetchInterval: visible ? TV_POLL_INTERVAL_MS : false,
  });
  const unauthorized = query.error instanceof TvUnauthorizedError;

  const refresh = useCallback(() => {
    void queryClient.invalidateQueries({ queryKey: tvKeys.overview(apiBaseUrl) });
  }, [apiBaseUrl, queryClient]);
  const [socketUrl, setSocketUrl] = useState<string | null>(null);
  useEffect(() => {
    setSocketUrl(tvWsUrl({ wsUrl, apiBaseUrl, pageOrigin: window.location.origin }));
  }, [apiBaseUrl, wsUrl]);
  // The socket is closed while the page is hidden and reopened (with a
  // refetch) when it comes back.
  useTvRealtime(query.data && !unauthorized && visible ? socketUrl : null, refresh);
  useWakeLock(Boolean(query.data) && !unauthorized);

  const previous = useRef<TvOverview | null>(null);
  const onEventsRef = useRef(onEvents);
  onEventsRef.current = onEvents;
  useEffect(() => {
    const data = query.data;
    if (!data || data === previous.current) return;
    const events = diffTvOverview(previous.current, data);
    previous.current = data;
    if (events.length > 0) onEventsRef.current?.(events, data);
  }, [query.data]);

  const now = useNow(10_000, visible);

  return (
    <div
      data-tv-root
      className="dark fixed inset-0 overflow-hidden bg-background text-foreground antialiased"
      data-testid="tv-screen"
    >
      <style>{TV_CSS}</style>
      {unauthorized || pairing === "rejected" ? (
        <TvPairing apiBaseUrl={apiBaseUrl} copy={copy} rejectedLink={pairing === "rejected"} onPaired={() => {
          setPairing("idle");
          refresh();
        }} />
      ) : query.data ? (
        <TvDashboard data={query.data} copy={copy} now={now} offline={query.isError} layer={layer?.(query.data)} />
      ) : (
        <div className="flex h-full items-center justify-center text-muted-foreground" style={fs("title")}>
          {copy.loading}
        </div>
      )}
    </div>
  );
}

function TvPairing({
  apiBaseUrl,
  copy,
  rejectedLink,
  onPaired,
}: {
  apiBaseUrl: string;
  copy: TvCopy;
  rejectedLink: boolean;
  onPaired: () => void;
}) {
  const [value, setValue] = useState("");
  const [rejected, setRejected] = useState(rejectedLink);
  const [busy, setBusy] = useState(false);
  const submit = async (event: FormEvent) => {
    event.preventDefault();
    setBusy(true);
    const ok = await createTvSession(apiBaseUrl, tvCredential(value)).catch(() => false);
    setBusy(false);
    setRejected(!ok);
    if (ok) {
      setValue("");
      onPaired();
    }
  };
  return (
    <form onSubmit={submit} className="mx-auto flex h-full w-[min(90vw,60rem)] flex-col justify-center gap-6">
      <h1 className="font-semibold" style={fs("title")}>
        {copy.pairTitle}
      </h1>
      <p className="text-muted-foreground" style={fs("body")}>
        {copy.pairHint}
      </p>
      <div className="flex flex-col gap-4 sm:flex-row">
        <Input
          type="password"
          autoComplete="off"
          aria-label={copy.pairTitle}
          placeholder={copy.pairPlaceholder}
          value={value}
          onChange={(e) => setValue(e.target.value)}
          className="h-auto py-3"
          style={fs("body")}
        />
        <Button type="submit" size="lg" disabled={busy || value.trim() === ""}>
          {copy.pairSubmit}
        </Button>
      </div>
      {rejected && (
        <p role="alert" className="flex items-center gap-3" style={{ ...fs("body"), color: TONE_VAR.serious }}>
          <CircleX className="size-[1.2em] shrink-0" aria-hidden />
          {copy.pairRejected}
        </p>
      )}
    </form>
  );
}

export function TvDashboard({
  data,
  copy,
  now,
  offline,
  layer,
}: {
  data: TvOverview;
  copy: TvCopy;
  now: Date;
  offline: boolean;
  layer?: ReactNode;
}) {
  // Portrait: one scrolling column in reading order. Landscape: the fixed
  // 16:9 grid — top bar, optional layer band, epics beside "needs you", feed.
  return (
    <div
      className={cn(
        "flex h-full flex-col gap-5 overflow-y-auto overscroll-contain p-4 pb-8",
        "landscape:grid landscape:grid-cols-[1fr_28%] landscape:gap-x-[2vw] landscape:gap-y-[1.6vw] landscape:overflow-hidden landscape:p-[2vw]",
        layer ? "landscape:grid-rows-[auto_38%_1fr_auto]" : "landscape:grid-rows-[auto_1fr_auto]",
      )}
    >
      <TopBar data={data} copy={copy} now={now} offline={offline} />
      {layer && (
        <div className="h-[32vh] shrink-0 overflow-hidden rounded-xl landscape:col-span-2 landscape:h-auto" data-testid="tv-layer">
          {layer}
        </div>
      )}
      <NeedsYou data={data} copy={copy} now={now} />
      <EpicRows data={data} copy={copy} now={now} />
      <Feed events={data.feed} copy={copy} />
    </div>
  );
}

function Clock({ now, data, copy, offline, className }: { now: Date; data: TvOverview; copy: TvCopy; offline: boolean; className?: string }) {
  return (
    <div className={cn("flex flex-col items-end", className)}>
      <time className="font-semibold tabular-nums" style={fs("num")} dateTime={now.toISOString()}>
        {tvTime(now)}
      </time>
      <span className="text-right text-muted-foreground" style={fs("small")}>
        {offline ? (
          <span className="flex items-center gap-2" style={{ color: TONE_VAR.warning }}>
            <WifiOff className="size-[1.1em] shrink-0" aria-hidden />
            {copy.offline}
          </span>
        ) : (
          `${data.workspace_name} · ${copy.timeZone}`
        )}
      </span>
    </div>
  );
}

function TopBar({ data, copy, now, offline }: { data: TvOverview; copy: TvCopy; now: Date; offline: boolean }) {
  return (
    <header className="flex flex-col gap-4 landscape:col-span-2 landscape:flex-row landscape:items-center landscape:gap-[2.5vw]">
      <div className="flex items-start justify-between gap-4 landscape:contents">
        <SprintBlock sprint={data.sprint} copy={copy} now={now} />
        <div className="flex shrink-0 flex-col items-end landscape:hidden">
          <time className="font-semibold tabular-nums" style={fs("title")} dateTime={now.toISOString()}>
            {tvTime(now)} <span className="text-muted-foreground" style={fs("small")}>{copy.timeZone}</span>
          </time>
          {offline && (
            <WifiOff className="size-[1.1em]" style={{ ...fs("small"), color: TONE_VAR.warning }} aria-label={copy.offline} />
          )}
        </div>
      </div>
      <div className="grid grid-cols-3 gap-3 landscape:flex landscape:gap-[2.5vw]">
        <Stat label={copy.agentsWorking} value={data.agents_working} icon={Bot} tone={data.agents_working > 0 ? "good" : "neutral"} />
        <Stat label={copy.waitingOwner} value={data.needs_owner_total} icon={Hand} tone={data.needs_owner_total > 0 ? "warning" : "good"} />
        <Stat label={copy.stale} value={data.stale_total} icon={Clock3} tone={data.stale_total > 0 ? "serious" : "good"} />
      </div>
      <Clock now={now} data={data} copy={copy} offline={offline} className="hidden landscape:ml-auto landscape:flex" />
    </header>
  );
}

function SprintBlock({ sprint, copy, now }: { sprint: TvSprint | null; copy: TvCopy; now: Date }) {
  if (!sprint) {
    return (
      <div className="min-w-0 flex-1 text-muted-foreground landscape:min-w-[24vw] landscape:flex-none" style={fs("title")}>
        {copy.noSprint}
      </div>
    );
  }
  const timeline = sprintTimeline(sprint.start_date, sprint.end_date, tvDate(now));
  let when = "";
  if (timeline?.phase === "running") {
    const left = timeline.daysLeft === 0 ? copy.sprintLastDay : copy.sprintLeft(timeline.daysLeft);
    when = `${copy.sprintDay(timeline.day, timeline.totalDays)} · ${left}`;
  } else if (timeline?.phase === "over") {
    when = copy.sprintOver(timeline.daysOver);
  } else if (timeline?.phase === "upcoming") {
    when = copy.sprintUpcoming(timeline.startsInDays);
  }
  const pct = percent(sprint.done, sprint.total);
  return (
    <div className="flex min-w-0 flex-1 flex-col gap-2 landscape:min-w-[24vw] landscape:flex-none">
      <div className="truncate font-semibold" style={fs("title")}>
        {sprint.name}
      </div>
      <div className="text-muted-foreground" style={fs("small")}>
        {when}
      </div>
      <div className="flex items-center gap-3" style={fs("small")}>
        <div className="h-2 flex-1 overflow-hidden rounded-full bg-muted landscape:h-[0.6vw] landscape:min-h-2" role="presentation">
          <div className="h-full rounded-full transition-[width] duration-700" style={{ width: `${pct}%`, background: TONE_VAR.good }} />
        </div>
        <span className="shrink-0 tabular-nums">
          {copy.done(sprint.done, sprint.total)} · {pct}%
        </span>
      </div>
    </div>
  );
}

function Stat({ label, value, icon: Icon, tone }: { label: string; value: number; icon: typeof Bot; tone: TvTone }) {
  return (
    <div className="flex min-w-0 flex-col gap-1 rounded-xl bg-muted/40 p-3 landscape:bg-transparent landscape:p-0" aria-label={`${label}: ${value}`}>
      <span className="flex items-center gap-2 font-bold tabular-nums" style={{ ...fs("num"), color: TONE_VAR[tone] }}>
        <Icon className="size-[0.6em] shrink-0" aria-hidden />
        {value}
      </span>
      <span className="text-muted-foreground" style={fs("small")}>
        {label}
      </span>
    </div>
  );
}

function EpicRows({ data, copy, now }: { data: TvOverview; copy: TvCopy; now: Date }) {
  const doneLabel = data.sprint ? copy.doneInSprint : copy.doneInWeek;
  return (
    <main className="flex flex-col gap-5 landscape:col-start-1 landscape:row-start-[-3] landscape:min-h-0 landscape:gap-[1.2vw] landscape:overflow-hidden">
      {data.epics.length === 0 && (
        <div className="text-muted-foreground" style={fs("title")}>
          {copy.noEpics}
        </div>
      )}
      {data.epics.map((epic) => (
        <EpicRow key={epic.id} epic={epic} copy={copy} doneLabel={doneLabel} now={now} />
      ))}
      <div className="flex flex-wrap gap-x-[2vw] gap-y-1 text-muted-foreground landscape:mt-auto" style={fs("body")}>
        <span>{copy.noEpic(data.no_epic_active)}</span>
        {data.epics_hidden > 0 && <span>{copy.moreEpics(data.epics_hidden)}</span>}
      </div>
    </main>
  );
}

function EpicRow({ epic, copy, doneLabel, now }: { epic: TvEpic; copy: TvCopy; doneLabel: string; now: Date }) {
  const segments = epicSegments(epic);
  const total = segments.reduce((sum, s) => sum + s.count, 0);
  const labels: Record<TvSegmentKey, string> = {
    in_progress: copy.inProgress,
    in_review: copy.inReview,
    blocked: copy.blocked,
    done: doneLabel,
  };
  const hiddenInProgress = epic.in_progress - epic.issues.length;
  return (
    <section className="flex flex-col gap-1.5">
      <div className="flex flex-col gap-1.5 landscape:flex-row landscape:items-center landscape:gap-[1.2vw]">
        <h2 className="truncate font-semibold landscape:w-[26%] landscape:shrink-0" style={fs("title")} title={epic.title}>
          {epic.title}
        </h2>
        <div className="flex h-2.5 w-full shrink-0 overflow-hidden rounded-full bg-muted landscape:h-[0.9vw] landscape:min-h-3 landscape:w-[18%]" role="presentation">
          {segments.map((s) =>
            s.count > 0 ? (
              <div
                key={s.key}
                className="h-full transition-[width] duration-700"
                style={{ width: `${(s.count / Math.max(total, 1)) * 100}%`, background: TONE_VAR[s.tone] }}
              />
            ) : null,
          )}
        </div>
        <div className="flex min-w-0 flex-wrap items-center gap-x-3 gap-y-0.5 landscape:gap-x-[1.2vw]" style={fs("small")}>
          {segments.map((s) => {
            const Icon = SEGMENT_ICON[s.key];
            return (
              <span key={s.key} className={cn("flex items-center gap-1.5 tabular-nums", s.count === 0 && "opacity-40")}>
                <Icon className="size-[1.1em] shrink-0" style={{ color: TONE_VAR[s.tone] }} aria-hidden />
                {s.count} {labels[s.key]}
              </span>
            );
          })}
        </div>
      </div>
      <ul className="flex flex-col gap-1 landscape:gap-0 landscape:pl-[1vw]" style={fs("body")}>
        {epic.issues.map((issue) => (
          <IssueLine key={issue.id} issue={issue} copy={copy} now={now} trailing={copy.duration(elapsedMs(issue.since, now))} />
        ))}
        {hiddenInProgress > 0 && (
          <li className="text-muted-foreground" style={fs("small")}>
            {copy.moreInProgress(hiddenInProgress)}
          </li>
        )}
      </ul>
    </section>
  );
}

function IssueLine({ issue, copy, trailing, now }: { issue: TvIssue; copy: TvCopy; trailing: ReactNode; now: Date }) {
  const stale = issue.stale || elapsedMs(issue.last_activity_at, now) > 24 * 3_600_000;
  return (
    <li
      className={cn(
        "grid min-w-0 grid-cols-[auto_1fr] gap-x-2 transition-opacity duration-700",
        "landscape:flex landscape:items-baseline landscape:gap-[0.8vw]",
        stale && "opacity-45",
      )}
    >
      <span className="shrink-0 tabular-nums text-muted-foreground" style={fs("small")}>
        {issue.identifier}
      </span>
      <span className="min-w-0 truncate">{truncateTitle(issue.title)}</span>
      <span className="col-span-2 shrink-0 text-muted-foreground landscape:ml-auto" style={fs("small")}>
        {issue.assignee_name ?? copy.unassigned} · {trailing}
      </span>
    </li>
  );
}

function NeedsYou({ data, copy, now }: { data: TvOverview; copy: TvCopy; now: Date }) {
  const hidden = data.needs_owner_total - data.needs_owner.length;
  const active = data.needs_owner_total > 0;
  return (
    <aside
      className="flex flex-col gap-3 rounded-xl border-2 p-4 landscape:col-start-2 landscape:row-start-[-3] landscape:min-h-0 landscape:gap-[1vw] landscape:overflow-hidden landscape:p-[1.2vw]"
      style={{ borderColor: active ? TONE_VAR.critical : "var(--border)" }}
      aria-label={copy.needsYou}
    >
      <h2 className="flex items-center gap-3 font-bold" style={{ ...fs("title"), color: active ? TONE_VAR.critical : undefined }}>
        <CircleAlert className="size-[1.1em] shrink-0" aria-hidden />
        {copy.needsYou}
        <span className="ml-auto tabular-nums">{data.needs_owner_total}</span>
      </h2>
      {data.owner_label === "" ? (
        <p className="text-muted-foreground" style={fs("small")}>
          {copy.needsYouNoLabel}
        </p>
      ) : data.needs_owner.length === 0 ? (
        <p className="flex items-center gap-3" style={{ ...fs("body"), color: TONE_VAR.good }}>
          <CircleCheck className="size-[1.1em] shrink-0" aria-hidden />
          {copy.needsYouEmpty}
        </p>
      ) : (
        <ul className="flex flex-col gap-3 landscape:gap-[0.9vw]">
          {data.needs_owner.map((issue) => (
            <li key={issue.id} className="flex flex-col">
              <span className="line-clamp-2" style={fs("body")}>
                <span className="tabular-nums text-muted-foreground" style={fs("small")}>
                  {issue.identifier}
                </span>{" "}
                {truncateTitle(issue.title)}
              </span>
              <span className="text-muted-foreground" style={fs("small")}>
                {copy.waitingFor(copy.duration(elapsedMs(issue.since, now)))}
              </span>
            </li>
          ))}
          {hidden > 0 && (
            <li className="text-muted-foreground" style={fs("small")}>
              {copy.needsYouMore(hidden)}
            </li>
          )}
        </ul>
      )}
    </aside>
  );
}

function Feed({ events, copy }: { events: TvEvent[]; copy: TvCopy }) {
  return (
    <footer className="flex flex-col gap-2 border-t border-border pt-4 landscape:col-span-2 landscape:row-start-[-2] landscape:pt-[1vw]">
      <span className="text-muted-foreground" style={fs("small")}>
        {copy.feedTitle}
      </span>
      {events.length === 0 ? (
        <span className="text-muted-foreground" style={fs("body")}>
          {copy.feedEmpty}
        </span>
      ) : (
        <ul className="grid grid-cols-1 gap-y-2 landscape:grid-cols-4 landscape:gap-x-[2vw]" style={fs("small")}>
          {events.map((event, n) => {
            const failed = event.kind === "run_failed";
            const Icon = failed ? CircleX : CircleCheck;
            const tone: TvTone = failed ? "serious" : "good";
            return (
              <li key={`${event.at}-${n}`} className="flex min-w-0 items-center gap-2">
                <span className="shrink-0 tabular-nums text-muted-foreground">{tvTime(event.at)}</span>
                <Icon className="size-[1.1em] shrink-0" style={{ color: TONE_VAR[tone] }} aria-label={failed ? copy.feedRunFailed : copy.feedDone} />
                <span className="min-w-0 truncate">
                  {event.issue_identifier ? `${event.issue_identifier} ` : ""}
                  {truncateTitle(event.issue_title ?? (failed ? copy.feedRunFailed : copy.feedDone), 40)}
                  {failed && event.actor_name ? ` — ${event.actor_name}` : ""}
                </span>
              </li>
            );
          })}
        </ul>
      )}
    </footer>
  );
}
