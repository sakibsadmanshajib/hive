"use client";

import { FormEvent, useState } from "react";
import { toast } from "sonner";

import { Button } from "../../../components/ui/button";
import { Card, CardContent, CardDescription, CardHeader, CardTitle } from "../../../components/ui/card";
import { Input } from "../../../components/ui/input";
import { GoogleLoginButton } from "../google-login-button";
import { writeAuthSession } from "../auth-session";
import { createSupabaseBrowserClient } from "../../../lib/supabase-client";

type AuthExperienceProps = {
  variant?: "page" | "modal";
  onAuthenticated?: () => void;
  onDismiss?: () => void;
};

export function AuthExperience({ variant = "page", onAuthenticated, onDismiss }: AuthExperienceProps) {
  const [name, setName] = useState("");
  const [email, setEmail] = useState("");
  const [password, setPassword] = useState("");
  const [status, setStatus] = useState("Sign in to continue.");
  const [loading, setLoading] = useState(false);

  async function completeAuthentication(input: {
    accessToken: string;
    email: string;
    name?: string;
    successMessage: string;
  }) {
    writeAuthSession({
      accessToken: input.accessToken,
      email: input.email,
      name: input.name,
    });
    setStatus(`Welcome ${input.email}`);
    toast.success(input.successMessage);

    if (onAuthenticated) {
      onAuthenticated();
      return;
    }

    window.location.assign("/");
  }

  async function register(event: FormEvent<HTMLFormElement>) {
    event.preventDefault();
    setLoading(true);
    try {
      const supabase = createSupabaseBrowserClient();
      const { data, error } = await supabase.auth.signUp({
        email,
        password,
        options: { data: { name } },
      });

      if (error) {
        setStatus(error.message);
        toast.error(error.message);
        return;
      }

      if (!data.session) {
        setStatus("Check your email to confirm your account.");
        toast.info("Check your email to confirm your account.");
        return;
      }

      await completeAuthentication({
        accessToken: data.session.access_token,
        email: data.user?.email ?? email,
        name: data.user?.user_metadata?.name ?? name,
        successMessage: "Account created successfully",
      });
    } catch (error) {
      const nextStatus = error instanceof Error ? error.message : "Registration failed";
      setStatus(nextStatus);
      toast.error(nextStatus);
    } finally {
      setLoading(false);
    }
  }

  async function login(event: FormEvent<HTMLFormElement>) {
    event.preventDefault();
    setLoading(true);
    try {
      const supabase = createSupabaseBrowserClient();
      const { data, error } = await supabase.auth.signInWithPassword({
        email,
        password,
      });

      if (error) {
        setStatus(error.message);
        toast.error(error.message);
        return;
      }

      await completeAuthentication({
        accessToken: data.session.access_token,
        email: data.user.email ?? email,
        name: typeof data.user.user_metadata?.name === "string" ? data.user.user_metadata.name : undefined,
        successMessage: "Authentication successful",
      });
    } catch (error) {
      const nextStatus = error instanceof Error ? error.message : "Authentication failed";
      setStatus(nextStatus);
      toast.error(nextStatus);
    } finally {
      setLoading(false);
    }
  }

  const introTitle = variant === "modal" ? "Unlock paid models" : "Welcome back";
  const introDescription = variant === "modal"
    ? "Sign in or create an account to use credit-backed models. You can dismiss this and keep chatting with free models."
    : "Authenticate first, then continue directly into the chat workspace.";

  const forms = (
    <div className="grid gap-4">
      {variant === "modal" ? (
        <div className="rounded-2xl border border-slate-200 bg-slate-50 px-4 py-3">
          <p id="auth-modal-title" className="text-lg font-semibold text-slate-950">{introTitle}</p>
          <p className="mt-1 text-sm text-slate-600">{introDescription}</p>
          <p className="mt-3 text-sm text-slate-500">{status}</p>
        </div>
      ) : null}

      <Card className="border-slate-800 bg-slate-900 text-slate-100">
        <CardHeader>
          <CardTitle>Login</CardTitle>
        </CardHeader>
        <CardContent>
          <form className="space-y-3" onSubmit={login}>
            <Input placeholder="Email" value={email} onChange={(event) => setEmail(event.target.value)} />
            <Input
              placeholder="Password"
              type="password"
              value={password}
              onChange={(event) => setPassword(event.target.value)}
            />
            <Button className="w-full" disabled={loading} type="submit">
              Login
            </Button>
            <GoogleLoginButton className="w-full" />
          </form>
        </CardContent>
      </Card>

      <Card className="border-slate-800 bg-slate-900 text-slate-100">
        <CardHeader>
          <CardTitle>Register</CardTitle>
        </CardHeader>
        <CardContent>
          <form className="space-y-3" onSubmit={register}>
            <Input placeholder="Name" value={name} onChange={(event) => setName(event.target.value)} />
            <Input placeholder="Email" value={email} onChange={(event) => setEmail(event.target.value)} />
            <Input
              placeholder="Password"
              type="password"
              value={password}
              onChange={(event) => setPassword(event.target.value)}
            />
            <Button className="w-full" disabled={loading} type="submit" variant="secondary">
              Create account
            </Button>
          </form>
        </CardContent>
      </Card>

      {variant === "modal" && onDismiss ? (
        <Button type="button" variant="outline" onClick={onDismiss}>
          Continue with free models
        </Button>
      ) : null}
    </div>
  );

  if (variant === "modal") {
    return forms;
  }

  return (
    <section className="mx-auto grid w-full max-w-5xl gap-4 lg:grid-cols-[1.05fr_1fr]">
      <Card className="border-slate-800 bg-gradient-to-br from-slate-900 to-slate-800 shadow-md">
        <CardHeader>
          <CardTitle className="text-3xl leading-tight text-slate-100 md:text-4xl">{introTitle}</CardTitle>
          <CardDescription className="max-w-md text-sm text-slate-400">
            {introDescription}
          </CardDescription>
        </CardHeader>
        <CardContent>
          <p className="text-sm text-slate-400">{status}</p>
        </CardContent>
      </Card>

      {forms}
    </section>
  );
}
