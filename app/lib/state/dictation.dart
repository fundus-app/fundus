import 'dart:async';

import 'package:flutter/foundation.dart';
import 'package:flutter/services.dart';
import 'package:record/record.dart';

import '../api/client.dart';

/// A microphone source: PCM 16-bit little-endian, 16 kHz mono.
abstract class Recorder {
  Future<bool> hasPermission();
  Future<Stream<Uint8List>> start();
  Future<void> stop();
  Future<void> dispose();
}

const dictationSampleRate = 16000;

/// The microphone for this platform: PulseAudio through the Linux runner
/// (no helper binaries, so it works in Flatpak and Snap), the `record`
/// package everywhere else.
Recorder platformRecorder() =>
    !kIsWeb && defaultTargetPlatform == TargetPlatform.linux
    ? LinuxPulseRecorder()
    : PackageRecorder();

/// Linux: the runner's PulseAudio channel (see linux/runner/pulse_recorder.cc).
/// Chunks of S16LE 16 kHz mono arrive as "chunk" calls while recording.
class LinuxPulseRecorder implements Recorder {
  LinuxPulseRecorder({MethodChannel? channel})
    : _channel = channel ?? const MethodChannel(channelName) {
    _channel.setMethodCallHandler(_onCall);
  }
  static const channelName = 'dev.fundus.app/recorder';
  final MethodChannel _channel;
  StreamController<Uint8List>? _ctrl;

  Future<dynamic> _onCall(MethodCall call) async {
    switch (call.method) {
      case 'chunk':
        final data = call.arguments;
        if (data is Uint8List && _ctrl != null && !_ctrl!.isClosed) {
          _ctrl!.add(data);
        }
      case 'error':
        if (_ctrl != null && !_ctrl!.isClosed) {
          _ctrl!.addError(
            StateError('${call.arguments ?? 'recording failed'}'),
          );
        }
    }
    return null;
  }

  /// Linux has no permission prompt of its own; a sandbox portal, if any,
  /// answers when the stream opens.
  @override
  Future<bool> hasPermission() async => true;

  @override
  Future<Stream<Uint8List>> start() async {
    await stop();
    final ctrl = StreamController<Uint8List>();
    _ctrl = ctrl;
    try {
      await _channel.invokeMethod<void>('start');
    } catch (_) {
      _ctrl = null;
      // Never listened to: closing must not wait for a subscriber.
      ctrl.stream.listen(null);
      unawaited(ctrl.close());
      rethrow;
    }
    return ctrl.stream;
  }

  @override
  Future<void> stop() async {
    final ctrl = _ctrl;
    if (ctrl == null) return;
    _ctrl = null;
    try {
      await _channel.invokeMethod<void>('stop');
    } catch (_) {
      // Nothing to stop when the runner never started.
    }
    if (!ctrl.isClosed) {
      if (!ctrl.hasListener) {
        ctrl.stream.listen(null);
      }
      await ctrl.close();
    }
  }

  @override
  Future<void> dispose() async {
    await stop();
    _channel.setMethodCallHandler(null);
  }
}

/// The `record` package (macOS/Windows/web native backends).
class PackageRecorder implements Recorder {
  // Created on first use: the plugin channel does not exist in widget tests
  // and an idle app should not touch the audio stack.
  AudioRecorder? _rec;
  AudioRecorder get _r => _rec ??= AudioRecorder();
  @override
  Future<bool> hasPermission() => _r.hasPermission();
  @override
  Future<Stream<Uint8List>> start() => _r.startStream(
    const RecordConfig(
      encoder: AudioEncoder.pcm16bits,
      sampleRate: dictationSampleRate,
      numChannels: 1,
    ),
  );
  @override
  Future<void> stop() async {
    await _rec?.stop();
  }

  @override
  Future<void> dispose() async {
    try {
      await _rec?.dispose();
    } catch (_) {
      // Nothing to release when the platform side never came up.
    }
    _rec = null;
  }
}

