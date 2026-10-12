"use client";

/**
 * Two-column frame shared by the epic and sprint pages: the working area on
 * the left, the summary on the right. Below `lg` the summary drops under the
 * working area instead of squeezing it.
 */
export function PlanningLayout({
  main,
  summary,
  summaryLabel,
}: {
  main: React.ReactNode;
  summary: React.ReactNode;
  summaryLabel: string;
}) {
  return (
    <div className="flex min-h-0 flex-1 flex-col overflow-y-auto lg:flex-row lg:overflow-hidden">
      <div className="min-w-0 flex-1 lg:overflow-y-auto">
        <div className="mx-auto w-full max-w-5xl space-y-6 px-6 py-6">{main}</div>
      </div>
      <aside
        aria-label={summaryLabel}
        className="shrink-0 space-y-6 border-t px-6 py-6 lg:w-80 lg:overflow-y-auto lg:border-t-0 lg:border-l lg:px-5"
      >
        {summary}
      </aside>
    </div>
  );
}
