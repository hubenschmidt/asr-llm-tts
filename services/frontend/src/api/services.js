import client from "./client";

export const fetchServices = () =>
  client.get("/services").then((r) => r.data);

export const startService = (name) =>
  client.post(`/services/${name}/start`).then((r) => r.data);

export const stopService = (name) =>
  client.post(`/services/${name}/stop`).then((r) => r.data);
