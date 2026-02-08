import client from "./client";

export const fetchServices = () =>
  client.get("/services").then((r) => r.data);

export const startService = (name, params) =>
  client.post(`/services/${name}/start${params ? "?" + params : ""}`).then((r) => r.data);

export const stopService = (name) =>
  client.post(`/services/${name}/stop`).then((r) => r.data);

export const fetchSTTModels = () =>
  client.get("/stt/models").then((r) => r.data);

const processNDJSONLines = (lines, onProgress) => {
  const parsed = lines.filter((l) => l.trim()).map((l) => JSON.parse(l));
  const err = parsed.find((m) => m.error);
  if (err) throw new Error(err.error);
  parsed.filter((m) => m.bytes && onProgress).forEach((m) => onProgress(m.bytes, m.total));
  return parsed.find((m) => m.status === "done") ?? null;
};

export const downloadSTTModel = async (name, onProgress) => {
  const resp = await fetch("/api/stt/models/download", {
    method: "POST",
    headers: { "Content-Type": "application/json" },
    body: JSON.stringify({ name }),
  });
  const reader = resp.body.getReader();
  const decoder = new TextDecoder();
  let buf = "";
  let chunk = await reader.read();
  while (!chunk.done) {
    buf += decoder.decode(chunk.value, { stream: true });
    const lines = buf.split("\n");
    buf = lines.pop();
    const result = processNDJSONLines(lines, onProgress);
    if (result) return result;
    chunk = await reader.read();
  }
};
