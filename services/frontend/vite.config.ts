import { defineConfig } from "vite";
import solidPlugin from "vite-plugin-solid";

export default defineConfig({
  plugins: [solidPlugin()],
  server: {
    host: "0.0.0.0",
    port: 3001,
    proxy: {
      "/ws": {
        target: "ws://gateway:8000",
        ws: true,
      },
      "/api": {
        target: "http://gateway:8000",
      },
    },
  },
});
