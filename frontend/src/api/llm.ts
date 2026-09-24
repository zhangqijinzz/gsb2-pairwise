import { callService } from './wails';

export type LlmProviderType = 'openai_compatible' | 'anthropic' | 'claude_code_acp' | 'codex_acp';

export interface LlmProviderConfig {
  id: string;
  name: string;
  providerType: LlmProviderType;
  model: string;
  polishModel: string;
  baseUrl?: string | null;
  apiKey: string;
  hasApiKey?: boolean;
  isDefault: boolean;
}

export function isDeepSeekFlashProvider(provider: LlmProviderConfig): boolean {
  const model = provider.model.trim().toLowerCase();
  if (model !== 'deepseek-v4-flash' && model !== 'deepseek-flash') return false;
  if (provider.providerType === 'claude_code_acp') return true;
  if (provider.providerType !== 'openai_compatible' || !provider.hasApiKey) return false;
  const baseUrl = (provider.baseUrl ?? '').trim().toLowerCase().replace(/\/$/, '');
  return baseUrl === 'https://api.deepseek.com' || baseUrl === 'https://api.deepseek.com/v1';
}

export interface GeneratePromptRequest {
  taskId: string;
  providerId?: string | null;
  taskType: string;
  scopes: string[];
  constraints: string[];
  additionalNotes?: string | null;
  thinkingBudget?: string;
}

export interface AnalyzedFileSnippet {
  path: string;
  snippet: string;
}

export interface CodeAnalysisSummary {
  repoPath: string;
  totalFiles: number;
  detectedStack: string[];
  fileTree: string[];
  keyFiles: AnalyzedFileSnippet[];
}

export interface PromptGenerationResult {
  promptText: string;
  promptDifficulty: string;
  analysis: CodeAnalysisSummary;
  providerName: string;
  model: string;
  status: string;
}

export interface GenerateCustomProjectPromptDocumentsRequest {
  counts?: CustomPromptCounts;
  projectId: string;
  projectNames: string[];
  providerId?: string | null;
}

export interface CustomPromptCounts {
  codeGen: number;
  feature: number;
  bugFix: number;
  difficult: number;
  hell: number;
}

export interface CustomProjectPromptDocumentDetail {
  projectName: string;
  sourcePath: string;
  outputPath: string;
  content: string;
  status: string;
  message: string;
}

export interface GenerateCustomProjectPromptDocumentsResult {
  projectId: string;
  rootPath: string;
  providerName: string;
  model: string;
  generatedCount: number;
  errorCount: number;
  details: CustomProjectPromptDocumentDetail[];
}

export async function testLlmProvider(provider: LlmProviderConfig): Promise<boolean> {
  return callService('PromptService', 'TestLLMProvider', provider);
}

export async function generateTaskPrompt(
  request: GeneratePromptRequest,
): Promise<PromptGenerationResult> {
  return callService('PromptService', 'GenerateTaskPrompt', request);
}

export async function saveTaskPrompt(taskId: string, promptText: string): Promise<void> {
  return callService('PromptService', 'SaveTaskPrompt', taskId, promptText);
}

export async function generateCustomProjectPromptDocuments(
  request: GenerateCustomProjectPromptDocumentsRequest,
): Promise<GenerateCustomProjectPromptDocumentsResult> {
  return callService('PromptService', 'GenerateCustomProjectPromptDocuments', request);
}

export async function readCustomProjectPromptDocument(
  path: string,
): Promise<CustomProjectPromptDocumentDetail> {
  return callService('PromptService', 'ReadCustomProjectPromptDocument', path);
}

export async function saveCustomProjectPromptDocument(
  path: string,
  content: string,
): Promise<CustomProjectPromptDocumentDetail> {
  return callService('PromptService', 'SaveCustomProjectPromptDocument', { path, content });
}

export interface PolishTextRequest {
  text: string;
  providerId?: string | null;
}

export interface PolishTextResult {
  polishedText: string;
  providerName: string;
  model: string;
}

export async function polishText(request: PolishTextRequest): Promise<PolishTextResult> {
  return callService('PromptService', 'PolishText', request);
}
