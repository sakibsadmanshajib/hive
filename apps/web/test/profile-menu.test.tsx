// @vitest-environment jsdom

import "@testing-library/jest-dom/vitest";
import { fireEvent, render, screen, waitFor } from "@testing-library/react";
import type { ButtonHTMLAttributes, ReactNode } from "react";
import { beforeEach, describe, expect, it, vi } from "vitest";

import { AUTH_STORAGE_KEY } from "../src/features/auth/auth-session";

const pushMock = vi.fn();
const signOutMock = vi.fn();

vi.mock("next/navigation", () => ({
  useRouter: () => ({
    push: pushMock,
  }),
}));

vi.mock("../src/lib/supabase-client", () => ({
  createSupabaseBrowserClient: () => ({
    auth: {
      signOut: signOutMock,
    },
  }),
}));

vi.mock("../src/components/ui/dropdown-menu", () => ({
  DropdownMenu: ({ children }: { children: ReactNode }) => <div>{children}</div>,
  DropdownMenuTrigger: ({ children, ...props }: ButtonHTMLAttributes<HTMLButtonElement>) => (
    <button type="button" {...props}>
      {children}
    </button>
  ),
  DropdownMenuContent: ({ children }: { children: ReactNode }) => <div>{children}</div>,
  DropdownMenuItem: ({
    children,
    onSelect,
    ...props
  }: ButtonHTMLAttributes<HTMLButtonElement> & { onSelect?: () => void }) => (
    <button
      type="button"
      onClick={() => onSelect?.()}
      {...props}
    >
      {children}
    </button>
  ),
  DropdownMenuLabel: ({ children }: { children: ReactNode }) => <div>{children}</div>,
  DropdownMenuSeparator: () => <hr />,
}));

import { ProfileMenu } from "../src/features/account/components/profile-menu";

describe("ProfileMenu", () => {
  beforeEach(() => {
    pushMock.mockReset();
    signOutMock.mockReset();
    window.localStorage.clear();
    window.localStorage.setItem(
      AUTH_STORAGE_KEY,
      JSON.stringify({ accessToken: "sk_test", email: "demo@example.com", name: "Demo User" }),
    );
  });

  it("clears the local session and redirects even if sign out fails", async () => {
    signOutMock.mockRejectedValueOnce(new Error("network down"));

    render(<ProfileMenu />);

    fireEvent.click(screen.getByRole("button", { name: /open profile menu/i }));
    fireEvent.click(await screen.findByRole("button", { name: /log out/i }));

    await waitFor(() => {
      expect(window.localStorage.getItem(AUTH_STORAGE_KEY)).toBeNull();
      expect(pushMock).toHaveBeenCalledWith("/auth");
    });
  });
});
