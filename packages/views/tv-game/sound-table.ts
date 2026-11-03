// The one table of the TV alarms (RUSL-1786, Ruslan's choice of 14.10.2026:
// Airbus wherever an Airbus sound exists, Boeing for the rest). Order is
// priority: when two signals want the speaker, the one higher in this list
// plays and the other is suppressed. Thresholds live on the server
// (MULTICA_TV_CONFIG); this table says how each raised signal sounds.
//
// The files are not in this repository (GPL-2.0 recordings from FlightGear
// aircraft, docs/tv-sounds-attribution.md); the server serves them from
// MULTICA_TV_SOUNDS_DIR at /api/tv/sounds/<file>.mp3.

export type AlarmLevel = "warning" | "caution" | "info";

export type SignalId =
  // warnings: repeat until acknowledged
  | "daemon"
  | "llm"
  | "site"
  | "stall"
  | "budget"
  // cautions: sound once
  | "oom"
  | "autopilot"
  | "failed_burst"
  | "duplicate_run"
  | "llm_slow"
  | "master_caution"
  | "minimums"
  // info
  | "cleared"
  | "issue_done"
  | "epic_closed";

export interface SoundEntry {
  id: SignalId;
  level: AlarmLevel;
  /** File name without extension. */
  file: string;
}

export const SOUND_TABLE: readonly SoundEntry[] = [
  { id: "daemon", level: "warning", file: "airbus_master_warning_crc" },
  { id: "llm", level: "warning", file: "airbus_master_warning_crc" },
  { id: "site", level: "warning", file: "boeing_fire_bell_737" },
  { id: "stall", level: "warning", file: "airbus_stall_cricket" },
  { id: "budget", level: "warning", file: "gpws_pull_up_x3" },
  { id: "oom", level: "caution", file: "overspeed_clacker" },
  { id: "autopilot", level: "caution", file: "airbus_ap_disconnect_cavalry" },
  { id: "failed_burst", level: "caution", file: "boeing_stick_shaker" },
  { id: "duplicate_run", level: "caution", file: "tcas_traffic_traffic" },
  { id: "llm_slow", level: "caution", file: "gpws_sink_rate" },
  { id: "master_caution", level: "caution", file: "airbus_master_caution_chime" },
  { id: "minimums", level: "caution", file: "gpws_minimums" },
  { id: "cleared", level: "info", file: "tcas_clear" },
  { id: "issue_done", level: "info", file: "airbus_cabin_chime" },
  { id: "epic_closed", level: "info", file: "airbus_altitude_c_chord" },
];

const BY_ID = new Map(SOUND_TABLE.map((entry, index) => [entry.id, { ...entry, priority: index }]));

export function soundEntry(id: SignalId): SoundEntry & { priority: number } {
  const entry = BY_ID.get(id);
  if (!entry) throw new Error(`unknown TV signal ${id}`);
  return entry;
}

/** Lower is more important. */
export function priorityOf(id: SignalId): number {
  return soundEntry(id).priority;
}

export function isSignalId(value: string): value is SignalId {
  return BY_ID.has(value as SignalId);
}

/** Every distinct file the table needs, for preloading. */
export const SOUND_FILES: readonly string[] = [...new Set(SOUND_TABLE.map((e) => e.file))];
