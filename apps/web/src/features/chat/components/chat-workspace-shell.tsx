import type { ReactNode } from "react";
import { Menu } from "lucide-react";

import { Button } from "../../../components/ui/button";
import { Sheet, SheetContent, SheetTitle, SheetTrigger } from "../../../components/ui/sheet";
import { ProfileMenu } from "../../account/components/profile-menu";
import { ConversationList } from "./conversation-list";

type ConversationItem = {
  id: string;
  title: string;
};

type ChatWorkspaceShellProps = {
  conversations: ConversationItem[];
  activeConversationId: string;
  guestMode: boolean;
  children: ReactNode;
  onNewChat: () => void;
  onOpenAuthModal: () => void;
  onSelectConversation: (conversationId: string) => void;
};

export function ChatWorkspaceShell({
  conversations,
  activeConversationId,
  guestMode,
  children,
  onNewChat,
  onOpenAuthModal,
  onSelectConversation,
}: ChatWorkspaceShellProps) {
  return (
    <section className="flex min-h-[calc(100vh-4.5rem)] flex-col gap-4">
      <div className="flex items-center justify-between gap-3 rounded-2xl border border-slate-800 bg-slate-950/90 px-3 py-2 sm:px-4">
        <div className="flex items-center gap-2 text-slate-200">
          <Sheet>
            <SheetTrigger asChild>
              <Button type="button" variant="ghost" className="h-9 w-9 p-0 text-slate-200 hover:bg-slate-800 lg:hidden" aria-label="Open conversations">
                <Menu className="h-4 w-4" />
              </Button>
            </SheetTrigger>
            <SheetContent side="left" className="w-[86vw] max-w-[320px] border-slate-800 bg-slate-950 p-4 text-slate-100">
              <SheetTitle className="mb-4 text-sm font-semibold uppercase tracking-wide text-slate-300">Conversations</SheetTitle>
              <ConversationList
                conversations={conversations}
                activeConversationId={activeConversationId}
                onNewChat={onNewChat}
                onSelectConversation={onSelectConversation}
              />
            </SheetContent>
          </Sheet>
          <p className="hidden text-sm font-medium text-slate-400 sm:block">BD AI Chat</p>
        </div>
        {guestMode ? (
          <Button
            type="button"
            variant="outline"
            className="border-slate-700 bg-slate-900 text-slate-100 hover:bg-slate-800 hover:text-slate-50"
            onClick={onOpenAuthModal}
          >
            Sign in
          </Button>
        ) : (
          <ProfileMenu />
        )}
      </div>

      <div className="grid min-h-0 flex-1 gap-4 lg:grid-cols-[280px_1fr]">
        <aside className="hidden rounded-2xl border border-slate-800 bg-slate-950 p-3 lg:block">
          <ConversationList
            conversations={conversations}
            activeConversationId={activeConversationId}
            onNewChat={onNewChat}
            onSelectConversation={onSelectConversation}
          />
        </aside>
        <div className="min-h-0">{children}</div>
      </div>
    </section>
  );
}
