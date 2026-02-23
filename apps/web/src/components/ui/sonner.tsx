"use client";

import { Toaster } from "sonner";
import type { ComponentProps } from "react";

type SonnerProps = ComponentProps<typeof Toaster>;

function AppToaster(props: SonnerProps) {
  return <Toaster richColors closeButton {...props} />;
}

export { AppToaster };
