import client from "./client";

export const fetchSessions = (limit = 50, offset = 0) =>
  client.get(`/traces/sessions?limit=${limit}&offset=${offset}`).then((r) => r.data);

export const fetchSession = (id) =>
  client.get(`/traces/sessions/${id}`).then((r) => r.data);

export const fetchRun = (sessionId, runId) =>
  client.get(`/traces/sessions/${sessionId}/runs/${runId}`).then((r) => r.data);
