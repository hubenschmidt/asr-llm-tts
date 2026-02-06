package audio

// Resample converts samples from srcRate to dstRate using linear interpolation.
// Returns the input unchanged if rates already match.
func Resample(samples []float32, srcRate, dstRate int) []float32 {
	if srcRate == dstRate {
		return samples
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

	return out
}

func interpolate(samples []float32, idx int, frac float32) float32 {
	if idx+1 >= len(samples) {
		return samples[len(samples)-1]
	}
	return samples[idx]*(1-frac) + samples[idx+1]*frac
}
