"use client";

import { useCallback, useEffect, useState, type CSSProperties, type FormEvent, type ReactNode } from "react";
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
  TvUnauthorizedError,
  createTvSession,
  tvKeys,
  tvOverviewOptions,
  tvWsUrl,
  type TvEpic,
  type TvEvent,
  type TvIssue,
  type TvOverview,
  type TvSprint,
} from "@multica/core/tv";
import { Button } from "@multica/ui/components/ui/button";
import { Input } from "@multica/ui/components/ui/input";
import { cn } from "@multica/ui/lib/utils";
import { TV_COPY, type TvCopy, type TvLang } from "./copy";
import { elapsedMs, epicSegments, percent, truncateTitle, tvDate, tvTime, type TvSegmentKey, type TvTone } from "./model";
import { useTvRealtime } from "./use-tv-realtime";

// A wall screen read from about three metres. Sizes follow the viewport width
// so 1080p and 4K show the same picture; the app's --text-* scale stops at
// 36px, below what a number needs at that distance.
const SCALE: CSSProperties = {
  ["--tv-num" as string]: "max(48px, 3.4vw)",
  ["--tv-title" as string]: "max(24px, 1.45vw)",
  ["--tv-body" as string]: "max(20px, 1.2vw)",
  ["--tv-small" as string]: "max(16px, 0.95vw)",
  // Status palette. Red (critical) is reserved for the "needs you" column.
  ["--tv-good" as string]: "var(--success)",
  ["--tv-warning" as string]: "var(--warning)",
  ["--tv-serious" as string]: "oklch(0.72 0.17 55)",
  ["--tv-critical" as string]: "var(--destructive)",
  ["--tv-neutral" as string]: "var(--info)",
};

