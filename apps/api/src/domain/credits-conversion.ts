export function bdtToCredits(amount: number): number {
  return Math.round(Math.max(0, amount) * 100);
}
