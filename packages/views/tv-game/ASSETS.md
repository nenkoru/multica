## TV game layer assets

Every picture and sound of the TV game layer is made in this repository. No
third-party sprites, samples, fonts or recordings are bundled.

| Asset | Source | License |
|---|---|---|
| Worker, crystal field, overgrowth, base, wreck, "!" marker sprites | Pixel matrices in `sprites.ts`, drawn for this project | Same as the repository |
| Info ding, caution double gong, master warning | Synthesized at runtime with the Web Audio API in `sound.ts` (oscillators and envelopes, no samples) | Same as the repository |
| Voice lines | Spoken at runtime by the browser's speech synthesis (`ru-RU`); text in `voice.ts` | Same as the repository; the voice itself belongs to the browser/OS |

Rules for new assets: drawn or generated for this project, or CC0 with the
source URL added to this table. Assets from commercial games and recordings of
real aircraft cockpits are not allowed.
