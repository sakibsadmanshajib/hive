import { cookies } from "next/headers";
import { getRequestConfig } from "next-intl/server";

import { LOCALE_COOKIE, resolveLocale, type AppLocale } from "@/lib/i18n/locales";
import en from "../messages/en.json";
import bn from "../messages/bn.json";

// Statically imported rather than `await import(\`../messages/${locale}.json\`)`:
// two message files are a couple of KB, and a static map removes both the
// webpack context module and any chance of an unvalidated path reaching the
// bundler on Cloudflare Workers.
const MESSAGES: Record<AppLocale, typeof en> = { en, bn };

export default getRequestConfig(async () => {
  const store = await cookies();
  const locale = resolveLocale(store.get(LOCALE_COOKIE)?.value);

  return { locale, messages: MESSAGES[locale] };
});
