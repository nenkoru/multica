import { I18nProvider } from "@multica/core/i18n/react";
import enTvGame from "@multica/views/locales/en/tv-game.json";
import { TvGameDemo } from "@multica/views/tv-game";

const resources = { en: { "tv-game": enTvGame } };

// Standalone TV game layer demo (`?tv-game`), no backend: the same script the
// TV screen plays with `?demo=1`. The strip takes 38 % of a 16:9 screen.
export function TvGamePreview() {
  return (
    <I18nProvider locale="en" resources={resources}>
      <div className="flex h-screen w-screen flex-col bg-black p-[2vh]">
        <div className="flex-1" />
        <div className="h-[38vh]">
          <TvGameDemo />
        </div>
      </div>
    </I18nProvider>
  );
}
