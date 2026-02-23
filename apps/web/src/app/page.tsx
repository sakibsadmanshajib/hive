import Link from "next/link";

export default function HomePage() {
  return (
    <section>
      <h1>BD AI Gateway Demo</h1>
      <p>ChatGPT-like chat UI, user management, prepaid top-up, and OpenAI-compatible API testing.</p>
      <ul>
        <li>
          <Link href="/chat">Chat workspace</Link>
        </li>
        <li>
          <Link href="/billing">Billing and usage</Link>
        </li>
      </ul>
    </section>
  );
}
