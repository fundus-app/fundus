// End-to-end: the real desktop app against a real `fundus` daemon.
//
// Run with a display (Xvfb or a headless Wayland compositor):
//   FUNDUS_BIN=../bin/fundus flutter test integration_test -d linux
//
// On failure the test prints the step that failed, the daemon log tail, the
// daemon's /v1/health and /v1/inbox, and a trimmed widget tree, and writes
// the same (plus a screenshot when the platform supports it) to build/e2e/.
import 'dart:convert';
import 'dart:io';

import 'package:flutter/material.dart';
import 'package:flutter/services.dart';
import 'package:flutter_test/flutter_test.dart';
import 'package:fundus_app/main.dart';
import 'package:fundus_app/state/settings.dart';
import 'package:integration_test/integration_test.dart';
import 'package:window_manager/window_manager.dart';

const _wizardTimeout = Duration(seconds: 60);
const _healthTimeout = Duration(seconds: 30);
const _receiptTimeout = Duration(seconds: 30);
const _stepTimeout = Duration(seconds: 30);

final _outDir = Directory('build/e2e');
late IntegrationTestWidgetsFlutterBinding _binding;
late Process _daemon;
late Directory _tmp;
late String _url;
late IOSink _daemonLog;
final _daemonLogFile = File('build/e2e/daemon.log');
String _step = 'start';

Future<int> _freePort() async {
  final s = await ServerSocket.bind(InternetAddress.loopbackIPv4, 0);
  final port = s.port;
  await s.close();
  return port;
}

Future<String> _httpGet(String path) async {
  final client = HttpClient();
  try {
    final req = await client
        .getUrl(Uri.parse('$_url$path'))
        .timeout(const Duration(seconds: 5));
    final res = await req.close().timeout(const Duration(seconds: 5));
    final body = await res.transform(utf8.decoder).join();
    return '${res.statusCode} $body';
  } catch (e) {
    return 'request failed: $e';
  } finally {
    client.close();
  }
}

/// Pumps until [finder] matches or [timeout] passes.
Future<void> pumpUntil(
  WidgetTester tester,
  Finder finder, {
  Duration timeout = _stepTimeout,
}) async {
  final end = DateTime.now().add(timeout);
  while (DateTime.now().isBefore(end)) {
    await tester.pump(const Duration(milliseconds: 200));
    if (finder.evaluate().isNotEmpty) return;
  }
  throw TestFailure(
    '[$_step] timed out after ${timeout.inSeconds}s waiting for $finder',
  );
}

Future<void> pumpUntilGone(
  WidgetTester tester,
  Finder finder, {
  Duration timeout = _stepTimeout,
}) async {
  final end = DateTime.now().add(timeout);
  while (DateTime.now().isBefore(end)) {
    await tester.pump(const Duration(milliseconds: 200));
    if (finder.evaluate().isEmpty) return;
  }
  throw TestFailure(
    '[$_step] timed out after ${timeout.inSeconds}s waiting for $finder to disappear',
  );
}

/// pumpAndSettle with a timeout; falls back to a short pump loop when
/// something keeps animating (spinners, toasts).
Future<void> settle(WidgetTester tester) async {
  try {
    await tester.pumpAndSettle(
      const Duration(milliseconds: 100),
      EnginePhase.sendSemanticsUpdate,
      const Duration(seconds: 5),
    );
  } catch (_) {
    for (var i = 0; i < 5; i++) {
      await tester.pump(const Duration(milliseconds: 100));
    }
  }
}

/// Taps a finder, scrolling it into view first and retrying once.
Future<void> tapVisible(WidgetTester tester, Finder finder) async {
  await pumpUntil(tester, finder);
  try {
    await tester.ensureVisible(finder.first);
  } catch (_) {}
  await tester.pump(const Duration(milliseconds: 100));
  await tester.tap(finder.first, warnIfMissed: false);
  await tester.pump(const Duration(milliseconds: 200));
}

Future<void> step(String name, Future<void> Function() body) async {
  _step = name;
  // ignore: avoid_print
  print('[e2e] step: $name');
  await body();
}

