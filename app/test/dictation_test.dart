import 'dart:async';
import 'dart:typed_data';

import 'package:flutter/material.dart';
import 'package:flutter/services.dart';
import 'package:flutter_test/flutter_test.dart';
import 'package:fundus_app/state/app_state.dart';
import 'package:fundus_app/state/dictation.dart';
import 'package:fundus_app/ui/capture_bar.dart';
import 'package:fundus_app/ui/theme.dart';
import 'package:provider/provider.dart';

import 'fake_api.dart';

class FakeRecorder implements Recorder {
  FakeRecorder({this.permitted = true});
  bool permitted;
  final controller = StreamController<Uint8List>();
  bool stopped = false;
  @override
  Future<bool> hasPermission() async => permitted;
  @override
  Future<Stream<Uint8List>> start() async => controller.stream;
  @override
  Future<void> stop() async {
    stopped = true;
    await controller.close();
  }

  @override
  Future<void> dispose() async {}
}

void main() {
  test('wav header wraps 16 kHz mono pcm', () {
    final wav = wavFromPcm16(List.filled(3200, 0));
    expect(wav.length, 44 + 3200);
    expect(String.fromCharCodes(wav.sublist(0, 4)), 'RIFF');
    expect(String.fromCharCodes(wav.sublist(8, 12)), 'WAVE');
    expect(ByteData.sublistView(wav).getUint32(24, Endian.little), 16000);
    expect(ByteData.sublistView(wav).getUint16(22, Endian.little), 1);
    expect(ByteData.sublistView(wav).getUint32(40, Endian.little), 3200);
  });

  test(
    'controller records, stops and transcribes; nothing is captured',
    () async {
      final api = FakeApi();
      final rec = FakeRecorder();
      final c = DictationController(api, recorder: rec);
      expect(await c.start(), isTrue);
      expect(c.isRecording, isTrue);
      rec.controller.add(Uint8List(16000));
      await Future<void>.delayed(const Duration(milliseconds: 10));
      final text = await c.stop();
      expect(text, 'call the dentist tomorrow');
      expect(api.transcribed.single, 44 + 16000);
      expect(api.captured, isEmpty);
      expect(c.status, DictationStatus.idle);
      c.dispose();
    },
  );

  test('microphone denied gives a sentence, not a crash', () async {
    final c = DictationController(
      FakeApi(),
      recorder: FakeRecorder(permitted: false),
    );
    expect(await c.start(), isFalse);
    expect(c.lastError, 'Microphone not available.');
    c.dispose();
  });

  test('too short a recording is dropped without an upload', () async {
    final api = FakeApi();
    final rec = FakeRecorder();
    final c = DictationController(api, recorder: rec);
    await c.start();
    rec.controller.add(Uint8List(100));
    await Future<void>.delayed(const Duration(milliseconds: 10));
    expect(await c.stop(), '');
    expect(api.transcribed, isEmpty);
    c.dispose();
  });

  testWidgets(
    'capture bar: mic → transcript appended to the field, not submitted',
    (tester) async {
      final api = FakeApi();
      final rec = FakeRecorder();
      final state = AppState(api, recorder: rec);
      await tester.pumpWidget(
        ChangeNotifierProvider<AppState>.value(
          value: state,
          child: MaterialApp(
            theme: FundusTheme.light(),
            home: Scaffold(
              body: CaptureBar(focusNode: FocusNode(), autofocus: true),
            ),
          ),
        ),
      );
      await tester.pump(const Duration(milliseconds: 100));
      await tester.enterText(find.byKey(const Key('capture-field')), 'Also');
      await tester.tap(find.byKey(const Key('mic-start')));
      await tester.pump();
      expect(find.byKey(const Key('mic-stop')), findsOneWidget);
      rec.controller.add(Uint8List(16000));
      await tester.pump(const Duration(milliseconds: 20));
      await tester.tap(find.byKey(const Key('mic-stop')));
      await tester.pump();
      await tester.pump(const Duration(milliseconds: 50));
      final field = tester.widget<TextField>(
        find.byKey(const Key('capture-field')),
      );
      expect(field.controller!.text, 'Also call the dentist tomorrow');
      expect(api.captured, isEmpty);
      await tester.sendKeyEvent(LogicalKeyboardKey.enter);
      await tester.pump(const Duration(milliseconds: 50));
      expect(api.captured, ['Also call the dentist tomorrow']);
      state.dispose();
    },
  );

  testWidgets(
    'Linux recorder: start/stop on the channel, chunks and errors flow',
    (tester) async {
      const channel = MethodChannel(LinuxPulseRecorder.channelName);
      final calls = <String>[];
      var fail = false;
      TestDefaultBinaryMessengerBinding.instance.defaultBinaryMessenger
          .setMockMethodCallHandler(channel, (call) async {
            calls.add(call.method);
            if (call.method == 'start' && fail) {
              throw PlatformException(
                code: 'unavailable',
                message: 'No such entity',
              );
            }
            return null;
          });
      addTearDown(
        () => TestDefaultBinaryMessengerBinding.instance.defaultBinaryMessenger
            .setMockMethodCallHandler(channel, null),
      );
      Future<void> fromNative(String method, Object? args) async {
        await TestDefaultBinaryMessengerBinding.instance.defaultBinaryMessenger
            .handlePlatformMessage(
              LinuxPulseRecorder.channelName,
              const StandardMethodCodec().encodeMethodCall(
                MethodCall(method, args),
              ),
              (_) {},
            );
      }

      final rec = LinuxPulseRecorder();
      final got = <int>[];
      final stream = await rec.start();
      final sub = stream.listen(got.addAll);
      await fromNative('chunk', Uint8List(3200));
      await fromNative('chunk', Uint8List(3200));
      expect(got.length, 6400);
      await rec.stop();
      expect(calls, ['start', 'stop']);
      // Not awaited: a cancel future completes in the root zone, which never
      // turns under fake async (dart-lang/sdk#40131).
      unawaited(sub.cancel());

      // The controller turns a failed start into the one user-facing message.
      fail = true;
      final api = FakeApi();
      final c = DictationController(api, recorder: rec);
      expect(await c.start(), isFalse);
      expect(c.lastError, 'Microphone not available.');
      expect(c.status, DictationStatus.idle);
      c.dispose();
    },
  );
}
