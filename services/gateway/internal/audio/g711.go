package audio

import "math"

var ulawTable [256]int16
var alawTable [256]int16

func init() {
	for i := range 256 {
		ulawTable[i] = decodeUlawSample(byte(i))
		alawTable[i] = decodeAlawSample(byte(i))
	}
}

func decodeUlawSample(b byte) int16 {
	b = ^b
	sign := int16(1)
	if b&0x80 != 0 {
		sign = -1
		b &= 0x7F
	}
	exponent := int16((b >> 4) & 0x07)
	mantissa := int16(b & 0x0F)
	sample := (mantissa<<3 + 0x84) << exponent
	sample -= 0x84
	return sign * sample
}

func decodeAlawSample(b byte) int16 {
	b ^= 0x55
	sign := int16(1)
	if b&0x80 == 0 {
		sign = -1
	}
	b &= 0x7F
	exponent := int16((b >> 4) & 0x07)
	mantissa := int16(b & 0x0F)
	if exponent == 0 {
		return sign * (mantissa<<4 + 8)
	}
	return sign * ((mantissa<<4 + 0x108) << (exponent - 1))
}

func decodeG711Ulaw(data []byte) []float32 {
	samples := make([]float32, len(data))
	for i, b := range data {
		samples[i] = float32(ulawTable[b]) / math.MaxInt16
	}
	return samples
}

func decodeG711Alaw(data []byte) []float32 {
	samples := make([]float32, len(data))
	for i, b := range data {
		samples[i] = float32(alawTable[b]) / math.MaxInt16
	}
	return samples
}
