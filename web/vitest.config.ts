import { defineConfig } from "vitest/config";

// Unit tests of the logic that has no screen: formats, rules, parsers. They run in node;
// test/setup.ts gives them the few browser globals the i18n module reads when it loads.
export default defineConfig({
  test: {
    include: ["src/**/*.test.ts"],
    setupFiles: ["src/test/setup.ts"],
  },
});
