import { lazy, StrictMode, Suspense } from "react";
import { createRoot } from "react-dom/client";
import "@fontsource-variable/inter";
import "./styles.css";
import { App } from "./app";
const TvGamePreview = lazy(() =>
  import("./tv-game-preview").then((module) => ({ default: module.TvGamePreview })),
);
const Preview = lazy(() =>
  import("./preview").then((module) => ({ default: module.Preview })),
);

createRoot(document.getElementById("root")!).render(
  <StrictMode>
    {window.location.search === "?tv-game" ? (
      <Suspense fallback={null}>
        <TvGamePreview />
      </Suspense>
    ) : window.location.search === "?preview" ? (
      <Suspense fallback={null}>
        <Preview />
      </Suspense>
    ) : (
      <App />
    )}
  </StrictMode>,
);
