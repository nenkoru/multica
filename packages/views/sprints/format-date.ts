// Sprint dates are calendar days, independent of the viewer's time zone.
export function formatSprintDate(date: string): string {
  return new Date(date).toLocaleDateString("en-US", {
    month: "short",
    day: "numeric",
    timeZone: "UTC",
  });
}
