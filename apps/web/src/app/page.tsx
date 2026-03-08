"use client";

import { useEffect, useState } from "react";
import { useRouter } from "next/navigation";

import { type AuthSession, readAuthSession } from "../features/auth/auth-session";
import { ChatWorkspaceShell } from "../features/chat/components/chat-workspace-shell";
import { MessageComposer } from "../features/chat/components/message-composer";
import { MessageList } from "../features/chat/components/message-list";
import { useChatSession } from "../features/chat/use-chat-session";

export default function HomePage() {
  const router = useRouter();
  const [authSession] = useState<AuthSession | null>(() => readAuthSession());
  const {
    conversations,
    activeConversation,
    activeConversationId,
    addConversation,
    selectConversation,
    model,
    setModel,
    prompt,
    setPrompt,
    loading,
    errorMessage,
    sendMessage,
  } = useChatSession();

  useEffect(() => {
    if (!authSession?.accessToken) {
      router.push("/auth");
    }
  }, [authSession?.accessToken, router]);

  if (!authSession?.accessToken) {
    return null;
  }

  return (
    <ChatWorkspaceShell
      conversations={conversations}
      activeConversationId={activeConversationId}
      onNewChat={addConversation}
      onSelectConversation={selectConversation}
    >
      <div className="flex h-full min-h-[60vh] flex-col gap-4">
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
    </ChatWorkspaceShell>
  );
}
