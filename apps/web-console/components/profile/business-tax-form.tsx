import type {
  BillingProfileFieldErrors,
  BillingProfileFormValues,
} from "@/lib/profile-schemas";
import {
  Card,
  CardContent,
  CardDescription,
  CardHeader,
  CardTitle,
} from "@/components/ui/card";
import { Field, Input } from "@/components/ui/input";
import { cn } from "@/lib/cn";

interface BusinessTaxFormProps {
  accountType: string;
  fieldErrors: BillingProfileFieldErrors;
  values: BillingProfileFormValues;
}

const SELECT_CLASSES = cn(
  "flex h-9 w-full rounded-md border border-[var(--color-border)]",
  "bg-[var(--color-surface)] px-3 text-sm text-[var(--color-ink)]",
  "transition-[border,box-shadow] duration-[var(--duration-fast)]",
  "ease-[var(--ease-out-expo)]",
  "focus-visible:outline-none focus-visible:border-[var(--color-accent)]",
  "focus-visible:ring-4 focus-visible:ring-[var(--color-accent-soft)]",
);

export function BusinessTaxForm({
  accountType,
  fieldErrors,
  values,
}: BusinessTaxFormProps) {
  const isPersonal = accountType === "personal";

  return (
    <Card>
      <CardHeader>
        <CardTitle>Legal entity and tax</CardTitle>
        <CardDescription>
          {isPersonal
            ? "Business-specific fields matter only when later checkout or invoicing requires them."
            : "You can save partial business identity details now and complete the rest only when checkout or invoicing needs them."}
        </CardDescription>
      </CardHeader>
      <CardContent className="grid gap-4 px-5 py-5">
        <Field label="Legal entity name" htmlFor="legalEntityName">
          <Input
            id="legalEntityName"
            name="legalEntityName"
            defaultValue={values.legalEntityName}
          />
        </Field>

        {isPersonal ? (
          <>
            <input
              type="hidden"
              name="legalEntityType"
              value={values.legalEntityType || "individual"}
              readOnly
            />
            <p className="text-xs text-[var(--color-ink-3)]">
              Personal accounts default the legal entity type to individual
              until a later billing flow needs something more specific.
            </p>
          </>
        ) : (
          <Field
            label="Legal entity type"
            htmlFor="legalEntityType"
            error={fieldErrors.legalEntityType}
          >
            <select
              id="legalEntityType"
              name="legalEntityType"
              defaultValue={values.legalEntityType || "private_company"}
              className={SELECT_CLASSES}
            >
              <option value="private_company">Private company</option>
              <option value="public_company">Public company</option>
              <option value="sole_proprietor">Sole proprietor</option>
              <option value="non_profit">Non-profit</option>
              <option value="individual">Individual</option>
            </select>
          </Field>
        )}

        {!isPersonal && (
          <Field
            label="Business registration number"
            htmlFor="businessRegistrationNumber"
          >
            <Input
              id="businessRegistrationNumber"
              name="businessRegistrationNumber"
              defaultValue={values.businessRegistrationNumber}
            />
          </Field>
        )}

        <div className="grid gap-4 sm:grid-cols-2">
          <Field
            label="VAT number"
            htmlFor="vatNumber"
            error={fieldErrors.vatNumber}
          >
            <Input
              id="vatNumber"
              name="vatNumber"
              defaultValue={values.vatNumber}
            />
          </Field>
          <Field label="Tax ID type" htmlFor="taxIdType">
            <Input
              id="taxIdType"
              name="taxIdType"
              defaultValue={values.taxIdType}
              placeholder="ein, gst, vat"
            />
          </Field>
        </div>

        <Field
          label="VAT / Tax ID"
          htmlFor="taxIdValue"
          error={fieldErrors.taxIdValue}
        >
          <Input
            id="taxIdValue"
            name="taxIdValue"
            defaultValue={values.taxIdValue}
          />
        </Field>
      </CardContent>
    </Card>
  );
}
