// Building blocks of the settings surface: a page with a serif title and
// rows separated by hairlines. A row is a 15 px label with an optional
// muted 13 px explanation on the left and the control on the right.
import 'package:flutter/material.dart';
import 'package:flutter/services.dart';

import '../theme.dart';
import '../widgets/toasts.dart';

/// "Saved." toast used by every setting that saves on change.
void showSaved(BuildContext context) => showToast(
  context,
  'Saved.',
  key: 'saved',
  duration: ToastController.settledDuration,
);

/// A section page: title, then rows with hairlines between them.
class SettingsPage extends StatelessWidget {
  const SettingsPage({
    super.key,
    required this.title,
    required this.children,
    this.footer,
  });
  final String title;
  final List<Widget> children;

  /// Sits under the rows without a separator (a single text button, say).
  final Widget? footer;

  @override
  Widget build(BuildContext context) {
    final theme = Theme.of(context);
    final scheme = theme.colorScheme;
    return SingleChildScrollView(
      padding: const EdgeInsets.fromLTRB(28, 22, 28, 28),
      child: Column(
        crossAxisAlignment: CrossAxisAlignment.stretch,
        children: [
          Text(
            title,
            style: theme.textTheme.headlineMedium!.copyWith(
              fontSize: 22,
              height: 1.2,
            ),
          ),
          const SizedBox(height: 10),
          for (var i = 0; i < children.length; i++) ...[
            if (i > 0)
              Divider(height: 1, thickness: 1, color: scheme.outlineVariant),
            children[i],
          ],
          if (footer != null) ...[const SizedBox(height: 12), footer!],
        ],
      ),
    );
  }
}

/// One settings row. [trailing] is the control; [child] is an inline
/// expansion (a key field, a picker) shown under the row when not null.
class SettingsRow extends StatelessWidget {
  const SettingsRow({
    super.key,
    required this.label,
    this.hint,
    this.trailing,
    this.child,
    this.leading,
  });
  final String label;
  final String? hint;
  final Widget? trailing;
  final Widget? child;
  final Widget? leading;

  @override
  Widget build(BuildContext context) {
    final theme = Theme.of(context);
    final scheme = theme.colorScheme;
    return Padding(
      padding: const EdgeInsets.symmetric(vertical: 8),
      child: Column(
        crossAxisAlignment: CrossAxisAlignment.stretch,
        children: [
          ConstrainedBox(
            constraints: const BoxConstraints(minHeight: 32),
            child: Row(
              children: [
                if (leading != null) ...[leading!, const SizedBox(width: 10)],
                Expanded(
                  child: Column(
                    mainAxisSize: MainAxisSize.min,
                    crossAxisAlignment: CrossAxisAlignment.start,
                    children: [
                      Text(
                        label,
                        style: theme.textTheme.bodyLarge!.copyWith(
                          fontSize: 15,
                          height: 1.3,
                        ),
                      ),
                      if (hint != null && hint!.isNotEmpty)
                        Padding(
                          padding: const EdgeInsets.only(top: 2),
                          child: Text(
                            hint!,
                            style: theme.textTheme.bodySmall!.copyWith(
                              fontSize: 13,
                              color: scheme.onSurfaceVariant,
                            ),
                          ),
                        ),
                    ],
                  ),
                ),
                if (trailing != null) ...[const SizedBox(width: 16), trailing!],
              ],
            ),
          ),
          if (child != null)
            Padding(
              padding: const EdgeInsets.only(top: 10, bottom: 4),
              child: child,
            ),
        ],
      ),
    );
  }
}

/// A row's value on the right: regular text, muted when [muted].
class RowValue extends StatelessWidget {
  const RowValue(this.text, {super.key, this.muted = false});
  final String text;
  final bool muted;
  @override
  Widget build(BuildContext context) {
    final theme = Theme.of(context);
    return ConstrainedBox(
      constraints: const BoxConstraints(maxWidth: 320),
      child: Text(
        text,
        style: theme.textTheme.bodyMedium!.copyWith(
          color: muted ? theme.colorScheme.onSurfaceVariant : null,
        ),
        textAlign: TextAlign.right,
        maxLines: 2,
        overflow: TextOverflow.ellipsis,
      ),
    );
  }
}

/// "Change" and friends: the row's text action.
class RowButton extends StatelessWidget {
  const RowButton(this.label, {super.key, required this.onPressed});
  final String label;
  final VoidCallback? onPressed;
  @override
  Widget build(BuildContext context) => TextButton(
    style: TextButton.styleFrom(visualDensity: VisualDensity.compact),
    onPressed: onPressed,
    child: Text(label),
  );
}

/// Value + Change side by side.
class ValueWithChange extends StatelessWidget {
  const ValueWithChange({
    super.key,
    required this.value,
    required this.onChange,
    this.muted = false,
    this.label = 'Change',
    this.buttonKey,
  });
  final String value;
  final VoidCallback? onChange;
  final bool muted;
  final String label;
  final Key? buttonKey;
  @override
  Widget build(BuildContext context) => Row(
    mainAxisSize: MainAxisSize.min,
    children: [
      Flexible(child: RowValue(value, muted: muted)),
      const SizedBox(width: 4),
      RowButton(label, key: buttonKey, onPressed: onChange),
    ],
  );
}

/// The one outlined field style used across settings.
InputDecoration fieldDecoration(
  BuildContext context, {
  String? label,
  String? hint,
  String? helper,
  Widget? suffix,
}) => InputDecoration(
  labelText: label,
  hintText: hint,
  helperText: helper,
  suffixIcon: suffix,
  isDense: true,
  border: const OutlineInputBorder(),
);

/// Path or command a user may copy: regular font, ellipsised, copy icon.
class CopyableLine extends StatelessWidget {
  const CopyableLine(this.text, {super.key, this.mono = true});
  final String text;
  final bool mono;
  @override
  Widget build(BuildContext context) {
    final theme = Theme.of(context);
    final scheme = theme.colorScheme;
    return Row(
      mainAxisSize: MainAxisSize.min,
      children: [
        Flexible(
          child: ConstrainedBox(
            constraints: const BoxConstraints(maxWidth: 360),
            child: Text(
              text,
              style: mono
                  ? monoStyle(context, size: 12.5, color: scheme.onSurface)
                  : theme.textTheme.bodyMedium,
              maxLines: 1,
              overflow: TextOverflow.ellipsis,
              textAlign: TextAlign.right,
            ),
          ),
        ),
        IconButton(
          tooltip: 'Copy',
          visualDensity: VisualDensity.compact,
          iconSize: 16,
          icon: const Icon(Icons.copy_rounded),
          onPressed: () => copyText(context, text),
        ),
      ],
    );
  }
}

Future<void> copyText(BuildContext context, String text) async {
  await Clipboard.setData(ClipboardData(text: text));
  if (context.mounted) {
    showToast(
      context,
      'Copied',
      key: 'copy',
      duration: ToastController.settledDuration,
    );
  }
}
