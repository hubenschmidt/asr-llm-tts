import { useCallback, useRef, useState } from "react";

export interface PipelineMetrics {
  asr_ms: number;
  llm_ms: number;
  tts_ms: number;
  total_ms: number;
}

export interface PipelineEvent {
  type: string;
  text?: string;
  token?: string;
  latency_ms?: number;
  asr_ms?: number;
  llm_ms?: number;
  tts_ms?: number;
  total_ms?: number;
}

interface UseAudioStreamOptions {
  ttsEngine: string;
  onTranscript: (text: string) => void;
  onLLMToken: (token: string) => void;
  onLLMDone: (text: string) => void;
  onAudio: (audio: ArrayBuffer) => void;
  onMetrics: (m: PipelineMetrics) => void;
  onError: (msg: string) => void;
  onLevel?: (rms: number) => void;
}

export function useAudioStream(opts: UseAudioStreamOptions) {
  const [isStreaming, setIsStreaming] = useState(false);
  const wsRef = useRef<WebSocket | null>(null);
  const mediaStreamRef = useRef<MediaStream | null>(null);
  const workletRef = useRef<AudioWorkletNode | null>(null);
  const ctxRef = useRef<AudioContext | null>(null);

  const connect = useCallback((): WebSocket => {
    const protocol = window.location.protocol === "https:" ? "wss:" : "ws:";
    const ws = new WebSocket(`${protocol}//${window.location.host}/ws/call`);
    ws.binaryType = "arraybuffer";

    ws.onopen = () => {
      const sampleRate = ctxRef.current?.sampleRate ?? 48000;
      ws.send(
        JSON.stringify({
          codec: "pcm",
          sample_rate: sampleRate,
          tts_engine: opts.ttsEngine,
          mode: "conversation",
        }),
      );
    };

    ws.onmessage = (ev) => {
      if (ev.data instanceof ArrayBuffer) {
        opts.onAudio(ev.data);
        return;
      }

      const event: PipelineEvent = JSON.parse(ev.data);
      const handlers: Record<string, () => void> = {
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

    ws.onerror = () => opts.onError("WebSocket error");

    wsRef.current = ws;
    return ws;
  }, [opts]);

  const startMic = useCallback(async () => {
    try {
      const audioCtx = new AudioContext();
      ctxRef.current = audioCtx;
      const ws = connect();

      await audioCtx.audioWorklet.addModule(createWorkletURL());

      const stream = await navigator.mediaDevices.getUserMedia({
        audio: { sampleRate: 16000, channelCount: 1, echoCancellation: true },
      });
      mediaStreamRef.current = stream;

      const source = audioCtx.createMediaStreamSource(stream);
      const worklet = new AudioWorkletNode(audioCtx, "pcm-sender");
      workletRef.current = worklet;

      worklet.port.onmessage = (ev) => {
        const float32 = ev.data as Float32Array;

        if (opts.onLevel) {
          let sum = 0;
          for (let i = 0; i < float32.length; i++) sum += float32[i] * float32[i];
          opts.onLevel(Math.sqrt(sum / float32.length));
        }

        if (ws.readyState !== WebSocket.OPEN) return;

        const pcm16 = new Int16Array(float32.length);
        for (let i = 0; i < float32.length; i++) {
          pcm16[i] = Math.max(-32768, Math.min(32767, float32[i] * 32767));
        }
        ws.send(pcm16.buffer);
      };

      source.connect(worklet);
      worklet.connect(audioCtx.destination);
      setIsStreaming(true);
    } catch (err) {
      opts.onError(`Mic start failed: ${err instanceof Error ? err.message : err}`);
    }
  }, [connect, opts]);

  const startFile = useCallback(
    async (file: File) => {
      const ws = connect();
      const audioCtx = new AudioContext({ sampleRate: 16000 });
      const arrayBuf = await file.arrayBuffer();
      const audioBuf = await audioCtx.decodeAudioData(arrayBuf);
      const samples = audioBuf.getChannelData(0);

      // Wait for WebSocket to open
      await new Promise<void>((resolve) => {
        if (ws.readyState === WebSocket.OPEN) return resolve();
        const origOpen = ws.onopen;
        ws.onopen = (ev) => {
          if (origOpen) (origOpen as (ev: Event) => void)(ev);
          resolve();
        };
      });

      // Stream chunks at ~real-time rate (320 samples = 20ms at 16kHz)
      const chunkSize = 320;
      setIsStreaming(true);

      for (let i = 0; i < samples.length; i += chunkSize) {
        if (ws.readyState !== WebSocket.OPEN) break;

        const chunk = samples.slice(i, i + chunkSize);
        const pcm16 = new Int16Array(chunk.length);
        for (let j = 0; j < chunk.length; j++) {
          pcm16[j] = Math.max(-32768, Math.min(32767, chunk[j] * 32767));
        }
        ws.send(pcm16.buffer);
        await new Promise((r) => setTimeout(r, 20));
      }

      ws.close();
      audioCtx.close();
      setIsStreaming(false);
    },
    [connect],
  );

  const stop = useCallback(() => {
    mediaStreamRef.current?.getTracks().forEach((t) => t.stop());
    workletRef.current?.disconnect();
    ctxRef.current?.close();
    wsRef.current?.close();
    mediaStreamRef.current = null;
    workletRef.current = null;
    ctxRef.current = null;
    wsRef.current = null;
    setIsStreaming(false);
  }, []);

  return { isStreaming, startMic, startFile, stop };
}

function createWorkletURL(): string {
  const code = `
class PCMSender extends AudioWorkletProcessor {
  process(inputs) {
    const input = inputs[0];
    if (input.length > 0) {
      this.port.postMessage(new Float32Array(input[0]));
    }
    return true;
  }
}
registerProcessor('pcm-sender', PCMSender);
`;
  const blob = new Blob([code], { type: "application/javascript" });
  return URL.createObjectURL(blob);
}
