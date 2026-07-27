import { NextResponse, type NextRequest } from "next/server";

import { resolveCanonicalOrigin } from "@/lib/http/origin";
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

  const response = NextResponse.redirect(
    new URL(resolveReturnTo(formData.get("return_to")), resolveCanonicalOrigin(request)),
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

/**
 * Only relative console paths are honoured, so a crafted `return_to` cannot
 * turn the language switcher into an open redirect. Rejecting anything with
 * "//" also covers protocol-relative targets.
 */
function resolveReturnTo(value: FormDataEntryValue | null): string {
  if (typeof value !== "string") return "/console";
  if (!value.startsWith("/console")) return "/console";
  if (value.includes("//") || value.includes("\\")) return "/console";
  return value;
}
