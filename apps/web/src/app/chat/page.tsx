"use client";

import { useEffect } from "react";
import { useRouter } from "next/navigation";

import { AppToaster } from "../../components/ui/sonner";
import { ChatShell } from "../../features/chat/components/chat-shell";
import { MessageComposer } from "../../features/chat/components/message-composer";
import { MessageList } from "../../features/chat/components/message-list";
import { useChatSession } from "../../features/chat/use-chat-session";
import { readAuthSession } from "../../features/auth/auth-session";

export default function ChatPage() {
  const router = useRouter();
  const authSession = readAuthSession();

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
    if (!authSession?.apiKey) {
      router.push("/auth");
    }
  }, [authSession?.apiKey, router]);

  if (!authSession?.apiKey) {
    return null;
  }

  return (
    <>
      <ChatShell
        conversations={conversations}
        activeConversationId={activeConversationId}
        onNewChat={addConversation}
        onSelectConversation={selectConversation}
      >
        <div className="space-y-4">
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
