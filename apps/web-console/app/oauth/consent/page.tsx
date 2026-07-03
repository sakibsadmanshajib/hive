import { ConsentPanel } from "@/components/oauth/consent-panel";

interface ConsentPageProps {
  searchParams: Promise<{ authorization_id?: string }>;
}

export default async function ConsentPage({ searchParams }: ConsentPageProps) {
  const { authorization_id: authorizationId } = await searchParams;

  return <ConsentPanel authorizationId={authorizationId ?? null} />;
}