/// Makes sure the window is wide enough for the three-column layout, whatever
/// the display gives us (Xvfb defaults, no window manager, small screens).
Future<void> _ensureWindowSize(WidgetTester tester) async {
  const wanted = Size(1400, 880);
  try {
    await windowManager.ensureInitialized();
    await windowManager.setSize(wanted);
    await windowManager.setPosition(Offset.zero);
    for (var i = 0; i < 10; i++) {
      await tester.pump(const Duration(milliseconds: 100));
      if (tester.view.physicalSize.width / tester.view.devicePixelRatio >=
          1000) {
        break;
      }
    }
  } catch (e) {
    // ignore: avoid_print
    print('[e2e] window_manager unavailable: $e');
  }
  final logical = tester.view.physicalSize / tester.view.devicePixelRatio;
  if (logical.width < 1000 || logical.height < 600) {
    // Last resort: lay out for a large view even if the surface is small.
    tester.view.physicalSize = wanted * tester.view.devicePixelRatio;
    await tester.pump();
    // ignore: avoid_print
    print('[e2e] forced view size to $wanted (surface was $logical)');
  }
}

Future<void> _diagnose(WidgetTester tester, Object error) async {
  final sb = StringBuffer();
  sb.writeln('==== e2e failure in step "$_step" ====');
  sb.writeln(error);
  sb.writeln(
    '---- view: physical ${tester.view.physicalSize}, dpr ${tester.view.devicePixelRatio}',
  );
  sb.writeln('---- /v1/health: ${await _httpGet('/v1/health')}');
  sb.writeln('---- /v1/inbox: ${await _httpGet('/v1/inbox')}');
  try {
    await _daemonLog.flush();
    final lines = _daemonLogFile.readAsLinesSync();
    sb.writeln(
      '---- daemon log (last ${lines.length < 40 ? lines.length : 40} of ${lines.length} lines):',
    );
    for (final l in lines.skip(lines.length > 40 ? lines.length - 40 : 0)) {
      sb.writeln(l);
    }
  } catch (e) {
    sb.writeln('---- daemon log unavailable: $e');
  }
  try {
    final tree =
        WidgetsBinding.instance.rootElement?.toStringDeep() ?? '(no tree)';
    final lines = tree.split('\n');
    sb.writeln('---- widget tree (first 120 of ${lines.length} lines):');
    sb.writeln(lines.take(120).join('\n'));
  } catch (e) {
    sb.writeln('---- widget tree unavailable: $e');
  }
  try {
    final bytes = await _binding.takeScreenshot('failure');
    if (bytes.isNotEmpty) {
      File('${_outDir.path}/failure.png').writeAsBytesSync(bytes);
      sb.writeln(
        '---- screenshot: ${_outDir.path}/failure.png (${bytes.length} bytes)',
      );
    }
  } catch (e) {
    sb.writeln('---- screenshot not available on this platform: $e');
  }
  final text = sb.toString();
  // ignore: avoid_print
  print(text);
  try {
    File('${_outDir.path}/diagnostics.txt').writeAsStringSync(text);
  } catch (_) {}
}

