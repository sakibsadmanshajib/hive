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
    <section className="grid gap-4 lg:grid-cols-[290px_1fr]">
      <div className="lg:hidden">
        <Sheet>
          <SheetTrigger asChild>
            <Button type="button" variant="outline" className="w-full justify-start">
              <Menu className="h-4 w-4" />
              Conversations
            </Button>
          </SheetTrigger>
          <SheetContent side="left" className="w-[88vw] max-w-[320px] p-4">
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

      <Card className="hidden h-[calc(100vh-10.5rem)] border-slate-800/70 bg-slate-950 text-slate-100 lg:block">
        <CardHeader>
          <CardTitle className="text-base tracking-wide text-slate-200">Conversations</CardTitle>
        </CardHeader>
        <CardContent>
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
