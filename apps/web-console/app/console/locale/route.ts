import { NextResponse, type NextRequest } from "next/server";

import { resolveCanonicalOrigin } from "@/lib/http/origin";
import { resolveReturnTo } from "@/lib/http/return-to";
import { LOCALE_COOKIE, resolveLocale } from "@/lib/i18n/locales";

const ONE_YEAR_SECONDS = 60 * 60 * 24 * 365;

// A form POST plus a 303 rather than a Server Action: a Server Action sets the
// cookie correctly but the re-render it returns still reads the *incoming*
// request cookies, so the first paint after switching stays in the old
// language (including `<html lang>`). Redirecting makes the browser re-request
// with the new cookie, which is also how /console/account-switch works.
export async function POST(request: NextRequest) {
  const formData = await request.formData();

  const submitted = formData.get(LOCALE_COOKIE);
  const locale = resolveLocale(
    typeof submitted === "string" ? submitted : undefined,
  );

  const origin = resolveCanonicalOrigin(request);
  const response = NextResponse.redirect(
    new URL(resolveReturnTo(formData.get("return_to"), origin), origin),
    { status: 303 },
  );

  response.cookies.set(LOCALE_COOKIE, locale, {
    httpOnly: true,
    sameSite: "lax",
    path: "/",
    maxAge: ONE_YEAR_SECONDS,
  });

  return response;
}
