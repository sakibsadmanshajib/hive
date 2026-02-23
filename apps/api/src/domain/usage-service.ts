import { randomUUID } from "node:crypto";
import type { UsageEvent } from "./types";

export class UsageService {
  private readonly events: UsageEvent[] = [];

  add(entry: Omit<UsageEvent, "id" | "createdAt">): UsageEvent {
    const event: UsageEvent = {
      ...entry,
      id: `usage_${randomUUID()}`,
      createdAt: new Date().toISOString(),
    };
    this.events.unshift(event);
    return event;
  }

  list(userId: string): UsageEvent[] {
    return this.events.filter((event) => event.userId === userId);
  }
}
