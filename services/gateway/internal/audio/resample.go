package audio

import "math"

// Resample converts samples from srcRate to dstRate using linear interpolation
// with a windowed-sinc anti-aliasing filter. Returns the input unchanged if
// rates already match.
func Resample(samples []float32, srcRate, dstRate int) []float32 {
	if srcRate == dstRate {
		return samples
	}

	cutoff := float64(min(srcRate, dstRate)) / 2.0

	// Downsampling: filter before interpolation to remove frequencies above new Nyquist.
	if srcRate > dstRate {
		samples = lowPass(samples, cutoff, float64(srcRate), 31)
	}

	ratio := float64(srcRate) / float64(dstRate)
	outLen := int(float64(len(samples)) / ratio)
	out := make([]float32, outLen)

	for i := range outLen {
		srcIdx := float64(i) * ratio
		idx := int(srcIdx)
		frac := float32(srcIdx - float64(idx))
		out[i] = interpolate(samples, idx, frac)
	}

	// Upsampling: filter after interpolation to remove imaging artifacts.
	if dstRate > srcRate {
		out = lowPass(out, cutoff, float64(dstRate), 31)
	}

	return out
}

// lowPass applies a windowed-sinc FIR low-pass filter via convolution.
// For each output sample, only the kernel taps overlapping the valid input range contribute.
func lowPass(samples []float32, cutoff, sampleRate float64, taps int) []float32 {
	kernel := sincKernel(cutoff, sampleRate, taps)
	half := taps / 2
	out := make([]float32, len(samples))

	for i := range samples {
		jStart := max(0, half-i)
		jEnd := min(taps, len(samples)-i+half)
		var sum float32
		for j := jStart; j < jEnd; j++ {
			sum += samples[i+j-half] * kernel[j]
		}
		out[i] = sum
	}

	return out
}

// sincKernel generates a normalized windowed-sinc FIR kernel using a Blackman window.
func sincKernel(cutoff, sampleRate float64, taps int) []float32 {
	fc := cutoff / sampleRate
	half := taps / 2
	kernel := make([]float32, taps)

	var sum float64
	for i := range taps {
		n := float64(i - half)
		sinc := 1.0
		if n != 0 {
			x := 2.0 * math.Pi * fc * n
			sinc = math.Sin(x) / x
		}
		// Blackman window
		w := 0.42 - 0.5*math.Cos(2.0*math.Pi*float64(i)/float64(taps-1)) +
			0.08*math.Cos(4.0*math.Pi*float64(i)/float64(taps-1))
		val := sinc * w
		kernel[i] = float32(val)
		sum += val
	}

	// Normalize so kernel sums to 1 (unity gain at DC).
	scale := float32(1.0 / sum)
	for i := range kernel {
		kernel[i] *= scale
	}

	return kernel
}

func interpolate(samples []float32, idx int, frac float32) float32 {
	if idx+1 >= len(samples) {
		return samples[len(samples)-1]
	}
	return samples[idx]*(1-frac) + samples[idx+1]*frac
}
