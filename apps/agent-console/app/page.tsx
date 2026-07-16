import { redirect } from "next/navigation";

// Middleware already redirects "/" based on session; this handles the
// direct-render case (e.g. middleware disabled in a test harness).
export default function RootPage() {
  redirect("/tasks");
}
