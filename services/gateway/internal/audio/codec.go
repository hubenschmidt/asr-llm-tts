package audio

import "fmt"

type Codec string

const (
	CodecPCM      Codec = "pcm"
	CodecG711Ulaw Codec = "g711_ulaw"
	CodecG711Alaw Codec = "g711_alaw"
)

// decoder holds a codec's decode function and its fixed output sample rate.
// A rate of 0 means "use the caller-supplied sampleRate" (e.g. PCM passthrough).
type decoder struct {
	fn   func([]byte) []float32
	rate int
}

// decoders maps each supported codec to its decode function and output sample rate.
var decoders = map[Codec]decoder{
	CodecPCM:      {fn: decodePCM, rate: 0},
	CodecG711Ulaw: {fn: decodeG711Ulaw, rate: 8000},
	CodecG711Alaw: {fn: decodeG711Alaw, rate: 8000},
}

// Decode converts encoded audio bytes to float32 PCM samples normalized to [-1, 1].
// Returns samples and the sample rate.
func Decode(data []byte, codec Codec, sampleRate int) ([]float32, int, error) {
	dec, ok := decoders[codec]
	if !ok {
		return nil, 0, fmt.Errorf("unsupported codec: %s", codec)
	}
	rate := dec.rate
	if rate == 0 {
		rate = sampleRate
	}
	return dec.fn(data), rate, nil
}
