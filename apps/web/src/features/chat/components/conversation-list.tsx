import { Plus } from "lucide-react";

import { Button } from "../../../components/ui/button";
import { cn } from "../../../lib/utils";

type ConversationItem = {
  id: string;
  title: string;
};

type ConversationListProps = {
  conversations: ConversationItem[];
  activeConversationId: string;
  onNewChat: () => void;
  onSelectConversation: (conversationId: string) => void;
};

export function ConversationList({ conversations, activeConversationId, onNewChat, onSelectConversation }: ConversationListProps) {
  return (
    <div className="flex h-full flex-col gap-3">
      <Button type="button" onClick={onNewChat} className="justify-start rounded-xl bg-slate-800 text-slate-100 hover:bg-slate-700">
        <Plus className="h-4 w-4" />
        New chat
      </Button>
      <p className="px-1 text-xs uppercase tracking-[0.12em] text-slate-400">Recent</p>
      <div className="space-y-1.5">
        {conversations.map((conversation) => (
          <Button
            key={conversation.id}
            type="button"
            variant="ghost"
            onClick={() => onSelectConversation(conversation.id)}
            className={cn(
              "h-auto w-full justify-start truncate rounded-xl border px-3 py-2.5",
              conversation.id === activeConversationId
                ? "border-slate-600 bg-slate-800 text-slate-100 shadow-sm"
                : "border-transparent text-slate-400 hover:bg-slate-900 hover:text-slate-200",
            )}
          >
            <span className="truncate">{conversation.title}</span>
          </Button>
        ))}
      </div>
    </div>
  );
}
