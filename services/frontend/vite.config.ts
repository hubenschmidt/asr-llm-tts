import { defineConfig } from "vite";
import react from "@vitejs/plugin-react";

export default defineConfig({
  plugins: [react()],
  server: {
    host: "0.0.0.0",
    port: 3001,
    proxy: {
      "/ws": {
        target: "ws://gateway:8000",
        ws: true,
      },
    },
  },
});
