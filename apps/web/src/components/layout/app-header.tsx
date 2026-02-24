"use client";

import Link from "next/link";
import { Menu } from "lucide-react";

import { AppSidebar } from "./app-sidebar";
import { ThemeToggle } from "./theme-toggle";
import { Avatar, AvatarFallback } from "../ui/avatar";
import { Button } from "../ui/button";
import { Sheet, SheetContent, SheetHeader, SheetTitle, SheetTrigger } from "../ui/sheet";

export function AppHeader() {
  return (
    <header className="sticky top-0 z-30 border-b border-white/70 bg-background/80 backdrop-blur-xl">
      <div className="container flex h-16 items-center justify-between gap-3">
        <div className="flex items-center gap-2">
          <Sheet>
            <SheetTrigger asChild>
              <Button aria-label="Open navigation" className="md:hidden" size="icon" type="button" variant="outline">
                <Menu className="h-4 w-4" />
              </Button>
            </SheetTrigger>
            <SheetContent side="left">
              <SheetHeader>
                <SheetTitle>BD AI Gateway</SheetTitle>
              </SheetHeader>
              <AppSidebar className="mt-6" />
            </SheetContent>
          </Sheet>
          <p className="text-sm font-semibold tracking-wide text-foreground/80">BD AI Gateway</p>
        </div>
        <div className="flex items-center gap-2">
          <Button asChild size="sm" variant="outline" className="bg-card/80">
            <Link href="/developer">Developer Panel</Link>
          </Button>
          <Button asChild className="gap-2" size="sm" variant="ghost">
            <Link href="/settings">
              <Avatar className="h-7 w-7 border border-border/80">
                <AvatarFallback className="text-[11px] font-semibold">SP</AvatarFallback>
              </Avatar>
              Settings
            </Link>
          </Button>
          <ThemeToggle />
        </div>
      </div>
    </header>
  );
}
