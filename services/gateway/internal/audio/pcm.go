package audio

import (
	"encoding/binary"
	"math"
)

func decodePCM(data []byte) []float32 {
	n := len(data) / 2
	samples := make([]float32, n)
	for i := range n {
		s := int16(binary.LittleEndian.Uint16(data[i*2:]))
		samples[i] = float32(s) / math.MaxInt16
	}
	return samples
}
