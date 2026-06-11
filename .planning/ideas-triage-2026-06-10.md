# Ideas Triage, 2026-06-10

Source: 15 social media screenshots from owner (`hive-ideas` folder). Extracted by vision agents, synthesized by orchestrator. Each idea mapped to a Hive decision: BUILD (scheduled), ADOPT (integrate existing OSS), WATCH (revisit later), SKIP (with reason).

## Theme 1: Persistent memory (strongest recurring signal, 4 of 15 ideas)

| Idea | What it is | Decision |
|---|---|---|
| Memvid | Single file agent memory layer, no DB, 14K stars | ADOPT candidate for Hive Memory backend |
| Obsidian Mind | Vault based cross session memory for Claude Code | Pattern reference |
| Rowboat | Local AI coworker, knowledge graph from email and notes | Pattern reference, connector ideas |
| OpenHuman | Local persistent personal agent | Pattern reference |

**Call: "Hive Memory" becomes a named v1.2 feature.** Per user persistent memory across chat sessions, tenant scoped, exposed in the chat workstation. This is the retention differentiator versus raw ChatGPT clones in the BD market. Evaluate Memvid first (ADOPT before build). Slot: new Phase 31 in v1.2 roadmap, size M.

## Theme 2: Workstation in a box (EnterpriseEdge packaging)

| Idea | What it is | Decision |
|---|---|---|
| HolyClaude | `docker compose up` full AI dev workstation | ADOPT patterns for EnterpriseEdge bundle |
| OpenJarvis | Local first agents, cloud only when needed | Pattern: local Ollama fallback tier |
| llm-checker | Hardware detection, recommends local LLMs (Bangladeshi author) | BUILD into self hosted onboarding wizard, size S. Also: community contact worth reaching out to |

**Call: EnterpriseEdge ships as one compose bundle** (gateway + OWUI + LiteLLM + optional Ollama), hardware aware model auto config at install. Folds into v1.2 Phase 30 (productization). The pitch for banks, telcos, government: your AI, your server, your data, pay in BDT.

## Theme 3: Skills and agent ecosystem

| Idea | What it is | Decision |
|---|---|---|
| Everything Claude Code | Curated skill and rules library | BUILD: skill preset packs as OWUI workspace templates, size S, quick win after Phase 19 closes |
| SkillNet | npm style marketplace for agent skills | WATCH: marketplace is a v1.3+ moat play, too early |
| CrewAI | Multi agent orchestration framework | WATCH: a Crews style API needs tool calling + sandboxing first |
| AgentHandover | Record workflow once, agent repeats it | WATCH: strong BD SME automation story, needs computer use stack, v1.3+ |
| My Brain Is Full Crew | Personal wellness agent crew | SKIP as product, keep as persona template idea |

## Theme 4: Routing and cost (core gateway)

| Idea | What it is | Decision |
|---|---|---|
| 9Router | Multi provider router npm tool, free model fallback, RTK token compression | Competitor intel. BUILD: prompt compression middleware in edge-api, size S to M, direct margin improvement. Free model fallback aligns with existing hive-auto alias |
| turbovec | Rust vector index, 10M docs in 4 GB RAM | WATCH: candidate vector backend for Phase 22 shared RAG self hosted tier |
| MiroShark | Agent swarm simulates public opinion from documents | SKIP: disinformation misuse risk, thin BD revenue, reputational hazard for a payments licensed business |

## Priority queue (owner to confirm)

1. **Skill preset packs** (S): curate system prompt + tool presets into OWUI templates. Earliest shippable, zero new infra.
2. **Prompt compression middleware** (S/M): gateway level token reduction, improves margin on every request.
3. **llm-checker style onboarding** (S): EnterpriseEdge installer wizard. Pair with outreach to the Bangladeshi author.
4. **Hive Memory** (M): evaluate Memvid, then design per tenant memory service. Add as Phase 31 to v1.2 roadmap.
5. **EnterpriseEdge compose bundle** (M): already aligned with Phase 30.

Everything WATCH/SKIP gets revisited at v1.2 close.