/// Wraps raw PCM in a WAV container.
Uint8List wavFromPcm16(
  List<int> pcm, {
  int sampleRate = dictationSampleRate,
  int channels = 1,
}) {
  final data = Uint8List.fromList(pcm);
  final header = ByteData(44);
  void ascii(int offset, String s) {
    for (var i = 0; i < s.length; i++) {
      header.setUint8(offset + i, s.codeUnitAt(i));
    }
  }

  const bitsPerSample = 16;
  final byteRate = sampleRate * channels * bitsPerSample ~/ 8;
  ascii(0, 'RIFF');
  header.setUint32(4, 36 + data.length, Endian.little);
  ascii(8, 'WAVE');
  ascii(12, 'fmt ');
  header.setUint32(16, 16, Endian.little);
  header.setUint16(20, 1, Endian.little);
  header.setUint16(22, channels, Endian.little);
  header.setUint32(24, sampleRate, Endian.little);
  header.setUint32(28, byteRate, Endian.little);
  header.setUint16(32, channels * bitsPerSample ~/ 8, Endian.little);
  header.setUint16(34, bitsPerSample, Endian.little);
  ascii(36, 'data');
  header.setUint32(40, data.length, Endian.little);
  final out = Uint8List(44 + data.length);
  out.setRange(0, 44, header.buffer.asUint8List());
  out.setRange(44, out.length, data);
  return out;
}

enum DictationStatus { idle, recording, transcribing }

/// Records, uploads to POST /v1/transcribe and hands the transcript back.
/// Nothing is captured automatically: the caller inserts the text into the
/// field for review.
class DictationController extends ChangeNotifier {
  DictationController(
    this.api, {
    Recorder? recorder,
    this.maxBytes = 25 * 1024 * 1024,
  }) : _recorder = recorder ?? platformRecorder();

  final FundusApi api;
  final Recorder _recorder;
  final int maxBytes;

  DictationStatus status = DictationStatus.idle;
  Duration elapsed = Duration.zero;

  /// Last problem, as a sentence for a toast ("Microphone not available.").
  Object? lastError;

  final _chunks = <int>[];
  StreamSubscription<Uint8List>? _sub;
  Timer? _tick;
  DateTime? _startedAt;
  bool _stopping = false;

  bool get isRecording => status == DictationStatus.recording;
  bool get isBusy => status != DictationStatus.idle;

  /// Starts recording; returns false (with [lastError] set) when the
  /// microphone is unavailable or permission was denied.
  Future<bool> start() async {
    if (status != DictationStatus.idle) return false;
    lastError = null;
    try {
      if (!await _recorder.hasPermission()) {
        lastError = 'Microphone not available.';
        notifyListeners();
        return false;
      }
      final stream = await _recorder.start();
      _chunks.clear();
      _sub = stream.listen(
        (b) {
          if (_chunks.length + b.length <= maxBytes) {
            _chunks.addAll(b);
          } else if (!_stopping) {
            stop();
          }
        },
        onError: (Object e) {
          lastError = 'Microphone not available.';
          _abort();
        },
      );
      _startedAt = DateTime.now();
      elapsed = Duration.zero;
      status = DictationStatus.recording;
      _tick = Timer.periodic(const Duration(seconds: 1), (_) {
        elapsed = DateTime.now().difference(_startedAt!);
        notifyListeners();
      });
      notifyListeners();
      return true;
    } catch (_) {
      lastError = 'Microphone not available.';
      status = DictationStatus.idle;
      notifyListeners();
      return false;
    }
  }

  /// Stops recording and transcribes. Returns the transcript ('' on cancel).
  Future<String> stop() async {
    if (status != DictationStatus.recording || _stopping) return '';
    _stopping = true;
    _tick?.cancel();
    try {
      await _recorder.stop();
    } catch (_) {}
    // Not awaited: a cancel future completes in the root zone, which never
    // turns under fake async (dart-lang/sdk#40131). Nothing depends on it.
    unawaited(_sub?.cancel());
    _sub = null;
    final pcm = List<int>.from(_chunks);
    _chunks.clear();
    if (pcm.length < dictationSampleRate ~/ 4) {
      // Under an eighth of a second: nothing worth sending.
      status = DictationStatus.idle;
      _stopping = false;
      notifyListeners();
      return '';
    }
    status = DictationStatus.transcribing;
    notifyListeners();
    try {
      final text = await api.transcribe(wavFromPcm16(pcm));
      return text.trim();
    } catch (e) {
      lastError = e;
      return '';
    } finally {
      status = DictationStatus.idle;
      _stopping = false;
      notifyListeners();
    }
  }

  /// Click on the button: start, or stop and transcribe.
  Future<String> toggle() async {
    if (status == DictationStatus.recording) return stop();
    await start();
    return '';
  }

  void _abort() {
    _tick?.cancel();
    _sub?.cancel();
    _sub = null;
    _chunks.clear();
    status = DictationStatus.idle;
    _stopping = false;
    notifyListeners();
  }

  @override
  void dispose() {
    _tick?.cancel();
    _sub?.cancel();
    _recorder.dispose();
    super.dispose();
  }
}
