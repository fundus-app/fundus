import 'package:flutter/material.dart';

import '../../state/dictation.dart';
import '../theme.dart';
import 'common.dart';

/// Microphone button: idle mic → pulsing red dot with elapsed seconds while
/// recording → small spinner while transcribing. The transcript goes to
/// [onText]; nothing is captured automatically.
class MicButton extends StatefulWidget {
  const MicButton({
    super.key,
    required this.controller,
    required this.onText,
    this.compact = false,
  });
  final DictationController controller;
  final ValueChanged<String> onText;
  final bool compact;

  @override
  State<MicButton> createState() => _MicButtonState();
}

class _MicButtonState extends State<MicButton>
    with SingleTickerProviderStateMixin {
  late final AnimationController _pulse = AnimationController(
    vsync: this,
    duration: const Duration(milliseconds: 900),
  )..repeat(reverse: true);

  @override
  void dispose() {
    _pulse.dispose();
    super.dispose();
  }

  Future<void> _tap() async {
    final c = widget.controller;
    if (c.status == DictationStatus.transcribing) return;
    final text = await c.toggle();
    if (!mounted) return;
    if (c.lastError != null) {
      showError(context, c.lastError!);
    } else if (text.isNotEmpty) {
      widget.onText(text);
    }
  }

  @override
  Widget build(BuildContext context) {
    final scheme = Theme.of(context).colorScheme;
    return ListenableBuilder(
      listenable: widget.controller,
      builder: (context, _) {
        final c = widget.controller;
        switch (c.status) {
          case DictationStatus.recording:
            final secs = c.elapsed.inSeconds;
            return Tooltip(
              message: 'Stop recording (Esc)',
              child: InkWell(
                key: const Key('mic-stop'),
                onTap: _tap,
                borderRadius: BorderRadius.circular(16),
                child: Padding(
                  padding: const EdgeInsets.symmetric(
                    horizontal: 8,
                    vertical: 6,
                  ),
                  child: Row(
                    mainAxisSize: MainAxisSize.min,
                    children: [
                      FadeTransition(
                        opacity: Tween(begin: 0.35, end: 1.0).animate(_pulse),
                        child: Container(
                          width: 10,
                          height: 10,
                          decoration: BoxDecoration(
                            shape: BoxShape.circle,
                            color: scheme.error,
                          ),
                        ),
                      ),
                      const SizedBox(width: 6),
                      Text(
                        '${secs ~/ 60}:${(secs % 60).toString().padLeft(2, '0')}',
                        style: monoStyle(
                          context,
                          size: 12,
                          color: scheme.onSurface,
                        ),
                      ),
                    ],
                  ),
                ),
              ),
            );
          case DictationStatus.transcribing:
            return Padding(
              padding: const EdgeInsets.symmetric(horizontal: 8),
              child: Row(
                mainAxisSize: MainAxisSize.min,
                children: [
                  SizedBox(
                    width: 12,
                    height: 12,
                    child: CircularProgressIndicator(
                      strokeWidth: 1.5,
                      color: scheme.primary,
                    ),
                  ),
                  if (!widget.compact) ...[
                    const SizedBox(width: 6),
                    Text('Transcribing…', style: secondaryStyle(context)),
                  ],
                ],
              ),
            );
          case DictationStatus.idle:
            return IconButton(
              key: const Key('mic-start'),
              tooltip: 'Dictate (Ctrl Shift K)',
              icon: const Icon(Icons.mic_none_rounded, size: 18),
              onPressed: _tap,
            );
        }
      },
    );
  }
}
