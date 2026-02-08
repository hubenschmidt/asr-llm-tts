import { createSignal } from "solid-js";

export const useAudioStream = (opts) => {
  const [isStreaming, setIsStreaming] = createSignal(false);
  let ws = null;
  let mediaStream = null;
  let worklet = null;
  let audioCtx = null;

  const connect = () => {
    const protocol = window.location.protocol === "https:" ? "wss:" : "ws:";
    const socket = new WebSocket(`${protocol}//${window.location.host}/ws/call`);
    socket.binaryType = "arraybuffer";

    socket.onopen = () => {
      const sampleRate = audioCtx?.sampleRate ?? 48000;
      socket.send(
        JSON.stringify({
          codec: "pcm",
          sample_rate: sampleRate,
          tts_engine: opts.ttsEngine(),
          stt_engine: opts.sttEngine(),
          system_prompt: opts.systemPrompt(),
          llm_model: opts.llmModel(),
          mode: "conversation",
        }),
      );
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

  const startMic = async () => {
    try {
      audioCtx = new AudioContext();
      const socket = connect();

      await audioCtx.audioWorklet.addModule("/pcm-sender.js");

      const stream = await navigator.mediaDevices.getUserMedia({
        audio: { sampleRate: 16000, channelCount: 1, echoCancellation: true, autoGainControl: true, noiseSuppression: true },
      });
      mediaStream = stream;

      const source = audioCtx.createMediaStreamSource(stream);
      const node = new AudioWorkletNode(audioCtx, "pcm-sender");
      worklet = node;

      node.port.onmessage = (ev) => {
        const float32 = ev.data;

        if (opts.onLevel) {
          let sum = 0;
          for (let i = 0; i < float32.length; i++) sum += float32[i] * float32[i];
          opts.onLevel(Math.sqrt(sum / float32.length));
        }

        if (socket.readyState !== WebSocket.OPEN) return;

        const pcm16 = new Int16Array(float32.length);
        for (let i = 0; i < float32.length; i++) {
          pcm16[i] = Math.max(-32768, Math.min(32767, float32[i] * 32767));
        }
        socket.send(pcm16.buffer);
      };

      source.connect(node);
      node.connect(audioCtx.destination);
      setIsStreaming(true);
    } catch (err) {
      opts.onError(`Mic start failed: ${err instanceof Error ? err.message : err}`);
    }
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
        if (origOpen) origOpen(ev);
        resolve();
      };
    });

    const chunkSize = 320;
    setIsStreaming(true);

    for (let i = 0; i < samples.length; i += chunkSize) {
      if (socket.readyState !== WebSocket.OPEN) break;

      const chunk = samples.slice(i, i + chunkSize);
      const pcm16 = new Int16Array(chunk.length);
      for (let j = 0; j < chunk.length; j++) {
        pcm16[j] = Math.max(-32768, Math.min(32767, chunk[j] * 32767));
      }
      socket.send(pcm16.buffer);
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
    setIsStreaming(false);
  };

  return { isStreaming, startMic, startFile, stop };
};
