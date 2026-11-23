import { defineConfig } from "vitest/config";

export default defineConfig({
  test: {
    environment: "node",
    include: ["runtime.client.test.ts", "interop.test.ts"],
  },
});