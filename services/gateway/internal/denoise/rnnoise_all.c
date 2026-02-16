// Amalgamation file — includes all RNNoise sources as a single compilation unit.
// This lets CGo compile vendored C in the rnnoise/ subdirectory.
#include "rnnoise/denoise.c"
#include "rnnoise/rnn.c"
#include "rnnoise/rnn_data.c"
#include "rnnoise/celt_lpc.c"
#include "rnnoise/pitch.c"
#include "rnnoise/kiss_fft.c"
