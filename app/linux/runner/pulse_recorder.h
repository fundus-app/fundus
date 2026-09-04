// Native microphone for dictation: PulseAudio simple API (works unchanged
// with PipeWire's pulse compatibility). No helper binaries, so it also runs
// inside Flatpak and Snap sandboxes.
//
// Method channel "dev.fundus.app/recorder":
//   start        opens the default source (S16LE, 16 kHz, mono) and streams
//                100 ms chunks to Dart as "chunk" calls with a Uint8List;
//                fails with code "unavailable" when no source can be opened.
//   stop         stops the stream.
// A read failure mid-recording reaches Dart as an "error" call.
#ifndef FUNDUS_PULSE_RECORDER_H_
#define FUNDUS_PULSE_RECORDER_H_

#include <flutter_linux/flutter_linux.h>

void pulse_recorder_register(FlBinaryMessenger* messenger);

#endif  // FUNDUS_PULSE_RECORDER_H_
