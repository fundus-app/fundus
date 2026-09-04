// The settings surface: rail and deep links, Connection, Providers, Models,
// Filing, About.
import 'package:flutter/material.dart';
import 'package:flutter_test/flutter_test.dart';
import 'package:fundus_app/state/app_state.dart';
import 'package:fundus_app/state/settings.dart';
import 'package:fundus_app/ui/settings/about.dart';
import 'package:fundus_app/ui/settings/connection.dart';
import 'package:fundus_app/ui/settings/filing.dart';
import 'package:fundus_app/ui/settings/models.dart';
import 'package:fundus_app/ui/settings/providers.dart';
import 'package:fundus_app/ui/settings_screen.dart';
import 'package:fundus_app/ui/theme.dart';
import 'package:fundus_app/ui/widgets/toasts.dart';
import 'package:provider/provider.dart';
import 'package:shared_preferences/shared_preferences.dart';

import 'fake_api.dart';

Widget _app(AppState state, Widget child, {Settings? settings}) =>
    MultiProvider(
      providers: [
        ChangeNotifierProvider<Settings>.value(
          value:
              settings ?? Settings.memory(serverUrl: 'http://127.0.0.1:7433'),
        ),
        ChangeNotifierProvider<AppState>.value(value: state),
      ],
      child: MaterialApp(
        theme: FundusTheme.light(),
        builder: toastBuilder,
        home: Scaffold(body: child),
      ),
    );

void _wide(WidgetTester tester) {
  tester.view.physicalSize = const Size(1400, 1000);
  tester.view.devicePixelRatio = 1;
  addTearDown(tester.view.reset);
}

FakeApi _configured() {
  final api = FakeApi()..setupNeeded = false;
  api.serverSettings['setup_needed'] = false;
  api.serverSettings['triage'] = <String, dynamic>{
    'provider': 'openai',
    'model': 'gpt-5.6-luna',
  };
  api.serverSettings['chat'] = <String, dynamic>{
    'provider': 'ollama',
    'model': 'qwen3:8b',
  };
  final provs = api.serverSettings['providers'] as Map<String, dynamic>;
  provs['openai'] = <String, dynamic>{
    'type': 'openai',
    'key_status': 'set',
    'key_hint': '…aRyP',
    'transcription': 'audio',
  };
  provs['ollama'] = <String, dynamic>{
    'type': 'openai',
    'local': true,
    'base_url': 'http://127.0.0.1:11434/v1',
    'transcription': 'none',
  };
  provs['openrouter'] = <String, dynamic>{
    'type': 'openai',
    'oauth': true,
    'key_status': 'env',
    'transcription': 'none',
  };
  return api;
}

