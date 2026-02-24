import { Button } from "../../components/ui/button";
import { cn } from "../../lib/utils";

type GoogleLoginButtonProps = {
  apiBase?: string;
  className?: string;
};

const defaultApiBase = process.env.NEXT_PUBLIC_API_BASE_URL ?? "http://127.0.0.1:8080";

export function GoogleLoginButton({ apiBase = defaultApiBase, className }: GoogleLoginButtonProps) {
  return (
    <Button asChild className={cn("w-full", className)} type="button" variant="outline">
      <a href={`${apiBase}/v1/auth/google/start`}>Continue with Google</a>
    </Button>
  );
}
