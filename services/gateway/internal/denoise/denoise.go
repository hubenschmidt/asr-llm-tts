package denoise

/*
#cgo CFLAGS: -I${SRCDIR}/rnnoise -O2
#cgo LDFLAGS: -lm
#include "rnnoise.h"
*/
import "C"
import "unsafe"

const frameSize = 480 // RNNoise operates on 480-sample frames at 48 kHz

// Denoiser wraps RNNoise for in-process noise suppression.
// Audio arrives at 16 kHz; we upsample 3x to 48 kHz, denoise, then downsample.
type Denoiser struct {
	st *C.DenoiseState
}

// New allocates a new RNNoise denoiser (default model).
func New() *Denoiser {
	return &Denoiser{st: C.rnnoise_create(nil)}
}

// Close frees the C-side denoiser state.
func (d *Denoiser) Close() {
	if d.st == nil {
		return
	}
	C.rnnoise_destroy(d.st)
	d.st = nil
}

// Denoise suppresses noise on 16 kHz float32 samples.
func (d *Denoiser) Denoise(samples []float32) []float32 {
	if len(samples) == 0 {
		return samples
	}

	up := upsample3(samples)

	// Process complete 480-sample frames
	nFrames := len(up) / frameSize
	for i := range nFrames {
		off := i * frameSize
		frame := up[off : off+frameSize]
		C.rnnoise_process_frame(d.st, (*C.float)(unsafe.Pointer(&frame[0])), (*C.float)(unsafe.Pointer(&frame[0])))
	}

	return downsample3(up[:nFrames*frameSize])
}

// upsample3 converts 16 kHz → 48 kHz via linear interpolation (3x).
func upsample3(in []float32) []float32 {
	out := make([]float32, len(in)*3)
	for i, s := range in {
		base := i * 3
		out[base] = s
		var next float32
		if i+1 < len(in) {
			next = in[i+1]
		}
		out[base+1] = s + (next-s)/3
		out[base+2] = s + 2*(next-s)/3
	}
	return out
}

// downsample3 converts 48 kHz → 16 kHz by taking every 3rd sample.
func downsample3(in []float32) []float32 {
	out := make([]float32, len(in)/3)
	for i := range out {
		out[i] = in[i*3]
	}
	return out
}
