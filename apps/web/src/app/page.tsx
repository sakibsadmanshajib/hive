"use client";

import { AuthModal } from "../features/auth/components/auth-modal";
import { ChatWorkspaceShell } from "../features/chat/components/chat-workspace-shell";
import { MessageComposer } from "../features/chat/components/message-composer";
import { MessageList } from "../features/chat/components/message-list";
import { useChatSession } from "../features/chat/use-chat-session";
import { useSupabaseAuthSessionSync } from "../lib/supabase-client";

export default function HomePage() {
  useSupabaseAuthSessionSync();
  const {
    conversations,
    activeConversation,
    activeConversationId,
    addConversation,
    selectConversation,
    model,
    prompt,
    setPrompt,
    loading,
    errorMessage,
    sendMessage,
    modelOptions,
    guestMode,
    authModalOpen,
    closeAuthModal,
    onModelChange,
  } = useChatSession();

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
          {guestMode ? (
            <p className="mb-3 rounded-2xl border border-amber-200 bg-amber-50 px-4 py-3 text-sm text-amber-900">
              Guest mode is active. Sign in to unlock paid models and top up credits.
            </p>
          ) : null}
          <MessageComposer
            prompt={prompt}
            model={model}
            modelOptions={modelOptions}
            guestMode={guestMode}
            loading={loading}
            onPromptChange={setPrompt}
            onModelChange={onModelChange}
            onSend={sendMessage}
          />
        </div>
      </div>
      <AuthModal open={authModalOpen} onClose={closeAuthModal} />
    </ChatWorkspaceShell>
  );
}
