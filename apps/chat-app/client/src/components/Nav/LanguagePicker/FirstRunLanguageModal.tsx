/* eslint-disable i18next/no-literal-string --
 * Intentional: bilingual native labels for the first-run picker. User cannot
 * read i18n-routed strings until *after* this picker resolves their locale.
 */
import * as Dialog from '@radix-ui/react-dialog';
import { useFirstRunLocale, type SupportedLocale } from '~/hooks/useFirstRunLocale';

/**
 * Hive Phase 19 — first-run language picker.
 *
 * Renders a non-dismissible modal on first visit (no `localStorage.hive_locale_v1`).
 * Choice persists locally; Phase 20 will forward it to Supabase user prefs.
 *
 * Bilingual-native button labels are intentionally NOT i18n-routed — the user
 * cannot read the i18n strings until *after* this picker resolves.
 */
export function FirstRunLanguageModal() {
  const { setLocale, isFirstRun } = useFirstRunLocale();

  if (!isFirstRun) {
    return null;
  }

  const choose = (next: SupportedLocale) => () => setLocale(next);

  // Block backdrop dismiss + ESC dismiss — user MUST pick.
  const stopDismiss = (e: Event) => e.preventDefault();

  return (
    <Dialog.Root open>
      <Dialog.Portal>
        <Dialog.Overlay
          className="fixed inset-0 z-[1100] bg-black/60 backdrop-blur-sm"
          aria-hidden
        />
        <Dialog.Content
          className="fixed left-1/2 top-1/2 z-[1101] w-[min(92vw,440px)] -translate-x-1/2 -translate-y-1/2 rounded-2xl bg-white p-8 shadow-2xl dark:bg-gray-800"
          onEscapeKeyDown={stopDismiss}
          onPointerDownOutside={stopDismiss}
          onInteractOutside={stopDismiss}
          aria-labelledby="hive-locale-title"
          aria-describedby="hive-locale-desc"
        >
          <Dialog.Title
            id="hive-locale-title"
            className="text-center text-xl font-semibold text-gray-900 dark:text-white"
          >
            Choose your language · ভাষা নির্বাচন করুন
          </Dialog.Title>
          <Dialog.Description
            id="hive-locale-desc"
            className="mt-2 text-center text-sm text-gray-500 dark:text-gray-300"
          >
            You can change this later.
          </Dialog.Description>

          <div className="mt-6 flex flex-col gap-3">
            <button
              type="button"
              onClick={choose('bn-BD')}
              className="w-full rounded-xl border border-gray-200 px-4 py-3 text-lg font-medium text-gray-900 transition hover:border-blue-500 hover:bg-blue-50 dark:border-gray-600 dark:text-white dark:hover:bg-gray-700"
              data-testid="hive-locale-bn-bd"
            >
              বাংলা
            </button>
            <button
              type="button"
              onClick={choose('en-US')}
              className="w-full rounded-xl border border-gray-200 px-4 py-3 text-lg font-medium text-gray-900 transition hover:border-blue-500 hover:bg-blue-50 dark:border-gray-600 dark:text-white dark:hover:bg-gray-700"
              data-testid="hive-locale-en-us"
            >
              English
            </button>
          </div>
        </Dialog.Content>
      </Dialog.Portal>
    </Dialog.Root>
  );
}

export default FirstRunLanguageModal;
