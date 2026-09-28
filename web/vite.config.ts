import { defineConfig } from "vite";
import react from "@vitejs/plugin-react";
import tailwindcss from "@tailwindcss/vite";

// Dev: run the panel with MIKAN_DEV=1 on 127.0.0.1:2053 and admin path "dev-admin-path-0000".
const devAdminPath = process.env.MIKAN_DEV_ADMIN_PATH ?? "dev-admin-path-0000";

export default defineConfig({
  // Relative asset URLs: the Go server injects <base href="/<secret>/"> at runtime.
  base: "./",
  plugins: [react(), tailwindcss()],
  build: {
    outDir: "dist",
    emptyOutDir: true,
    // Never inline assets as data: URIs — the CSP only allows fonts and scripts from 'self'.
    assetsInlineLimit: 0,
    rollupOptions: {
      input: { index: "index.html", sub: "sub.html" },
    },
  },
  server: {
    proxy: {
      "/api": {
        target: "http://127.0.0.1:2053",
        rewrite: (p) => `/${devAdminPath}${p}`,
      },
    },
  },
});
