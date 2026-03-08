"use client";

import { FormEvent, useState } from "react";
import { toast } from "sonner";

import { Button } from "../../components/ui/button";
import { Card, CardContent, CardDescription, CardHeader, CardTitle } from "../../components/ui/card";
import { Input } from "../../components/ui/input";
import { GoogleLoginButton } from "../../features/auth/google-login-button";
import { writeAuthSession } from "../../features/auth/auth-session";
import { createSupabaseBrowserClient } from "../../lib/supabase-client";

export default function AuthPage() {
  const [name, setName] = useState("");
  const [email, setEmail] = useState("");
  const [password, setPassword] = useState("");
  const [status, setStatus] = useState("Sign in to continue.");
  const [loading, setLoading] = useState(false);

  const supabase = createSupabaseBrowserClient();

  async function register(event: FormEvent<HTMLFormElement>) {
    event.preventDefault();
    setLoading(true);
    try {
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

      writeAuthSession({
        accessToken: data.session.access_token,
        email: data.user?.email ?? email,
        name: data.user?.user_metadata?.name ?? name,
      });
      setStatus(`Welcome ${data.user?.email ?? email}`);
      toast.success("Account created successfully");
      window.location.assign("/");
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
      const { data, error } = await supabase.auth.signInWithPassword({
        email,
        password,
      });

      if (error) {
        setStatus(error.message);
        toast.error(error.message);
        return;
      }

      writeAuthSession({
        accessToken: data.session.access_token,
        email: data.user.email ?? email,
        name: data.user.user_metadata?.name,
      });
      setStatus(`Welcome ${data.user.email}`);
      toast.success("Authentication successful");
      window.location.assign("/");
    } catch (error) {
      const nextStatus = error instanceof Error ? error.message : "Authentication failed";
      setStatus(nextStatus);
      toast.error(nextStatus);
    } finally {
      setLoading(false);
    }
  }

  return (
    <section className="mx-auto grid w-full max-w-5xl gap-4 lg:grid-cols-[1.05fr_1fr]">
      <Card className="border-slate-800 bg-gradient-to-br from-slate-900 to-slate-800 shadow-md">
        <CardHeader>
          <CardTitle className="text-3xl leading-tight text-slate-100 md:text-4xl">Welcome back</CardTitle>
          <CardDescription className="max-w-md text-sm text-slate-400">
            Authenticate first, then continue directly into the chat workspace.
          </CardDescription>
        </CardHeader>
        <CardContent>
          <p className="text-sm text-slate-400">{status}</p>
        </CardContent>
      </Card>

      <div className="grid gap-4">
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
      </div>
    </section>
  );
}
