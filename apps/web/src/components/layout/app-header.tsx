"use client";

import { Menu } from "lucide-react";

import { AppSidebar } from "./app-sidebar";
import { ThemeToggle } from "./theme-toggle";
import { Button } from "../ui/button";
import { Sheet, SheetContent, SheetHeader, SheetTitle, SheetTrigger } from "../ui/sheet";

export function AppHeader() {
  return (
    <header className="sticky top-0 z-30 border-b bg-background/90 backdrop-blur">
      <div className="container flex h-14 items-center justify-between gap-3">
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
          <p className="text-sm font-semibold tracking-wide text-muted-foreground">BD AI Gateway</p>
        </div>
        <ThemeToggle />
      </div>
    </header>
  );
}
