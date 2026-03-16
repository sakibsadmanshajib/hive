import * as React from "react";

type SelectContextType = {
  value: string;
  onValueChange: (value: string) => void;
  open: boolean;
  setOpen: (open: boolean) => void;
};

const SelectContext = React.createContext<SelectContextType>({
  value: "",
  onValueChange: () => {},
  open: false,
  setOpen: () => {},
});

export function Select({
  value = "",
  onValueChange = () => {},
  children,
}: {
  value?: string;
  onValueChange?: (value: string) => void;
  children: React.ReactNode;
}) {
  const [open, setOpen] = React.useState(false);
  return (
    <SelectContext.Provider value={{ value, onValueChange, open, setOpen }}>
      <div data-testid="select-root">{children}</div>
    </SelectContext.Provider>
  );
}

export const SelectTrigger = React.forwardRef<
  HTMLButtonElement,
  React.ButtonHTMLAttributes<HTMLButtonElement>
>(({ children, ...props }, ref) => {
  const { open, setOpen } = React.useContext(SelectContext);
  return (
    <button
      ref={ref}
      role="combobox"
      aria-expanded={open}
      type="button"
      onClick={() => setOpen(!open)}
      {...props}
    >
      {children}
    </button>
  );
});
SelectTrigger.displayName = "SelectTrigger";

export function SelectValue({ placeholder }: { placeholder?: string }) {
  const { value } = React.useContext(SelectContext);
  return <span>{value || placeholder}</span>;
}

export function SelectContent({ children }: { children: React.ReactNode }) {
  const { open } = React.useContext(SelectContext);
  if (!open) return null;
  return <div role="listbox">{children}</div>;
}

export const SelectItem = React.forwardRef<
  HTMLDivElement,
  { value: string; children: React.ReactNode }
>(({ value, children, ...props }, ref) => {
  const ctx = React.useContext(SelectContext);
  return (
    <div
      ref={ref}
      role="option"
      aria-selected={ctx.value === value}
      onClick={() => {
        ctx.onValueChange(value);
        ctx.setOpen(false);
      }}
      {...props}
    >
      {children}
    </div>
  );
});
SelectItem.displayName = "SelectItem";

export function SelectGroup({ children }: { children: React.ReactNode }) {
  return <div role="group">{children}</div>;
}
export function SelectLabel({ children, ...props }: { children: React.ReactNode }) {
  return <div {...props}>{children}</div>;
}
export function SelectSeparator() {
  return <hr />;
}
export const SelectViewport = ({ children }: { children: React.ReactNode }) => <>{children}</>;
