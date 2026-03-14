"use client";

import { X } from "lucide-react";

import { Button } from "../../../components/ui/button";
import { AuthExperience } from "./auth-experience";

type AuthModalProps = {
  open: boolean;
  onClose: () => void;
};

export function AuthModal({ open, onClose }: AuthModalProps) {
  if (!open) {
    return null;
  }

  return (
    <div className="fixed inset-0 z-50 flex items-center justify-center bg-slate-950/70 p-4" role="dialog" aria-modal="true" aria-labelledby="auth-modal-title">
      <div className="absolute inset-0" onClick={onClose} />
      <div className="relative z-10 w-full max-w-3xl rounded-3xl border border-slate-200 bg-white p-4 shadow-2xl sm:p-6">
        <div className="mb-3 flex justify-end">
          <Button type="button" variant="ghost" size="icon" aria-label="Close sign in modal" onClick={onClose}>
            <X className="h-4 w-4" />
          </Button>
        </div>
        <AuthExperience variant="modal" onAuthenticated={onClose} onDismiss={onClose} />
      </div>
    </div>
  );
}
