# BD AI Credit Gateway Implementation Plan

> **For Claude:** REQUIRED SUB-SKILL: Use superpowers:executing-plans to implement this plan task-by-task.

**Goal:** Launch a ship-ready MVP for Bangladesh users with local payment-driven top-up and OpenAI-compatible AI access.

**Architecture:** Python service with in-memory core domain objects for credits, routing, and payments. Stateless HTTP layer exposes API and a lightweight chat UI for MVP validation.

**Tech Stack:** Python 3.12 standard library, unittest, OpenAPI 3.1.

---

## Completed Tasks
1. Core domain: ledger, refunds, routing, payment idempotency, rate limiting.
2. API endpoints: `/v1/models`, `/v1/chat/completions`, `/v1/responses`, `/v1/images/generations`, `/v1/credits/balance`, `/v1/usage`, payment intent/webhook routes.
3. Basic web UI: chat page served at `/`.
4. Ship docs: OpenAPI spec, runbooks, launch checklist, fallback matrix.

## Remaining for Production Hardening
1. Replace in-memory stores with PostgreSQL + Redis.
2. Add real bKash and SSLCOMMERZ adapters with signature verification.
3. Add auth service for multi-key user management.
4. Add observability pipeline and alerting.
