import client from "./client";

export const unloadAllGPU = () =>
  client.post("/gpu/unload-all").then((r) => r.data);
