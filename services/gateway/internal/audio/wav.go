package audio

import (
	"encoding/binary"
	"math"
)

// SamplesToWAV encodes float32 PCM samples as a WAV byte slice.
func SamplesToWAV(samples []float32, sampleRate int) []byte {
	dataLen := len(samples) * 2
	totalLen := 44 + dataLen

	buf := make([]byte, totalLen)
	copy(buf[0:4], "RIFF")
	binary.LittleEndian.PutUint32(buf[4:8], uint32(totalLen-8))
	copy(buf[8:12], "WAVE")
	copy(buf[12:16], "fmt ")
	binary.LittleEndian.PutUint32(buf[16:20], 16)
	binary.LittleEndian.PutUint16(buf[20:22], 1) // PCM
	binary.LittleEndian.PutUint16(buf[22:24], 1) // mono
	binary.LittleEndian.PutUint32(buf[24:28], uint32(sampleRate))
	binary.LittleEndian.PutUint32(buf[28:32], uint32(sampleRate*2)) // byte rate
	binary.LittleEndian.PutUint16(buf[32:34], 2)                    // block align
	binary.LittleEndian.PutUint16(buf[34:36], 16)                   // bits per sample
	copy(buf[36:40], "data")
	binary.LittleEndian.PutUint32(buf[40:44], uint32(dataLen))

	for i, s := range samples {
		clamped := max(-1.0, min(1.0, s))
		val := int16(clamped * math.MaxInt16)
		binary.LittleEndian.PutUint16(buf[44+i*2:], uint16(val))
	}

	return buf
}
