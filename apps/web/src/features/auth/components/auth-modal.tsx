"use client";

import { useEffect, useRef } from "react";
import { X } from "lucide-react";

import { Button } from "../../../components/ui/button";
import { AuthExperience } from "./auth-experience";

type AuthModalProps = {
  open: boolean;
  onClose: () => void;
};

export function AuthModal({ open, onClose }: AuthModalProps) {
  const containerRef = useRef<HTMLDivElement | null>(null);
  const closeButtonRef = useRef<HTMLButtonElement | null>(null);
  const previousFocusRef = useRef<HTMLElement | null>(null);

  useEffect(() => {
    if (!open) {
      return;
    }

    previousFocusRef.current = document.activeElement instanceof HTMLElement ? document.activeElement : null;
    closeButtonRef.current?.focus();

    const handleKeyDown = (event: KeyboardEvent) => {
      if (event.key === "Escape") {
        event.preventDefault();
        onClose();
        return;
      }

      if (event.key !== "Tab" || !containerRef.current) {
        return;
      }

      const focusable = Array.from(
        containerRef.current.querySelectorAll<HTMLElement>(
          "button, [href], input, select, textarea, [tabindex]:not([tabindex='-1'])",
        ),
      ).filter((element) => !element.hasAttribute("disabled"));
      if (focusable.length === 0) {
        event.preventDefault();
        return;
      }

      const first = focusable[0];
      const last = focusable[focusable.length - 1];
      if (event.shiftKey && document.activeElement === first) {
        event.preventDefault();
        last.focus();
        return;
      }
      if (!event.shiftKey && document.activeElement === last) {
        event.preventDefault();
        first.focus();
      }
    };

    document.addEventListener("keydown", handleKeyDown);
    return () => {
      document.removeEventListener("keydown", handleKeyDown);
      previousFocusRef.current?.focus();
    };
  }, [open, onClose]);

  if (!open) {
    return null;
  }

  return (
    <div className="fixed inset-0 z-50 flex items-center justify-center bg-slate-950/70 p-4" role="dialog" aria-modal="true" aria-labelledby="auth-modal-title">
      <div className="absolute inset-0" onClick={onClose} />
      <div ref={containerRef} className="relative z-10 w-full max-w-3xl rounded-3xl border border-slate-200 bg-white p-4 shadow-2xl sm:p-6">
        <div className="mb-3 flex justify-end">
          <Button ref={closeButtonRef} type="button" variant="ghost" size="icon" aria-label="Close sign in modal" onClick={onClose}>
            <X className="h-4 w-4" />
          </Button>
        </div>
        <AuthExperience variant="modal" onAuthenticated={onClose} onDismiss={onClose} />
      </div>
    </div>
  );
}
