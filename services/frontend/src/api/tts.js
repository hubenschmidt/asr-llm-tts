import client from "./client";

export const warmupTTS = (engine) =>
  client.post("/tts/warmup", { engine });
