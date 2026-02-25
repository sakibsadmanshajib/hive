export interface TokenUsage {
  input: number;
  output: number;
}

export interface ProviderCostConfig {
  inputCostPerToken: number;
  outputCostPerToken: number;
  baseCredits?: number;
}

export class ProviderCostCalculator {
  private readonly providerCosts: Record<string, ProviderCostConfig> = {
    openrouter: {
      // OpenRouter uses actual provider pricing + 5.5% fee
      // Approximate: $0.0015 input, $0.002 output per 1K tokens + 5.5% fee
      inputCostPerToken: 0.00000158, // $1.58 per 1M input tokens
      outputCostPerToken: 0.00000211, // $2.11 per 1M output tokens
    },
    // Fallback costs for other providers
    default: {
      inputCostPerToken: 0.000001,
      outputCostPerToken: 0.000002,
      baseCredits: 8,
    },
  };

  calculateCredits(provider: string, model: string, tokens: TokenUsage): number {
    const config = this.providerCosts[provider];
    
    // If provider has specific token costs configured, use them
    if (config && config.inputCostPerToken && config.outputCostPerToken) {
      const inputCost = tokens.input * config.inputCostPerToken;
      const outputCost = tokens.output * config.outputCostPerToken;
      const totalCost = inputCost + outputCost;
      
      // Convert to Hive credits (1 credit ≈ $0.001)
      return Math.max(1, Math.ceil(totalCost * 1000));
    }
    
    // Otherwise, use the default fallback credits
    return this.providerCosts.default.baseCredits || 8;
  }

  // Estimate tokens for messages (rough approximation)
  estimateTokens(messages: any[]): number {
    return messages.reduce((total, msg) => {
      const content = typeof msg.content === 'string' ? msg.content : JSON.stringify(msg.content);
      // Rough estimate: 1 token per 4 characters
      return total + Math.ceil(content.length / 4);
    }, 0);
  }
}