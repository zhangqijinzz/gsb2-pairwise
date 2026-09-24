import { describe, expect, it } from 'vitest';
import { isDeepSeekFlashProvider } from './llm';

describe('isDeepSeekFlashProvider', () => {
  it('accepts the official API provider and the existing Claude ACP route', () => {
    expect(isDeepSeekFlashProvider({
      id: 'api', name: 'DeepSeek', providerType: 'openai_compatible', model: 'deepseek-v4-flash',
      polishModel: '', baseUrl: 'https://api.deepseek.com', apiKey: '', hasApiKey: true, isDefault: true,
    })).toBe(true);
    expect(isDeepSeekFlashProvider({
      id: 'acp', name: 'DeepSeek ACP', providerType: 'claude_code_acp', model: 'deepseek-flash',
      polishModel: '', apiKey: '', isDefault: false,
    })).toBe(true);
  });

  it('rejects other models and API providers without a saved key', () => {
    expect(isDeepSeekFlashProvider({
      id: 'gpt', name: 'GPT', providerType: 'codex_acp', model: 'gpt-5.5', polishModel: '', apiKey: '', isDefault: true,
    })).toBe(false);
    expect(isDeepSeekFlashProvider({
      id: 'empty', name: 'DeepSeek', providerType: 'openai_compatible', model: 'deepseek-v4-flash',
      polishModel: '', baseUrl: 'https://api.deepseek.com', apiKey: '', hasApiKey: false, isDefault: false,
    })).toBe(false);
  });
});
