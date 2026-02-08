import client from "./client";

export const fetchModels = () =>
  client.get("/models").then((r) => r.data);

export const preloadModel = (model) =>
  client.post("/models/preload", { model });

export const unloadModel = (type, model) =>
  client.post("/models/unload", { type, model });
