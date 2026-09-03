import 'dart:async';
import 'dart:io';

import 'package:flutter/material.dart';
import 'package:flutter/services.dart';
import 'package:window_manager/window_manager.dart';

import '../api/client.dart';
import '../api/models.dart';
import '../state/dictation.dart';
import 'widgets/mic_button.dart';
import 'widgets/toasts.dart';
import 'theme.dart';
import 'widgets/common.dart';

/// The `--quick-capture` window: one field, Enter files it, Esc closes.
class QuickCaptureApp extends StatelessWidget {
  const QuickCaptureApp({
    super.key,
    required this.api,
    required this.themeMode,
  });
  final FundusApi api;
  final ThemeMode themeMode;

  @override
  Widget build(BuildContext context) => MaterialApp(
    title: 'Fundus capture',
    debugShowCheckedModeBanner: false,
    theme: FundusTheme.light(),
    darkTheme: FundusTheme.dark(),
    themeMode: themeMode,
    builder: toastBuilder,
    home: QuickCapture(api: api),
  );
}

class QuickCapture extends StatefulWidget {
  const QuickCapture({super.key, required this.api});
  final FundusApi api;
  @override
  State<QuickCapture> createState() => _QuickCaptureState();
}

class _QuickCaptureState extends State<QuickCapture> {
  final _ctrl = TextEditingController();
  final _focus = FocusNode();
  Capture? _capture;
  Object? _error;
  bool _busy = false;
  late final DictationController _dictation = DictationController(widget.api);
  bool _dictationAvailable = false;

  @override
  void initState() {
    super.initState();
    widget.api
        .health()
        .then((h) {
          if (mounted) setState(() => _dictationAvailable = h.dictation);
        })
        .catchError((_) {});
  }

  @override
  void dispose() {
    _dictation.dispose();
    _ctrl.dispose();
    _focus.dispose();
    super.dispose();
  }

  void _insert(String text) {
    final existing = _ctrl.text.trimRight();
    _ctrl.text = existing.isEmpty ? text : '$existing $text';
    _ctrl.selection = TextSelection.collapsed(offset: _ctrl.text.length);
    _focus.requestFocus();
    setState(() {});
  }

  Future<void> _escape() async {
    if (_dictation.isRecording) {
      final t = await _dictation.stop();
      if (mounted && t.isNotEmpty) _insert(t);
      return;
    }
    await _close();
  }

  Future<void> _close() async {
    try {
      await windowManager.close();
    } catch (_) {}
    exit(0);
  }

  Future<void> _submit() async {
    final t = _ctrl.text.trim();
    if (t.isEmpty || _busy) return;
    setState(() {
      _busy = true;
      _error = null;
    });
    try {
      // Fundus waits briefly for filing so the receipt can be shown when
      // it is quick; the window never stays open longer than ~2 s.
      final c = await widget.api.capture(t, source: 'desktop', waitMs: 1400);
      if (mounted) setState(() => _capture = c);
      await Future<void>.delayed(const Duration(milliseconds: 600));
      await _close();
    } catch (e) {
      setState(() {
        _error = e;
        _busy = false;
      });
    }
  }

  @override
  Widget build(BuildContext context) {
    final theme = Theme.of(context);
    final scheme = theme.colorScheme;
    final c = _capture;
    return CallbackShortcuts(
      bindings: {
        const SingleActivator(LogicalKeyboardKey.escape): _escape,
        const SingleActivator(LogicalKeyboardKey.enter): _submit,
        const SingleActivator(LogicalKeyboardKey.numpadEnter): _submit,
      },
      child: Scaffold(
        body: Padding(
          padding: const EdgeInsets.all(16),
          child: Column(
            crossAxisAlignment: CrossAxisAlignment.stretch,
            children: [
              Row(
                children: [
                  Icon(
                    Icons.add_circle_outline_rounded,
                    size: 16,
                    color: scheme.primary,
                  ),
                  const SizedBox(width: 6),
                  Text('Fundus', style: theme.textTheme.titleSmall),
                  const Spacer(),
                  if (_dictationAvailable)
                    MicButton(
                      controller: _dictation,
                      onText: _insert,
                      compact: true,
                    ),
                  const KeyHint('↵'),
                  const SizedBox(width: 6),
                  const KeyHint('Esc'),
                ],
              ),
              const SizedBox(height: 10),
              Expanded(
                child: TextField(
                  controller: _ctrl,
                  focusNode: _focus,
                  autofocus: true,
                  enabled: !_busy,
                  maxLines: null,
                  expands: true,
                  textAlignVertical: TextAlignVertical.top,
                  style: theme.textTheme.bodyLarge,
                  decoration: const InputDecoration(
                    hintText: 'Capture a thought…',
                  ),
                ),
              ),
              const SizedBox(height: 8),
              SizedBox(
                height: 22,
                child: Row(
                  children: [
                    if (c != null) ...[
                      StatusDot(c.status),
                      const SizedBox(width: 8),
                      Expanded(
                        child: Text(
                          c.isBusy
                              ? 'Filing…'
                              : (c.filingReceipt?.summary ??
                                    c.result?.question ??
                                    c.status),
                          style: theme.textTheme.bodySmall,
                          maxLines: 1,
                          overflow: TextOverflow.ellipsis,
                        ),
                      ),
                    ] else if (_error != null)
                      Expanded(
                        child: Text(
                          describeError(_error!),
                          style: theme.textTheme.bodySmall!.copyWith(
                            color: scheme.error,
                          ),
                          maxLines: 1,
                          overflow: TextOverflow.ellipsis,
                        ),
                      )
                    else
                      Text(
                        'Enter files it and closes. Shift+Enter for a new line.',
                        style: theme.textTheme.labelSmall,
                      ),
                  ],
                ),
              ),
            ],
          ),
        ),
      ),
    );
  }
}
