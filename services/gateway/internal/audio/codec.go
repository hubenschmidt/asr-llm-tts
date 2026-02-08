package audio

import "fmt"

type Codec string

const (
	CodecPCM      Codec = "pcm"
	CodecG711Ulaw Codec = "g711_ulaw"
	CodecG711Alaw Codec = "g711_alaw"
)

// Decode converts encoded audio bytes to float32 PCM samples normalized to [-1, 1].
// Returns samples and the sample rate.
func Decode(data []byte, codec Codec, sampleRate int) ([]float32, int, error) {
	if codec == CodecPCM {
		return decodePCM(data), sampleRate, nil
	}

	if codec == CodecG711Ulaw {
		return decodeG711Ulaw(data), 8000, nil
	}

	if codec == CodecG711Alaw {
		return decodeG711Alaw(data), 8000, nil
	}

	return nil, 0, fmt.Errorf("unsupported codec: %s", codec)
}
