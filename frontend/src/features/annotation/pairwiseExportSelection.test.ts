import { describe, expect, it } from 'vitest';
import type { AnnotationCase, PairwiseReview } from '../../api/annotation';
import { currentExportReview, hasGeneratedPairwiseGsb, pairwiseExportBlocker, selectableExportIds } from './pairwiseExportSelection';

function makeReview(overrides: Partial<PairwiseReview> = {}): PairwiseReview {
  return {
    current: true,
    id: 'review-1',
    status: 'ready',
    conclusion: 'A_better',
    reason: 'A 完成了题目要求的关键改动，B 只改了样式。',
    model: 'Codex CLI',
    skillHash: 'skill',
    sourceHashA: 'a',
    sourceHashB: 'b',
    reviewPath: '/review',
    reviewHash: 'hash',
    createdAt: 1,
    ...overrides,
  };
}

function makeCase(taskId: string, reviews: PairwiseReview[], mode: AnnotationCase['mode'] = 'pairwise_gsb'): AnnotationCase {
  return { taskId, taskName: taskId, mode, pairwise: { reviews } } as unknown as AnnotationCase;
}

describe('pairwise export selection', () => {
  it('only treats pairwise cases with any review as generated GSB', () => {
    expect(hasGeneratedPairwiseGsb(makeCase('task-1', []))).toBe(false);
    expect(hasGeneratedPairwiseGsb(makeCase('task-1', [makeReview()]))).toBe(true);
    expect(hasGeneratedPairwiseGsb(makeCase('task-1', [makeReview()], 'legacy'))).toBe(false);
  });

  it('accepts a current ready review and reports why others are blocked', () => {
    expect(pairwiseExportBlocker(makeCase('task-1', [makeReview()]))).toBe('');
    expect(pairwiseExportBlocker(makeCase('task-1', []))).toBe('尚未生成 GSB');
    expect(pairwiseExportBlocker(makeCase('task-1', [makeReview({ current: false })]))).toBe('A/B 证据已变化，需重新审核');
    expect(pairwiseExportBlocker(makeCase('task-1', [makeReview({ reason: '   ' })]))).toBe('GSB 理由不完整，需重新审核');
    expect(pairwiseExportBlocker(makeCase('task-1', [makeReview({ status: 'needs_evidence' })]))).toBe('GSB 结论未就绪，需重新审核');
    expect(pairwiseExportBlocker(makeCase('task-1', [makeReview({ reason: 'A 更好，因为『移除了多余状态』。' })]))).toBe('GSB 理由含装饰括号，需重新审核');
  });

  it('keeps the newest current review and skips blocked tasks when selecting', () => {
    const stale = makeReview({ id: 'old', current: false, reason: '旧理由' });
    const fresh = makeReview({ id: 'new' });
    expect(currentExportReview(makeCase('task-1', [stale, fresh]))?.id).toBe('new');
    expect(currentExportReview(makeCase('task-1', [stale]))).toBeUndefined();
    expect(selectableExportIds([
      makeCase('task-1', [fresh]),
      makeCase('task-2', [stale]),
      makeCase('task-3', []),
    ])).toEqual(['task-1']);
  });
});
