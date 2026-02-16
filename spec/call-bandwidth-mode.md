# Call Bandwidth Mode

Simulate call-center telephony audio characteristics by recording and transmitting at 8kHz narrowband instead of the default wideband rate.

---

## Problem

Call center audio is captured at 8kHz (narrowband telephony, 300Hz–3.4kHz passband). The PoC currently records at the browser's native rate (~48kHz) and requests 16kHz from `getUserMedia`. ASR models trained or evaluated against wideband audio will perform differently than they would on real call center input. To get representative results, we need to simulate the telephony audio path.

---

## Proposal

A server-driven "Audio bandwidth" selector in the UI. The gateway advertises available bandwidth modes via the existing `/api/models` config endpoint. The client fetches them on load and applies the selected mode's audio constraints locally.

When narrowband is selected:

1. **AudioContext created at 8kHz** — `new AudioContext({ sampleRate: 8000 })`
2. **Bandpass filter applied** — BiquadFilterNode chain: highpass at 300Hz + lowpass at 3.4kHz, inserted between mic source and the PCM worklet
3. **`sample_rate: 8000` sent in WS metadata** — gateway already reads this field and handles resampling

This gives a faithful simulation of G.711/PSTN audio characteristics without requiring actual telephony hardware.

---

## Changes

### Gateway

**`routes.go` — Extend `/api/models` response** with a new `audio` key:

```json
{
  "asr": { "engines": [...] },
  "llm": { ... },
  "tts": { "engines": [...] },
  "audio": {
    "bandwidth_modes": [
      {
        "id": "wideband",
        "label": "Wideband (48kHz)",
        "sample_rate": 48000,
        "bandpass": null
      },
      {
        "id": "narrowband",
        "label": "Narrowband — Call Center (8kHz)",
        "sample_rate": 8000,
        "bandpass": { "low_hz": 300, "high_hz": 3400 }
      }
    ],
    "default": "wideband"
  }
}
```

The mode definitions are constructed in `main.go` at startup (not from a config file — just hardcoded for now). This keeps the server as the single source of truth for what modes are available. New modes (e.g., `"telephony_ulaw"` with codec simulation) can be added server-side without frontend changes.

**No audio pipeline changes required.** The gateway already receives `sample_rate` from WS metadata and handles resampling via `audio/resample.go`. ASR backends receive audio at whatever rate they expect (typically 16kHz for Whisper), with the gateway upsampling from 8kHz as needed.

### Frontend

**Fetch on load** — The client already fetches `/api/models` for engine lists. Read `audio.bandwidth_modes` from that same response.

**`CallPanel.tsx`** — Render a dropdown populated from the server response:
- Label: "Audio bandwidth"
- Options: map `bandwidth_modes[].label` values
- Default: whichever mode matches `audio.default`
- Stored as signal, passed to `useAudioStream` via `opts.audioBandwidth()`

**`useAudioStream.js`** — In `startWithMic()`:
- Receive the full mode object (not just the id) so it has `sample_rate` and `bandpass` params
- If `bandpass` is non-null: create `AudioContext({ sampleRate: mode.sample_rate })` and insert BiquadFilterNode chain (highpass at `bandpass.low_hz`, lowpass at `bandpass.high_hz`) between mic source and worklet
- If `bandpass` is null: current behavior (no sampleRate constraint, no filter)
- Include the effective `sample_rate` in WS metadata (already done, line 29)
- Include `audio_bandwidth: mode.id` in WS metadata for server-side logging

### Optional: G.711 codec simulation

For even more realistic telephony simulation, add a third mode server-side:

```json
{
  "id": "telephony_ulaw",
  "label": "Telephony — G.711 μ-law (8kHz)",
  "sample_rate": 8000,
  "bandpass": { "low_hz": 300, "high_hz": 3400 },
  "codec": "ulaw"
}
```

Client applies u-law companding (encode → decode round-trip) to float32 samples before sending, introducing the quantization artifacts characteristic of real phone lines. The gateway already has G.711 codec support in `audio/codec.go`. Gated on the presence of the `codec` field — frontend only needs to check for it.

---

## WS Metadata

| Field | Type | Default | Values |
|---|---|---|---|
| `sample_rate` | int | `48000` | Already exists; set from selected mode's `sample_rate` |
| `audio_bandwidth` | string | `"wideband"` | New; set from selected mode's `id` |

`audio_bandwidth` is informational — the gateway can use it for logging/metrics. The functional difference is carried entirely by `sample_rate` and the client-side filter chain.

---

## Implementation Notes

- `AudioContext({ sampleRate: 8000 })` is well-supported in modern browsers; the browser resamples mic input to match
- The bandpass filter ensures frequencies outside 300Hz–3.4kHz are attenuated, matching PSTN characteristics even if the browser's resampling alone doesn't perfectly band-limit
- Upsampling 8kHz→16kHz for Whisper is lossy by nature (no new information above 4kHz) — this is the point; it tests ASR robustness against narrowband input
- The existing `toPCM16` conversion and worklet pipeline require no changes; they operate on whatever sample rate the AudioContext produces
- Server-driven config means adding new modes (e.g., wideband 16kHz for VoIP, or Opus-compressed narrowband) requires only a gateway change — no frontend redeploy
