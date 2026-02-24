"use client";

import { FormEvent, useState } from "react";
import { toast } from "sonner";

import { Button } from "../../components/ui/button";
import { Card, CardContent, CardDescription, CardHeader, CardTitle } from "../../components/ui/card";
import { Input } from "../../components/ui/input";
import { GoogleLoginButton } from "../../features/auth/google-login-button";
import { writeAuthSession } from "../../features/auth/auth-session";

const apiBase = process.env.NEXT_PUBLIC_API_BASE_URL ?? "http://127.0.0.1:8080";

type AuthResponse = {
  api_key: string;
  user: { email: string; name?: string };
  error?: string;
};

export default function AuthPage() {
  const [name, setName] = useState("");
  const [email, setEmail] = useState("");
  const [password, setPassword] = useState("");
  const [status, setStatus] = useState("Sign in to continue.");
  const [loading, setLoading] = useState(false);

  async function runAuth(endpoint: "register" | "login", payload: Record<string, string>) {
    setLoading(true);
    try {
      const response = await fetch(`${apiBase}/v1/users/${endpoint}`, {
        method: "POST",
        headers: { "content-type": "application/json" },
        body: JSON.stringify(payload),
      });
      const json = (await response.json()) as AuthResponse;

      if (!response.ok) {
        const nextStatus = json.error ?? "Authentication failed";
        setStatus(nextStatus);
        toast.error(nextStatus);
        return;
      }

      writeAuthSession({
        apiKey: json.api_key,
        email: json.user.email,
        name: json.user.name,
      });
      setStatus(`Welcome ${json.user.email}`);
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

  function register(event: FormEvent<HTMLFormElement>) {
    event.preventDefault();
    void runAuth("register", { name, email, password });
  }

  function login(event: FormEvent<HTMLFormElement>) {
    event.preventDefault();
    void runAuth("login", { email, password });
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
