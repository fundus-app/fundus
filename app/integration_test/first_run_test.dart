// End-to-end: the real desktop app against a real `fundus` daemon.
//
// Run with a display (Xvfb or a headless Wayland compositor):
//   FUNDUS_BIN=../bin/fundus flutter test integration_test -d linux
import 'dart:io';

import 'package:flutter/material.dart';
import 'package:flutter/services.dart';
import 'package:flutter_test/flutter_test.dart';
import 'package:fundus_app/main.dart';
import 'package:fundus_app/state/settings.dart';
import 'package:integration_test/integration_test.dart';

Future<int> _freePort() async {
  final s = await ServerSocket.bind(InternetAddress.loopbackIPv4, 0);
  final port = s.port;
  await s.close();
  return port;
}

/// Pumps until [finder] matches or [timeout] passes.
Future<void> pumpUntil(
  WidgetTester tester,
  Finder finder, {
  Duration timeout = const Duration(seconds: 20),
}) async {
  final end = DateTime.now().add(timeout);
  while (DateTime.now().isBefore(end)) {
    await tester.pump(const Duration(milliseconds: 200));
    if (finder.evaluate().isNotEmpty) return;
  }
  throw TestFailure('Timed out waiting for $finder');
}

Future<void> pumpUntilGone(
  WidgetTester tester,
  Finder finder, {
  Duration timeout = const Duration(seconds: 20),
}) async {
  final end = DateTime.now().add(timeout);
  while (DateTime.now().isBefore(end)) {
    await tester.pump(const Duration(milliseconds: 200));
    if (finder.evaluate().isEmpty) return;
  }
  throw TestFailure('Timed out waiting for $finder to disappear');
}

Future<void> pressCtrl(WidgetTester tester, LogicalKeyboardKey key) async {
  await tester.sendKeyDownEvent(LogicalKeyboardKey.controlLeft);
  await tester.sendKeyEvent(key);
  await tester.sendKeyUpEvent(LogicalKeyboardKey.controlLeft);
  await tester.pump();
}

void main() {
  IntegrationTestWidgetsFlutterBinding.ensureInitialized();

  late Process daemon;
  late Directory tmp;
  late String url;

  setUpAll(() async {
    final bin = Platform.environment['FUNDUS_BIN'] ?? '../bin/fundus';
    if (!File(bin).existsSync()) {
      throw StateError('daemon binary not found at $bin (set FUNDUS_BIN)');
    }
    tmp = await Directory.systemTemp.createTemp('fundus-e2e-');
    final port = await _freePort();
    url = 'http://127.0.0.1:$port';
    // Fresh config and data: no model configured, so the wizard must appear.
    final env = Map<String, String>.from(Platform.environment)
      ..remove('OPENAI_API_KEY')
      ..remove('ANTHROPIC_API_KEY')
      ..remove('OPENROUTER_API_KEY')
      ..['FUNDUS_CONFIG'] = '${tmp.path}/config.toml'
      ..['XDG_DATA_HOME'] = '${tmp.path}/data'
      ..['XDG_CONFIG_HOME'] = '${tmp.path}/config'
      ..['XDG_STATE_HOME'] = '${tmp.path}/state';
    daemon = await Process.start(bin, [
      'serve',
      '--listen',
      '127.0.0.1:$port',
      '--data',
      '${tmp.path}/fundus-data',
    ], environment: env);
    daemon.stdout.drain<void>();
    daemon.stderr.drain<void>();
    // Wait for /v1/health.
    final client = HttpClient();
    final end = DateTime.now().add(const Duration(seconds: 15));
    while (true) {
      try {
        final r = await client.getUrl(Uri.parse('$url/v1/health'));
        final res = await r.close();
        await res.drain<void>();
        if (res.statusCode == 200) break;
      } catch (_) {}
      if (DateTime.now().isAfter(end)) {
        throw StateError('daemon did not start at $url');
      }
      await Future<void>.delayed(const Duration(milliseconds: 200));
    }
    client.close();
  });

  tearDownAll(() async {
    daemon.kill();
    await daemon.exitCode.timeout(
      const Duration(seconds: 5),
      onTimeout: () => -1,
    );
    try {
      await tmp.delete(recursive: true);
    } catch (_) {}
  });

  testWidgets(
    'first run: wizard → no model → capture → receipt → undo → settings',
    (tester) async {
      final settings = Settings.memory(serverUrl: url);
      await tester.pumpWidget(FundusApp(settings: settings));

      // 1. The wizard appears because no model is configured.
      await pumpUntil(tester, find.text('Connect a model'));
      expect(find.textContaining('You can already capture'), findsOneWidget);

      // 2. "No model for now" files by rules; the inbox appears.
      await tester.tap(find.byKey(const Key('provider-fake')));
      await pumpUntil(tester, find.text('Inbox'));
      expect(find.byKey(const Key('capture-field')), findsOneWidget);

      // 3. Capture a task.
      await tester.tap(find.byKey(const Key('capture-field')));
      await tester.pump();
      await tester.enterText(
        find.byKey(const Key('capture-field')),
        'I must call the dentist tomorrow',
      );
      await tester.sendKeyEvent(LogicalKeyboardKey.enter);
      await pumpUntil(
        tester,
        find.textContaining('Filing'),
        timeout: const Duration(seconds: 5),
      );
      await pumpUntil(tester, find.textContaining('Created task'));

      // 4. The Open view lists it.
      await tester.tap(
        find.descendant(
          of: find.byType(NavigationRail),
          matching: find.text('Open'),
        ),
      );
      await pumpUntil(tester, find.text('I must call the dentist tomorrow'));

      // 5. Undo from the pill: the task disappears, the capture is parked.
      await tester.tap(find.widgetWithText(TextButton, 'Undo').first);
      await pumpUntilGone(
        tester,
        find.text('I must call the dentist tomorrow'),
      );
      await tester.tap(
        find.descendant(
          of: find.byType(NavigationRail),
          matching: find.text('Inbox'),
        ),
      );
      await pumpUntil(tester, find.text('Filing was undone.'));

      // 6. Settings shows the address of the daemon under test.
      // The undo toast floats over the bottom of the rail; let it go first.
      await pumpUntilGone(
        tester,
        find.textContaining('Undid:'),
        timeout: const Duration(seconds: 10),
      );
      await tester.tap(find.byIcon(Icons.settings_outlined));
      await pumpUntil(tester, find.text('Settings'));
      final field = tester.widget<TextField>(
        find.byWidgetPredicate(
          (w) => w is TextField && w.controller?.text == url,
        ),
      );
      expect(field.controller!.text, url);
    },
  );
}