const TONE_VAR: Record<TvTone, string> = {
  neutral: "var(--tv-neutral)",
  good: "var(--tv-good)",
  warning: "var(--tv-warning)",
  serious: "var(--tv-serious)",
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

function useNow(intervalMs: number): Date {
  const [now, setNow] = useState(() => new Date());
  useEffect(() => {
    const id = setInterval(() => setNow(new Date()), intervalMs);
    return () => clearInterval(id);
  }, [intervalMs]);
  return now;
}

export interface TvScreenProps {
  /** API origin; empty when the API is served from the page origin. */
  apiBaseUrl?: string;
  /** Explicit realtime URL (…/ws) when the deployment sets one. */
  wsUrl?: string;
  lang?: TvLang;
}

/** Full-screen, read-only TV dashboard authenticated by a kiosk token. */
export function TvScreen({ apiBaseUrl = "", wsUrl, lang = "ru" }: TvScreenProps) {
  const copy = TV_COPY[lang];
  const queryClient = useQueryClient();
  const [pairing, setPairing] = useState<"idle" | "exchanging">("idle");

  // A pairing link carries the token in the fragment, which never reaches a
  // server log. Trade it for the cookie once and drop it from the address.
  useEffect(() => {
    const match = /(?:^#|&)token=([^&]+)/.exec(window.location.hash);
    if (!match?.[1]) return;
    const token = decodeURIComponent(match[1]);
    window.history.replaceState(null, "", window.location.pathname + window.location.search);
    setPairing("exchanging");
    createTvSession(apiBaseUrl, token)
      .catch(() => false)
      .finally(() => {
        setPairing("idle");
        void queryClient.invalidateQueries({ queryKey: tvKeys.overview(apiBaseUrl) });
      });
  }, [apiBaseUrl, queryClient]);

  const query = useQuery({ ...tvOverviewOptions(apiBaseUrl), enabled: pairing === "idle" });
  const unauthorized = query.error instanceof TvUnauthorizedError;

  const refresh = useCallback(() => {
    void queryClient.invalidateQueries({ queryKey: tvKeys.overview(apiBaseUrl) });
  }, [apiBaseUrl, queryClient]);
  const [socketUrl, setSocketUrl] = useState<string | null>(null);
  useEffect(() => {
    setSocketUrl(tvWsUrl({ wsUrl, apiBaseUrl, pageOrigin: window.location.origin }));
  }, [apiBaseUrl, wsUrl]);
  useTvRealtime(query.data && !unauthorized ? socketUrl : null, refresh);

  const now = useNow(10_000);

  return (
    <div
      className="dark fixed inset-0 overflow-hidden bg-background text-foreground antialiased"
      style={SCALE}
      data-testid="tv-screen"
    >
      {unauthorized ? (
        <TvPairing apiBaseUrl={apiBaseUrl} copy={copy} onPaired={refresh} />
      ) : query.data ? (
        <TvDashboard data={query.data} copy={copy} now={now} offline={query.isError} />
      ) : (
        <div className="flex h-full items-center justify-center text-muted-foreground" style={fs("title")}>
          {copy.loading}
        </div>
      )}
    </div>
  );
}

function TvPairing({ apiBaseUrl, copy, onPaired }: { apiBaseUrl: string; copy: TvCopy; onPaired: () => void }) {
  const [token, setToken] = useState("");
  const [rejected, setRejected] = useState(false);
  const [busy, setBusy] = useState(false);
  const submit = async (event: FormEvent) => {
    event.preventDefault();
    setBusy(true);
    const ok = await createTvSession(apiBaseUrl, token.trim()).catch(() => false);
    setBusy(false);
    setRejected(!ok);
    if (ok) {
      setToken("");
      onPaired();
    }
  };
  return (
    <form onSubmit={submit} className="mx-auto flex h-full max-w-[60vw] flex-col justify-center gap-6">
      <h1 className="font-semibold" style={fs("title")}>
        {copy.pairTitle}
      </h1>
      <p className="text-muted-foreground" style={fs("body")}>
        {copy.pairHint}
      </p>
      <div className="flex gap-4">
        <Input
          type="password"
          autoComplete="off"
          aria-label={copy.pairTitle}
          placeholder={copy.pairPlaceholder}
          value={token}
          onChange={(e) => setToken(e.target.value)}
          className="h-auto py-3"
          style={fs("body")}
        />
        <Button type="submit" size="lg" disabled={busy || token.trim() === ""}>
          {copy.pairSubmit}
        </Button>
      </div>
      {rejected && (
        <p role="alert" className="flex items-center gap-3" style={{ ...fs("body"), color: TONE_VAR.serious }}>
          <CircleX className="size-[1.2em]" aria-hidden />
          {copy.pairRejected}
        </p>
      )}
    </form>
  );
}

export function TvDashboard({ data, copy, now, offline }: { data: TvOverview; copy: TvCopy; now: Date; offline: boolean }) {
  return (
    <div className="grid h-full grid-cols-[1fr_28%] grid-rows-[auto_1fr_auto] gap-x-[2vw] gap-y-[1.6vw] p-[2vw]">
      <TopBar data={data} copy={copy} now={now} offline={offline} />
      <EpicRows data={data} copy={copy} now={now} />
      <NeedsYou data={data} copy={copy} now={now} />
      <Feed events={data.feed} copy={copy} />
    </div>
  );
}

function TopBar({ data, copy, now, offline }: { data: TvOverview; copy: TvCopy; now: Date; offline: boolean }) {
  return (
    <header className="col-span-2 flex items-center gap-[2.5vw]">
      <SprintBlock sprint={data.sprint} copy={copy} now={now} />
      <Stat label={copy.agentsWorking} value={data.agents_working} icon={Bot} tone={data.agents_working > 0 ? "good" : "neutral"} />
      <Stat label={copy.waitingOwner} value={data.needs_owner_total} icon={Hand} tone={data.needs_owner_total > 0 ? "warning" : "good"} />
      <Stat label={copy.stale} value={data.stale_total} icon={Clock3} tone={data.stale_total > 0 ? "serious" : "good"} />
      <div className="ml-auto flex flex-col items-end">
        <time className="font-semibold tabular-nums" style={fs("num")} dateTime={now.toISOString()}>
          {tvTime(now)}
        </time>
        <span className="text-muted-foreground" style={fs("small")}>
          {offline ? (
            <span className="flex items-center gap-2" style={{ color: TONE_VAR.warning }}>
              <WifiOff className="size-[1.1em]" aria-hidden />
              {copy.offline}
            </span>
          ) : (
            `${data.workspace_name} · ${copy.timeZone}`
          )}
        </span>
      </div>
    </header>
  );
}

function SprintBlock({ sprint, copy, now }: { sprint: TvSprint | null; copy: TvCopy; now: Date }) {
  if (!sprint) {
    return (
      <div className="min-w-[24vw] text-muted-foreground" style={fs("title")}>
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
    <div className="flex min-w-[24vw] flex-col gap-2">
      <div className="truncate font-semibold" style={fs("title")}>
        {sprint.name}
      </div>
      <div className="text-muted-foreground" style={fs("small")}>
        {when}
      </div>
      <div className="flex items-center gap-3" style={fs("small")}>
        <div className="h-[0.6vw] min-h-2 flex-1 overflow-hidden rounded-full bg-muted" role="presentation">
          <div
            className="h-full rounded-full transition-[width] duration-700"
            style={{ width: `${pct}%`, background: TONE_VAR.good }}
          />
        </div>
        <span className="tabular-nums">
          {copy.done(sprint.done, sprint.total)} · {pct}%
        </span>
      </div>
    </div>
  );
}

function Stat({ label, value, icon: Icon, tone }: { label: string; value: number; icon: typeof Bot; tone: TvTone }) {
  return (
    <div className="flex flex-col gap-1" aria-label={`${label}: ${value}`}>
      <span className="flex items-center gap-3 font-bold tabular-nums" style={{ ...fs("num"), color: TONE_VAR[tone] }}>
        <Icon className="size-[0.7em]" aria-hidden />
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
    <main className="flex min-h-0 flex-col gap-[1.2vw] overflow-hidden">
      {data.epics.length === 0 && (
        <div className="text-muted-foreground" style={fs("title")}>
          {copy.noEpics}
        </div>
      )}
      {data.epics.map((epic) => (
        <EpicRow key={epic.id} epic={epic} copy={copy} doneLabel={doneLabel} now={now} />
      ))}
      <div className="mt-auto flex gap-[2vw] text-muted-foreground" style={fs("body")}>
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
    <section className="flex flex-col gap-1">
      <div className="flex items-center gap-[1.2vw]">
        <h2 className="w-[26%] shrink-0 truncate font-semibold" style={fs("title")} title={epic.title}>
          {epic.title}
        </h2>
        <div className="flex h-[0.9vw] min-h-3 w-[18%] shrink-0 overflow-hidden rounded-full bg-muted" role="presentation">
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
        <div className="flex min-w-0 flex-wrap items-center gap-x-[1.2vw]" style={fs("small")}>
          {segments.map((s) => {
            const Icon = SEGMENT_ICON[s.key];
            return (
              <span key={s.key} className={cn("flex items-center gap-1.5 tabular-nums", s.count === 0 && "opacity-40")}>
                <Icon className="size-[1.1em]" style={{ color: TONE_VAR[s.tone] }} aria-hidden />
                {s.count} {labels[s.key]}
              </span>
            );
          })}
        </div>
      </div>
      <ul className="flex flex-col pl-[1vw]" style={fs("body")}>
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
    <li className={cn("flex min-w-0 items-baseline gap-[0.8vw] transition-opacity duration-700", stale && "opacity-45")}>
      <span className="shrink-0 tabular-nums text-muted-foreground" style={fs("small")}>
        {issue.identifier}
      </span>
      <span className="min-w-0 truncate">{truncateTitle(issue.title)}</span>
      <span className="ml-auto shrink-0 text-muted-foreground" style={fs("small")}>
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
      className="flex min-h-0 flex-col gap-[1vw] overflow-hidden rounded-xl border-2 p-[1.2vw]"
      style={{ borderColor: active ? TONE_VAR.critical : "var(--border)" }}
      aria-label={copy.needsYou}
    >
      <h2 className="flex items-center gap-3 font-bold" style={{ ...fs("title"), color: active ? TONE_VAR.critical : undefined }}>
        <CircleAlert className="size-[1.1em]" aria-hidden />
        {copy.needsYou}
        <span className="ml-auto tabular-nums">{data.needs_owner_total}</span>
      </h2>
      {data.owner_label === "" ? (
        <p className="text-muted-foreground" style={fs("small")}>
          {copy.needsYouNoLabel}
        </p>
      ) : data.needs_owner.length === 0 ? (
        <p className="flex items-center gap-3" style={{ ...fs("body"), color: TONE_VAR.good }}>
          <CircleCheck className="size-[1.1em]" aria-hidden />
          {copy.needsYouEmpty}
        </p>
      ) : (
        <ul className="flex flex-col gap-[0.9vw]">
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
    <footer className="col-span-2 flex flex-col gap-2 border-t border-border pt-[1vw]">
      <span className="text-muted-foreground" style={fs("small")}>
        {copy.feedTitle}
      </span>
      {events.length === 0 ? (
        <span className="text-muted-foreground" style={fs("body")}>
          {copy.feedEmpty}
        </span>
      ) : (
        <ul className="grid grid-cols-4 gap-x-[2vw] gap-y-2" style={fs("small")}>
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
