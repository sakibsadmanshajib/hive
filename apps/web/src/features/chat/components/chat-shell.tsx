import type { ReactNode } from "react";
import { Menu } from "lucide-react";

import { Button } from "../../../components/ui/button";
import { Card, CardContent, CardHeader, CardTitle } from "../../../components/ui/card";
import { Sheet, SheetContent, SheetTitle, SheetTrigger } from "../../../components/ui/sheet";
import { ConversationList } from "./conversation-list";

type ConversationItem = {
  id: string;
  title: string;
};

type ChatShellProps = {
  conversations: ConversationItem[];
  activeConversationId: string;
  children: ReactNode;
  onNewChat: () => void;
  onSelectConversation: (conversationId: string) => void;
};

export function ChatShell({ conversations, activeConversationId, children, onNewChat, onSelectConversation }: ChatShellProps) {
  return (
    <section className="grid gap-4 lg:grid-cols-[310px_1fr]">
      <div className="lg:hidden">
        <Sheet>
          <SheetTrigger asChild>
            <Button type="button" variant="outline" className="w-full justify-start rounded-xl bg-card/85 shadow-sm backdrop-blur">
              <Menu className="h-4 w-4" />
              Conversations
            </Button>
          </SheetTrigger>
          <SheetContent side="left" className="w-[88vw] max-w-[320px] border-r border-slate-200/80 bg-[linear-gradient(180deg,rgba(255,255,255,0.96)_0%,rgba(248,250,252,0.92)_100%)] p-4">
            <SheetTitle className="mb-3">Chats</SheetTitle>
            <ConversationList
              conversations={conversations}
              activeConversationId={activeConversationId}
              onNewChat={onNewChat}
              onSelectConversation={onSelectConversation}
            />
          </SheetContent>
        </Sheet>
      </div>

      <Card className="hidden h-[calc(100vh-10.5rem)] rounded-2xl border border-slate-900/80 bg-[linear-gradient(180deg,rgba(10,16,32,0.98)_0%,rgba(17,24,39,0.95)_100%)] text-slate-100 shadow-[0_24px_48px_-30px_rgba(2,6,23,0.95)] lg:block">
        <CardHeader>
          <CardTitle className="text-base tracking-wide text-slate-100">Conversations</CardTitle>
        </CardHeader>
        <CardContent className="pt-0">
          <ConversationList
            conversations={conversations}
            activeConversationId={activeConversationId}
            onNewChat={onNewChat}
            onSelectConversation={onSelectConversation}
          />
        </CardContent>
      </Card>

      <div>{children}</div>
    </section>
  );
}
