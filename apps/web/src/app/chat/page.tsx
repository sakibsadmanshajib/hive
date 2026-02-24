"use client";

import { AppToaster } from "../../components/ui/sonner";
import { Card, CardContent, CardHeader, CardTitle } from "../../components/ui/card";
import { Input } from "../../components/ui/input";
import { GoogleLoginButton } from "../../features/auth/google-login-button";
import { ChatShell } from "../../features/chat/components/chat-shell";
import { MessageComposer } from "../../features/chat/components/message-composer";
import { MessageList } from "../../features/chat/components/message-list";
import { useChatSession } from "../../features/chat/use-chat-session";

export default function ChatPage() {
  const {
    conversations,
    activeConversation,
    activeConversationId,
    addConversation,
    selectConversation,
    apiKey,
    setApiKey,
    email,
    setEmail,
    password,
    setPassword,
    name,
    setName,
    model,
    setModel,
    prompt,
    setPrompt,
    loading,
    authMessage,
    errorMessage,
    registerUser,
    loginUser,
    sendMessage,
  } = useChatSession();

  return (
    <>
      <ChatShell
        conversations={conversations}
        activeConversationId={activeConversationId}
        onNewChat={addConversation}
        onSelectConversation={selectConversation}
      >
        <div className="space-y-4">
          <Card className="border-dashed bg-card/85">
            <CardHeader className="pb-2">
              <CardTitle className="text-base">Session setup</CardTitle>
            </CardHeader>
            <CardContent className="grid gap-3 sm:grid-cols-2">
              <form onSubmit={registerUser} className="space-y-2 rounded-lg border bg-background/80 p-3">
                <p className="text-sm font-semibold">Register</p>
                <Input placeholder="Name" value={name} onChange={(event) => setName(event.target.value)} />
                <Input placeholder="Email" value={email} onChange={(event) => setEmail(event.target.value)} />
                <Input placeholder="Password" type="password" value={password} onChange={(event) => setPassword(event.target.value)} />
                <button disabled={loading} type="submit" className="text-sm font-medium text-primary hover:underline">
                  Create account
                </button>
              </form>

              <form onSubmit={loginUser} className="space-y-2 rounded-lg border bg-background/80 p-3">
                <p className="text-sm font-semibold">Login</p>
                <Input placeholder="Email" value={email} onChange={(event) => setEmail(event.target.value)} />
                <Input placeholder="Password" type="password" value={password} onChange={(event) => setPassword(event.target.value)} />
                <button disabled={loading} type="submit" className="text-sm font-medium text-primary hover:underline">
                  Login
                </button>
                <GoogleLoginButton className="mt-3" />
              </form>

              <div className="sm:col-span-2">
                <label className="space-y-2 text-sm font-medium">
                  API key
                  <Input value={apiKey} onChange={(event) => setApiKey(event.target.value)} placeholder="sk_live_..." />
                </label>
                <p className="mt-2 text-sm text-muted-foreground">{authMessage}</p>
              </div>
            </CardContent>
          </Card>

          <MessageList messages={activeConversation?.messages ?? []} loading={loading} errorMessage={errorMessage} />

          <div className="sticky bottom-2">
            <MessageComposer
              prompt={prompt}
              model={model}
              loading={loading}
              onPromptChange={setPrompt}
              onModelChange={setModel}
              onSend={sendMessage}
            />
          </div>
        </div>
      </ChatShell>
      <AppToaster position="top-right" />
    </>
  );
}