void main() {
  _binding = IntegrationTestWidgetsFlutterBinding.ensureInitialized();

  setUpAll(() async {
    _outDir.createSync(recursive: true);
    final bin = Platform.environment['FUNDUS_BIN'] ?? '../bin/fundus';
    if (!File(bin).existsSync()) {
      throw StateError(
        'daemon binary not found at $bin (cwd ${Directory.current.path}; set FUNDUS_BIN)',
      );
    }
    _tmp = await Directory.systemTemp.createTemp('fundus-e2e-');
    final port = await _freePort();
    _url = 'http://127.0.0.1:$port';
    // Fresh config and data: no model configured, so the wizard must appear.
    final env = Map<String, String>.from(Platform.environment)
      ..remove('OPENAI_API_KEY')
      ..remove('ANTHROPIC_API_KEY')
      ..remove('OPENROUTER_API_KEY')
      ..['FUNDUS_CONFIG'] = '${_tmp.path}/config.toml'
      ..['XDG_DATA_HOME'] = '${_tmp.path}/data'
      ..['XDG_CONFIG_HOME'] = '${_tmp.path}/config'
      ..['XDG_STATE_HOME'] = '${_tmp.path}/state';
    _daemonLog = _daemonLogFile.openWrite();
    _daemon = await Process.start(bin, [
      'serve',
      '--listen',
      '127.0.0.1:$port',
      '--data',
      '${_tmp.path}/fundus-data',
      '--log-level',
      'debug',
    ], environment: env);
    _daemon.stdout.listen(_daemonLog.add);
    _daemon.stderr.listen(_daemonLog.add);
    // ignore: avoid_print
    print(
      '[e2e] daemon $bin pid ${_daemon.pid} at $_url, log ${_daemonLogFile.path}',
    );
    final end = DateTime.now().add(_healthTimeout);
    while (true) {
      final h = await _httpGet('/v1/health');
      if (h.startsWith('200 ')) {
        // ignore: avoid_print
        print('[e2e] health: $h');
        break;
      }
      if (DateTime.now().isAfter(end)) {
        await _daemonLog.flush();
        throw StateError(
          'daemon did not answer at $_url within ${_healthTimeout.inSeconds}s: $h',
        );
      }
      await Future<void>.delayed(const Duration(milliseconds: 250));
    }
  });

  tearDownAll(() async {
    _daemon.kill();
    await _daemon.exitCode.timeout(
      const Duration(seconds: 5),
      onTimeout: () => -1,
    );
    await _daemonLog.flush();
    await _daemonLog.close();
    try {
      await _tmp.delete(recursive: true);
    } catch (_) {}
  });

  testWidgets(
    'first run: wizard → no model → capture → receipt → undo → settings',
    (tester) async {
      try {
        final settings = Settings.memory(serverUrl: _url);
        await tester.pumpWidget(FundusApp(settings: settings));
        await _ensureWindowSize(tester);

        await step('wizard appears (setup_needed)', () async {
          await pumpUntil(
            tester,
            find.text('Connect a model'),
            timeout: _wizardTimeout,
          );
          expect(
            find.textContaining('You can already capture'),
            findsOneWidget,
          );
        });

        await step('choose "No model for now"', () async {
          await tapVisible(tester, find.byKey(const Key('provider-fake')));
          await pumpUntil(
            tester,
            find.byKey(const Key('capture-field')),
            timeout: _wizardTimeout,
          );
          expect(
            find.byType(NavigationRail),
            findsOneWidget,
            reason: 'three-column layout expected (window too small?)',
          );
        });

        await step('capture a task', () async {
          await tapVisible(tester, find.byKey(const Key('capture-field')));
          await tester.enterText(
            find.byKey(const Key('capture-field')),
            'I must call the dentist tomorrow',
          );
          await tester.sendKeyEvent(LogicalKeyboardKey.enter);
          // The pill shows "Filing…" until the receipt arrives; on a fast
          // daemon the receipt may already be there, so accept either.
          await pumpUntil(
            tester,
            find.textContaining(RegExp('Filing|Created task')),
            timeout: _receiptTimeout,
          );
          await pumpUntil(
            tester,
            find.textContaining('Created task'),
            timeout: _receiptTimeout,
          );
        });

        await step('Open view lists the task', () async {
          await tapVisible(
            tester,
            find.descendant(
              of: find.byType(NavigationRail),
              matching: find.text('Open'),
            ),
          );
          await pumpUntil(
            tester,
            find.text('I must call the dentist tomorrow'),
          );
        });

        await step('undo from the pill', () async {
          await tapVisible(tester, find.widgetWithText(TextButton, 'Undo'));
          await pumpUntilGone(
            tester,
            find.text('I must call the dentist tomorrow'),
          );
          await tapVisible(
            tester,
            find.descendant(
              of: find.byType(NavigationRail),
              matching: find.text('Inbox'),
            ),
          );
          await pumpUntil(tester, find.text('Filing was undone.'));
        });

        await step('settings shows the address', () async {
          // The undo toast floats over the bottom of the rail; let it go first.
          await pumpUntilGone(
            tester,
            find.textContaining('Undid:'),
            timeout: const Duration(seconds: 15),
          );
          await tapVisible(tester, find.byIcon(Icons.settings_outlined));
          await pumpUntil(tester, find.text('Settings'));
          await settle(tester);
          expect(
            find.byWidgetPredicate(
              (w) => w is TextField && w.controller?.text == _url,
            ),
            findsOneWidget,
            reason: 'Fundus address field should show $_url',
          );
        });
      } catch (e) {
        await _diagnose(tester, e);
        rethrow;
      }
    },
  );
}
