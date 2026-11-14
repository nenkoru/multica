import { createRequire } from "node:module";
const requireViews = createRequire(new URL("../../packages/views/package.json", import.meta.url));
const { createServer } = await import(requireViews.resolve("vite"));
const { default: react } = await import(requireViews.resolve("@vitejs/plugin-react"));
const server = await createServer({ root: "scripts/genui-browser", configFile: false, plugins: [react()], server: { host: "127.0.0.1", port: 4173 } });
await server.listen();
try { await import("./check.mjs"); }
finally { await server.close(); }
