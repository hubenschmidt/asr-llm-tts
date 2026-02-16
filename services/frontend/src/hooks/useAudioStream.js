import { createSignal, onCleanup } from "solid-js";

const rmsLevel = (samples) => Math.sqrt(samples.reduce((sum, s) => sum + s * s, 0) / samples.length);

const toPCM16 = (float32) => Int16Array.from(float32, (s) => Math.max(-32768, Math.min(32767, s * 32767)));

const downsample = (samples, srcRate, dstRate) => {
  if (!dstRate || srcRate <= dstRate) return samples;
  const ratio = srcRate / dstRate;
  const out = new Float32Array(Math.floor(samples.length / ratio));
  for (let i = 0; i < out.length; i++) out[i] = samples[Math.floor(i * ratio)];
  return out;
};

export const useAudioStream = (opts) => {
  const [isStreaming, setIsStreaming] = createSignal(false);
  const [isRecording, setIsRecording] = createSignal(false);
  let ws = null;
  let mediaStream = null;
  let worklet = null;
  let audioCtx = null;
  let sendingAudio = true;
  let activeBwMode = null;

  const handleUnload = () => stop();
  window.addEventListener("beforeunload", handleUnload);
  onCleanup(() => window.removeEventListener("beforeunload", handleUnload));

  const connect = (mode) => {
    const protocol = window.location.protocol === "https:" ? "wss:" : "ws:";
    const socket = new WebSocket(`${protocol}//${window.location.host}/ws/call`);
    socket.binaryType = "arraybuffer";

    socket.onopen = () => {
      const nativeRate = audioCtx?.sampleRate ?? 48000;
      const sampleRate = activeBwMode?.sample_rate || nativeRate;
      const meta = {
        codec: "pcm",
        sample_rate: sampleRate,
        audio_bandwidth: opts.audioBandwidth?.() || "wideband",
        tts_engine: opts.ttsEngine(),
        stt_engine: opts.sttEngine(),
        system_prompt: opts.systemPrompt(),
        llm_model: opts.llmModel(),
        llm_engine: opts.llmEngine(),
      };
      if (mode) meta.mode = mode;
      socket.send(JSON.stringify(meta));
    };

    socket.onmessage = (ev) => {
      if (ev.data instanceof ArrayBuffer) {
        opts.onAudio(ev.data);
        return;
      }

      const event = JSON.parse(ev.data);
      const handlers = {
        transcript: () => opts.onTranscript(event.text ?? ""),
        llm_token: () => opts.onLLMToken(event.token ?? ""),
        llm_done: () => opts.onLLMDone(event.text ?? ""),
        thinking_done: () => opts.onThinkingDone?.(event.text ?? ""),
        metrics: () =>
          opts.onMetrics({
            asr_ms: event.asr_ms ?? 0,
            llm_ms: event.llm_ms ?? 0,
            tts_ms: event.tts_ms ?? 0,
            total_ms: event.total_ms ?? 0,
          }),
        error: () => opts.onError(event.text ?? "unknown error"),
      };
      handlers[event.type]?.();
    };

    socket.onerror = () => opts.onError("WebSocket error");

    ws = socket;
    return socket;
  };

  const setupMic = async (socket) => {
    await audioCtx.audioWorklet.addModule("/pcm-sender.js");

    const stream = await navigator.mediaDevices.getUserMedia({
      audio: { sampleRate: 16000, channelCount: 1, echoCancellation: true, autoGainControl: true, noiseSuppression: true },
    });
    mediaStream = stream;

    const source = audioCtx.createMediaStreamSource(stream);
    const node = new AudioWorkletNode(audioCtx, "pcm-sender");
    worklet = node;

    const targetRate = activeBwMode?.sample_rate;
    const nativeRate = audioCtx.sampleRate;

    node.port.onmessage = (ev) => {
      const float32 = ev.data;
      if (!sendingAudio) return;
      opts.onLevel?.(rmsLevel(float32));
      if (socket.readyState !== WebSocket.OPEN) return;
      const resampled = downsample(float32, nativeRate, targetRate);
      socket.send(toPCM16(resampled).buffer);
    };
    let chain = source;
    if (activeBwMode?.bandpass) {
      const hp = audioCtx.createBiquadFilter();
      hp.type = "highpass";
      hp.frequency.value = activeBwMode.bandpass.low_hz;
      const lp = audioCtx.createBiquadFilter();
      lp.type = "lowpass";
      lp.frequency.value = activeBwMode.bandpass.high_hz;
      chain.connect(hp);
      hp.connect(lp);
      chain = lp;
    }
    chain.connect(node);
    node.connect(audioCtx.destination);
  };

  const startWithMic = async (mode) => {
    try {
      activeBwMode = (opts.bandwidthModes?.() || []).find((m) => m.id === opts.audioBandwidth?.()) || null;
      audioCtx = new AudioContext();
      sendingAudio = true;
      const socket = connect(mode);
      await setupMic(socket);
      setIsStreaming(true);
      setIsRecording(true);
    } catch (err) {
      opts.onError(`Mic start failed: ${err instanceof Error ? err.message : err}`);
    }
  };

  const startMic = () => startWithMic();

  const startSnippet = () => startWithMic("snippet");

  const pauseRecording = () => {
    sendingAudio = false;
    setIsRecording(false);
    opts.onLevel?.(0);
  };

  const resumeRecording = () => {
    sendingAudio = true;
    setIsRecording(true);
  };

  const ensureConnected = () => {
    if (ws?.readyState === WebSocket.OPEN) return Promise.resolve();
    return new Promise((resolve) => {
      const socket = connect("text");
      const origOpen = socket.onopen;
      socket.onopen = (ev) => {
        origOpen?.(ev);
        setIsStreaming(true);
        resolve();
      };
    });
  };

  const sendChat = async (text) => {
    await ensureConnected();
    ws.send(JSON.stringify({ action: "chat", message: text }));
  };

  const processSnippet = () => {
    if (ws?.readyState !== WebSocket.OPEN) return;
    ws.send(JSON.stringify({ action: "process" }));
  };

  const startFile = async (file) => {
    const socket = connect();
    const ctx = new AudioContext({ sampleRate: 16000 });
    const arrayBuf = await file.arrayBuffer();
    const audioBuf = await ctx.decodeAudioData(arrayBuf);
    const samples = audioBuf.getChannelData(0);

    await new Promise((resolve) => {
      if (socket.readyState === WebSocket.OPEN) return resolve();
      const origOpen = socket.onopen;
      socket.onopen = (ev) => {
        origOpen?.(ev);
        resolve();
      };
    });

    const chunkSize = 320;
    setIsStreaming(true);

    let i = 0;
    while (i < samples.length && socket.readyState === WebSocket.OPEN) {
      socket.send(toPCM16(samples.slice(i, i + chunkSize)).buffer);
      i += chunkSize;
      await new Promise((r) => setTimeout(r, 20));
    }

    socket.close();
    ctx.close();
    setIsStreaming(false);
  };

  const stop = () => {
    mediaStream?.getTracks().forEach((t) => t.stop());
    worklet?.disconnect();
    audioCtx?.close();
    ws?.close();
    mediaStream = null;
    worklet = null;
    audioCtx = null;
    ws = null;
    sendingAudio = true;
    activeBwMode = null;
    setIsStreaming(false);
    setIsRecording(false);
  };

  return { isStreaming, isRecording, startMic, startSnippet, pauseRecording, resumeRecording, processSnippet, startFile, stop, sendChat };
};
