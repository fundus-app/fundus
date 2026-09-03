import 'package:fundus_app/api/models.dart';
import 'package:fundus_app/desktop/daemon_launcher.dart';
import 'package:fundus_app/state/app_state.dart';
import 'package:fundus_app/ui/setup/setup_wizard.dart';
import 'package:fundus_app/ui/theme.dart';
import 'package:flutter/material.dart';
import 'package:flutter_test/flutter_test.dart';
import 'package:provider/provider.dart';

import 'fake_api.dart';

Widget _wrap(AppState state, Widget child) =>
    ChangeNotifierProvider<AppState>.value(
      value: state,
      child: MaterialApp(theme: FundusTheme.light(), home: child),
    );

void main() {
  testWidgets('wizard: provider card → key → test → models → save', (
    tester,
  ) async {
    tester.view.physicalSize = const Size(1200, 1400);
    tester.view.devicePixelRatio = 1;
    addTearDown(tester.view.reset);
    final api = FakeApi();
    final state = AppState(api);
    var done = false;
    await tester.pumpWidget(
      _wrap(state, SetupWizard(onDone: () => done = true, onSkip: () {})),
    );
    await tester.pump();
    await tester.pump(const Duration(milliseconds: 100));

    expect(find.text('Connect a model'), findsOneWidget);
    expect(find.textContaining('You can already capture'), findsOneWidget);
    await tester.tap(find.byKey(const Key('provider-openai')));
    await tester.pumpAndSettle();

    await tester.enterText(find.byKey(const Key('api-key')), 'sk-test-1234');
    await tester.pump();
    await tester.tap(find.byKey(const Key('test-connection')));
    await tester.pumpAndSettle();

    // The typed key is only sent to list models (POST), never stored yet.
    expect(api.settingsPatches, isEmpty);
    expect(api.modelsRequested, ['openai']);
    expect(api.modelsKeys, ['sk-test-1234']);
    expect(find.textContaining('Reachable'), findsOneWidget);

    await tester.tap(find.byKey(const Key('continue')));
    await tester.pumpAndSettle();
    expect(find.byKey(const Key('model-triage')), findsOneWidget);

    await tester.tap(find.byKey(const Key('save')));
    await tester.pumpAndSettle();

    // Provider, models and the key are stored together at the end.
    final last = api.settingsPatches.last;
    expect(last['triage'], {'provider': 'openai', 'model': 'gpt-5.6-luna'});
    expect(last['chat'], {'provider': 'openai', 'model': 'gpt-5.6-terra'});
    expect(last['dictation'], {
      'provider': 'openai',
      'model': 'gpt-transcribe',
    });
    expect(last['providers']['openai']['api_key'], 'sk-test-1234');
    expect(api.setupNeeded, isFalse);
    expect(done, isTrue);
    state.dispose();
  });

  testWidgets('wizard: failed probe blocks continue', (tester) async {
    tester.view.physicalSize = const Size(1200, 1400);
    tester.view.devicePixelRatio = 1;
    addTearDown(tester.view.reset);
    final api = FakeApi()..probeOk = false;
    final state = AppState(api);
    await tester.pumpWidget(_wrap(state, SetupWizard(onSkip: () {})));
    await tester.pump(const Duration(milliseconds: 100));
    await tester.tap(find.byKey(const Key('provider-anthropic')));
    await tester.pumpAndSettle();
    await tester.enterText(find.byKey(const Key('api-key')), 'bad-key');
    await tester.pump();
    await tester.tap(find.byKey(const Key('test-connection')));
    await tester.pumpAndSettle();
    expect(find.text('The key was rejected.'), findsOneWidget);
    expect(api.settingsPatches, isEmpty);
    final btn = tester.widget<FilledButton>(find.byKey(const Key('continue')));
    expect(btn.onPressed, isNull);
    state.dispose();
  });

  testWidgets('wizard: no model for now saves the fake provider', (
    tester,
  ) async {
    tester.view.physicalSize = const Size(1200, 1400);
    tester.view.devicePixelRatio = 1;
    addTearDown(tester.view.reset);
    final api = FakeApi();
    final state = AppState(api);
    await tester.pumpWidget(_wrap(state, SetupWizard(onSkip: () {})));
    await tester.pump(const Duration(milliseconds: 100));
    await tester.tap(find.byKey(const Key('provider-fake')));
    for (var i = 0; i < 6; i++) {
      await tester.pump(const Duration(milliseconds: 200));
    }
    expect(api.settingsPatches.last['triage'], {
      'provider': 'fake',
      'model': 'heuristic',
    });
    state.dispose();
  });

  testWidgets(
    'embedded wizard renders inside a scroll view with Cancel and Back',
    (tester) async {
      tester.view.physicalSize = const Size(1200, 1400);
      tester.view.devicePixelRatio = 1;
      addTearDown(tester.view.reset);
      final api = FakeApi();
      final state = AppState(api);
      var done = 0;
      await tester.pumpWidget(
        _wrap(
          state,
          Scaffold(
            body: SingleChildScrollView(
              child: Column(
                children: [SetupWizard(embedded: true, onDone: () => done++)],
              ),
            ),
          ),
        ),
      );
      await tester.pump(const Duration(milliseconds: 100));
      expect(tester.takeException(), isNull);
      expect(find.text('Model & provider'), findsOneWidget);
      await tester.tap(find.byKey(const Key('provider-openai')));
      await tester.pumpAndSettle();
      expect(find.byKey(const Key('wizard-back')), findsOneWidget);
      await tester.tap(find.byKey(const Key('wizard-back')));
      await tester.pumpAndSettle();
      expect(find.byKey(const Key('provider-openai')), findsOneWidget);
      await tester.tap(find.byKey(const Key('wizard-cancel')));
      await tester.pump();
      expect(done, 1);
      state.dispose();
    },
  );

  test('settings parse: key status and hints', () {
    final s = ServerSettings.fromJson({
      'path': '/x/config.toml',
      'setup_needed': false,
      'timezone': 'Europe/Berlin',
      'token_set': true,
      'triage': {'provider': 'openai', 'model': 'gpt-5.4-mini'},
      'chat': {'provider': 'openai', 'model': 'gpt-5.5'},
      'autonomy': {
        'min_confidence': 0.7,
        'auto_create': false,
        'max_ops_per_capture': 8,
        'max_new_topics_per_capture': 2,
      },
      'providers': {
        'openai': {
          'type': 'openai',
          'base_url': 'u',
          'key_status': 'set',
          'key_hint': '…ab12',
          'local': false,
          'oauth': false,
        },
        'openrouter': {'type': 'openai', 'key_status': 'env', 'oauth': true},
        'ollama': {'type': 'openai', 'key_status': 'unset', 'local': true},
        'fake': {'type': 'fake'},
      },
    });
    expect(s.triage.label, 'openai/gpt-5.4-mini');
    expect(s.provider('openai')!.hasKey, isTrue);
    expect(s.provider('openai')!.keyHint, '…ab12');
    expect(s.provider('openrouter')!.keyStatus, 'env');
    expect(s.provider('openrouter')!.oauth, isTrue);
    expect(s.provider('ollama')!.needsKey, isFalse);
    expect(s.provider('fake')!.needsKey, isFalse);
    expect(s.autonomy.autoCreate, isFalse);
    expect(s.autonomy.minConfidence, 0.7);
    expect(s.autonomy.maxNewTopicsPerCapture, 2);
    expect(s.tokenSet, isTrue);
    final p = ProbeResult.fromJson({
      'reachable': true,
      'structured': true,
      'latency': 1200000000,
      'errors': [],
    });
    expect(p.latency.inMilliseconds, 1200);
    expect(p.usable, isTrue);
    final h = Health.fromJson({
      'ok': true,
      'setup_needed': true,
      'configured': {'triage': '', 'chat': ''},
    });
    expect(h.setupNeeded, isTrue);
    // The daemon currently sends PascalCase autonomy keys; both spellings parse.
    final a = Autonomy.fromJson({
      'MinConfidence': 0.5,
      'AutoCreate': false,
      'MaxOpsPerCapture': 9,
      'MaxNewTopicsPerCapture': 1,
    });
    expect(a.minConfidence, 0.5);
    expect(a.autoCreate, isFalse);
    expect(a.maxNewTopicsPerCapture, 1);
    final ml = ModelList.fromJson({
      'models': [],
      'error': 'nothing is listening',
    });
    expect(ml.ok, isFalse);
  });

  test('daemon path resolution: override, next to app, then PATH', () {
    final existing = {'/opt/fundus/fundus', '/usr/local/bin/fundus'};
    bool exists(String p) => existing.contains(p);
    expect(
      resolveDaemonPath(
        override: '/tmp/mine',
        exeDir: '/opt/app',
        pathDirs: ['/usr/local/bin'],
        exists: (p) => p == '/tmp/mine',
      ),
      '/tmp/mine',
    );
    expect(
      resolveDaemonPath(
        exeDir: '/opt/fundus',
        pathDirs: ['/usr/local/bin'],
        exists: exists,
      ),
      '/opt/fundus/fundus',
    );
    expect(
      resolveDaemonPath(
        exeDir: '/opt/app',
        pathDirs: ['/nope', '/usr/local/bin'],
        exists: exists,
      ),
      '/usr/local/bin/fundus',
    );
    expect(
      resolveDaemonPath(
        exeDir: '/opt/app',
        pathDirs: ['/nope'],
        exists: exists,
      ),
      isNull,
    );
    // macOS: the bundled daemon sits next to the executable in Contents/MacOS.
    expect(
      resolveDaemonPath(
        exeDir: '/Applications/Fundus.app/Contents/MacOS',
        pathDirs: ['/usr/local/bin'],
        exists: (p) => p == '/Applications/Fundus.app/Contents/MacOS/fundus',
      ),
      '/Applications/Fundus.app/Contents/MacOS/fundus',
    );
    // Windows: fundus.exe next to fundus_app.exe, .exe also tried on PATH.
    expect(
      resolveDaemonPath(
        exeDir: r'C:\Program Files\Fundus',
        pathDirs: const [],
        windows: true,
        exists: (p) => p == r'C:\Program Files\Fundus\fundus.exe',
      ),
      r'C:\Program Files\Fundus\fundus.exe',
    );
    expect(
      resolveDaemonPath(
        exeDir: r'C:\App',
        pathDirs: [r'C:\Tools'],
        windows: true,
        exists: (p) => p == r'C:\Tools\fundus.exe',
      ),
      r'C:\Tools\fundus.exe',
    );
    // Relative PATH entries are ignored.
    expect(
      resolveDaemonPath(
        exeDir: '/opt/app',
        pathDirs: ['.', 'bin'],
        exists: (p) => p == './fundus' || p == 'bin/fundus',
      ),
      isNull,
    );
    // Log file locations per platform.
    expect(
      daemonLogPath(env: {'XDG_STATE_HOME': '/s'}, home: '/h'),
      '/s/fundus/fundus.log',
    );
    expect(
      daemonLogPath(env: {}, home: '/h'),
      '/h/.local/state/fundus/fundus.log',
    );
    expect(
      daemonLogPath(env: {}, home: '/h', macos: true),
      '/h/Library/Logs/fundus.log',
    );
    expect(
      daemonLogPath(
        env: {'LOCALAPPDATA': r'C:\U\L'},
        home: r'C:\U',
        windows: true,
      ),
      r'C:\U\L\fundus\fundus.log',
    );
  });

  test(
    'autostart: AppState starts the daemon when unreachable and recovers',
    () async {
      final api = FakeApi()..healthFails = true;
      var started = 0;
      final state = AppState(
        api,
        daemonStarter: () async {
          started++;
          api.healthFails = false;
          return '/usr/local/bin/fundus';
        },
      );
      await Future<void>.delayed(const Duration(milliseconds: 900));
      expect(started, 1);
      expect(state.reachable, isTrue);
      expect(state.starting, isFalse);
      state.dispose();
    },
  );
}
