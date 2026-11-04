"use client";

import { useEffect, useRef, useState } from "react";
import { toast } from "sonner";
import { useTranscribeMultikaVoice } from "@multica/core/streams";
import { Loader2, Mic, Square } from "@multica/ui/icons";
import { Button } from "@multica/ui/components/ui/button";
import { Tooltip, TooltipContent, TooltipTrigger } from "@multica/ui/components/ui/tooltip";
import { useT } from "../../i18n";

/** A voice note longer than this stops by itself (the server takes ≤ 5 MB). */
export const MAX_VOICE_MS = 60_000;

type Phase = "idle" | "starting" | "recording" | "transcribing";

/**
 * Voice input for Мультика's chat (harness-design §9, §13 day 9): records a
 * note, the server transcribes it, and the text lands in the draft for Руслан
 * to read before sending — a misheard word is never sent on its own.
 */
export function VoiceButton({ onText }: { onText: (text: string) => void }) {
  const { t } = useT("streams");
  const transcribe = useTranscribeMultikaVoice();
  const [phase, setPhase] = useState<Phase>("idle");
  const recorderRef = useRef<MediaRecorder | null>(null);
  const timerRef = useRef<ReturnType<typeof setTimeout> | undefined>(undefined);
  // Set on unmount: a microphone granted after that is released at once.
  const unmountedRef = useRef(false);
  // Set from the first click until the recorder exists or the prompt fails.
  const startingRef = useRef(false);

  useEffect(
    () => () => {
      unmountedRef.current = true;
      clearTimeout(timerRef.current);
      const rec = recorderRef.current;
      recorderRef.current = null;
      if (rec) rec.onerror = null;
      if (rec && rec.state !== "inactive") {
        rec.onstop = null;
        rec.stop();
      }
      rec?.stream.getTracks().forEach((track) => track.stop());
    },
    [],
  );

  const stop = () => {
    clearTimeout(timerRef.current);
    if (recorderRef.current?.state === "recording") recorderRef.current.stop();
  };

  const start = async () => {
    if (startingRef.current || recorderRef.current) return;
    if (typeof MediaRecorder === "undefined" || !navigator.mediaDevices?.getUserMedia) {
      toast.error(t(($) => $.voice.unsupported));
      return;
    }
    // "starting" disables the button while the permission prompt is open, so
    // a second click cannot open a second recorder.
    startingRef.current = true;
    setPhase("starting");
    let stream: MediaStream;
    try {
      stream = await navigator.mediaDevices.getUserMedia({ audio: true });
    } catch {
      startingRef.current = false;
      if (!unmountedRef.current) {
        setPhase("idle");
        toast.error(t(($) => $.voice.no_microphone));
      }
      return;
    }
    startingRef.current = false;
    if (unmountedRef.current) {
      stream.getTracks().forEach((track) => track.stop());
      return;
    }
    const rec = new MediaRecorder(stream);
    const chunks: Blob[] = [];
    rec.ondataavailable = (e) => {
      if (e.data.size > 0) chunks.push(e.data);
    };
    rec.onerror = () => {
      clearTimeout(timerRef.current);
      rec.onstop = null;
      stream.getTracks().forEach((track) => track.stop());
      recorderRef.current = null;
      setPhase("idle");
      toast.error(t(($) => $.voice.failed));
    };
    rec.onstop = () => {
      stream.getTracks().forEach((track) => track.stop());
      recorderRef.current = null;
      const type = rec.mimeType || "audio/webm";
      const audio = new Blob(chunks, { type });
      if (audio.size === 0) {
        setPhase("idle");
        return;
      }
      setPhase("transcribing");
      transcribe.mutate(
        { audio, filename: "voice." + extensionFor(type) },
        {
          onSuccess: (text) => {
            if (text.trim()) onText(text.trim());
            else toast.error(t(($) => $.voice.empty));
          },
          onError: () => toast.error(t(($) => $.voice.failed)),
          onSettled: () => setPhase("idle"),
        },
      );
    };
    recorderRef.current = rec;
    rec.start();
    setPhase("recording");
    timerRef.current = setTimeout(stop, MAX_VOICE_MS);
  };

  const label =
    phase === "recording"
      ? t(($) => $.voice.stop)
      : phase === "transcribing"
        ? t(($) => $.voice.transcribing)
        : t(($) => $.voice.record);

  const button = (
    <Button
      size="icon-sm"
      variant={phase === "recording" ? "destructive" : "ghost"}
      className="rounded-full"
      aria-label={label}
      aria-busy={phase === "transcribing" || undefined}
      disabled={phase === "transcribing" || phase === "starting"}
      onClick={phase === "recording" ? stop : () => void start()}
    >
      {phase === "recording" ? (
        <Square className="fill-current" aria-hidden="true" />
      ) : phase === "transcribing" || phase === "starting" ? (
        <Loader2 className="animate-spin" aria-hidden="true" />
      ) : (
        <Mic aria-hidden="true" />
      )}
    </Button>
  );
  return (
    <Tooltip>
      <TooltipTrigger render={button} />
      <TooltipContent side="top">{label}</TooltipContent>
    </Tooltip>
  );
}

/** File extension the transcription service uses to pick a decoder. */
export function extensionFor(mime: string): string {
  if (mime.includes("ogg")) return "ogg";
  if (mime.includes("mp4") || mime.includes("aac")) return "m4a";
  if (mime.includes("wav")) return "wav";
  return "webm";
}