void main() {
  testWidgets('rail switches sections; deep links pick the section by name', (
    tester,
  ) async {
    final api = _configured();
    final state = AppState(api);
    _wide(tester);
    await tester.pumpWidget(
      _app(
        state,
        Builder(
          builder: (context) => TextButton(
            onPressed: () =>
                SettingsScreen.show(context, section: SettingsSection.research),
            child: const Text('open'),
          ),
        ),
      ),
    );
    await tester.tap(find.text('open'));
    await tester.pumpAndSettle();
    expect(find.text('Settings'), findsOneWidget);
    expect(
      find.text('Start research automatically when you ask for it'),
      findsOneWidget,
    );
    for (final s in SettingsSection.values) {
      expect(find.byKey(Key('settings-nav-${s.name}')), findsOneWidget);
    }
    await tester.tap(find.byKey(const Key('settings-nav-about')));
    await tester.pumpAndSettle();
    expect(find.text('Semantic search'), findsOneWidget);
    expect(
      SettingsSection.fromName('settings/providers'),
      SettingsSection.providers,
    );
    expect(SettingsSection.fromName('filing'), SettingsSection.filing);
    expect(SettingsSection.fromName('nope'), isNull);
    state.dispose();
  });

  testWidgets(
    'connection: one status row; Change… reveals the fields and reconnects',
    (tester) async {
      final api = _configured();
      final state = AppState(api);
      SharedPreferences.setMockInitialValues({
        'server_url': 'http://127.0.0.1:7433',
      });
      final settings = await Settings.load();
      _wide(tester);
      await tester.pumpWidget(
        _app(state, const ConnectionSection(), settings: settings),
      );
      await tester.pump();
      expect(find.textContaining('127.0.0.1:7433'), findsOneWidget);
      expect(find.byKey(const Key('connection-url')), findsNothing);
      await tester.tap(find.byKey(const Key('connection-change')));
      await tester.pump();
      expect(find.byKey(const Key('connection-url')), findsOneWidget);
      expect(find.byKey(const Key('connection-save')), findsOneWidget);
      await tester.enterText(
        find.byKey(const Key('connection-url')),
        'http://10.0.0.2:7433',
      );
      await tester.tap(find.byKey(const Key('connection-save')));
      await tester.pump();
      expect(settings.serverUrl, 'http://10.0.0.2:7433');
      expect(
        find.byKey(const Key('connection-url')),
        findsNothing,
        reason: 'collapses after saving',
      );
      state.dispose();
    },
  );

  testWidgets(
    'models: one row per role with value and Change; dictation unavailable with Ollama',
    (tester) async {
      final api = _configured();
      api.serverSettings['dictation'] = <String, dynamic>{
        'provider': 'ollama',
        'model': '',
      };
      final state = AppState(api);
      _wide(tester);
      await tester.pumpWidget(_app(state, const ModelsSection()));
      await tester.pump();
      await tester.pump();
      for (final r in [
        'triage',
        'chat',
        'dictation',
        'research',
        'embedding',
      ]) {
        expect(find.byKey(Key('role-$r')), findsOneWidget);
        expect(find.byKey(Key('change-$r')), findsOneWidget);
      }
      expect(find.text('gpt-5.6-luna · OpenAI'), findsOneWidget);
      expect(find.text('qwen3:8b · Ollama'), findsOneWidget);
      expect(find.text('Not available with Ollama'), findsOneWidget);
      expect(find.byKey(const Key('models-test')), findsOneWidget);
      await tester.tap(find.byKey(const Key('change-triage')));
      await tester.pumpAndSettle();
      expect(find.text('Filing model'), findsOneWidget);
      await tester.enterText(
        find.descendant(
          of: find.byKey(const Key('picker-model')),
          matching: find.byType(TextField),
        ),
        'gpt-5.6-terra',
      );
      await tester.tap(find.byKey(const Key('picker-save')));
      await tester.pumpAndSettle();
      expect(api.settingsPatches.last, {
        'triage': {'provider': 'openai', 'model': 'gpt-5.6-terra'},
      });
      await tester.tap(find.byKey(const Key('models-test')));
      await tester.pump();
      expect(api.tested, isNotEmpty);
      state.dispose();
    },
  );

  testWidgets(
    'providers: status per provider; Change expands the key row; Replace and Remove patch',
    (tester) async {
      final api = _configured();
      final state = AppState(api);
      _wide(tester);
      await tester.pumpWidget(_app(state, const ProvidersSection()));
      await tester.pump();
      await tester.pump();
      expect(find.text('Key stored · ends with …aRyP'), findsOneWidget);
      expect(find.text('From environment'), findsOneWidget);
      expect(find.text('Running at 127.0.0.1:11434'), findsOneWidget);
      expect(
        find.text('No key'),
        findsNWidgets(2),
        reason: 'Gemini and Anthropic',
      );
      expect(find.byKey(const Key('provider-key-openai')), findsNothing);
      await tester.tap(find.byKey(const Key('provider-change-openai')));
      await tester.pump();
      await tester.enterText(
        find.byKey(const Key('provider-key-openai')),
        'sk-new-key-1234',
      );
      await tester.pump();
      await tester.tap(find.byKey(const Key('provider-replace-openai')));
      await tester.pump();
      expect(api.settingsPatches.last, {
        'providers': {
          'openai': {'api_key': 'sk-new-key-1234'},
        },
      });
      await tester.tap(find.byKey(const Key('provider-remove-openai')));
      await tester.pump();
      expect(api.settingsPatches.last, {
        'providers': {
          'openai': {'api_key': ''},
        },
      });
      await tester.tap(find.byKey(const Key('provider-change-ollama')));
      await tester.pump();
      expect(find.byKey(const Key('ollama-url')), findsOneWidget);
      expect(
        find.byKey(const Key('provider-oauth-openrouter')),
        findsNothing,
        reason: 'collapsed rows show nothing',
      );
      state.dispose();
    },
  );

  testWidgets(
    'filing: switch saves autonomy; about shows semantic search state',
    (tester) async {
      final api = _configured();
      final state = AppState(api);
      _wide(tester);
      await tester.pumpWidget(_app(state, const FilingSection()));
      await tester.pump();
      await tester.pump();
      await tester.tap(find.byKey(const Key('filing-auto')));
      await tester.pump();
      expect(api.settingsPatches.last.keys, ['autonomy']);
      await tester.pumpWidget(_app(state, const AboutSection()));
      await state.checkHealth();
      await tester.pump();
      await tester.pump();
      expect(find.text('Semantic search'), findsOneWidget);
      expect(find.text('off'), findsOneWidget);
      expect(find.text('fundus-app.de', findRichText: true), findsOneWidget);
      state.dispose();
    },
  );
}
