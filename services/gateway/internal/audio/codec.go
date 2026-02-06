package audio

import (
	"encoding/binary"
	"fmt"
	"math"
)

// ulawTable maps 8-bit μ-law encoded bytes to 16-bit linear PCM values.
var ulawTable [256]int16

// alawTable maps 8-bit A-law encoded bytes to 16-bit linear PCM values.
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

type Codec string

const (
	CodecPCM      Codec = "pcm"
	CodecG711Ulaw Codec = "g711_ulaw"
	CodecG711Alaw Codec = "g711_alaw"
)

// Decode converts encoded audio bytes to float32 PCM samples normalized to [-1, 1].
// Returns samples and the sample rate. For PCM, sampleRate must be provided by the caller.
// For G.711 codecs, the rate is always 8000.
func Decode(data []byte, codec Codec, sampleRate int) ([]float32, int, error) {
	decoders := map[Codec]func([]byte) []float32{
		CodecPCM:      decodePCM,
		CodecG711Ulaw: decodeG711Ulaw,
		CodecG711Alaw: decodeG711Alaw,
	}

	decoder, ok := decoders[codec]
	if !ok {
		return nil, 0, fmt.Errorf("unsupported codec: %s", codec)
	}

	samples := decoder(data)

	rateMap := map[Codec]int{
		CodecPCM:      sampleRate,
		CodecG711Ulaw: 8000,
		CodecG711Alaw: 8000,
	}

	return samples, rateMap[codec], nil
}

func decodePCM(data []byte) []float32 {
	n := len(data) / 2
	samples := make([]float32, n)
	for i := range n {
		s := int16(binary.LittleEndian.Uint16(data[i*2:]))
		samples[i] = float32(s) / math.MaxInt16
	}
	return samples
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
