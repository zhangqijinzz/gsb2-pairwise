import { describe, expect, it } from 'vitest';
import type { AnnotationRound } from '../../api/annotation';
import { getTableProgress } from './tableProgress';

function round(overrides: Partial<AnnotationRound> = {}): AnnotationRound {
  return { promptId: 'p', sessionId: 's', prompt: '需求', order: 1, status: 'complete', reason: '', evidenceHash: 'current', sourceStart: 1, sourceEnd: 2, version: '1', cwd: '/workspace', captureId: 'c', evaluations: null, ...overrides };
}

describe('table progress', () => {
  it('counts low scores as prepared while keeping incomplete evidence separate', () => {
    const evaluations = [{ status: 'ready', evidenceHash: 'current', scores: [1, 2, 3, 4, 5] }, { status: 'needs_evidence', evidenceHash: 'current', scores: [null, 2, 3, 4, 5] }] as AnnotationRound['evaluations'];
    expect(getTableProgress({ rounds: [round({ evaluations: evaluations!.slice(0, 1) }), round({ evaluations: evaluations!.slice(1) })] })).toMatchObject({ prepared: 1, reviewed:2, missing:1, total: 2 });
  });

  it('does not count uncaptured, stale, pending or excluded rounds as prepared', () => {
    const evaluations = [{ status: 'ready', evidenceHash: 'old' }] as AnnotationRound['evaluations'];
    expect(getTableProgress({ rounds: [round(), round({ evaluations }), round({ status: 'pending' }), round({ status: 'excluded' })] })).toMatchObject({ prepared: 0, total: 3 });
    expect(getTableProgress({ rounds: [] })).toMatchObject({ prepared: 0, total: 0 });
  });

  it('counts a newly captured unreviewed round in the total to remove the completed mark', () => {
    const evaluations = [{ status: 'ready', evidenceHash: 'current', scores: [4, 4, 4, 4, 4] }] as AnnotationRound['evaluations'];
    expect(getTableProgress({ rounds: [round({ evaluations }), round()] })).toMatchObject({ prepared: 1, total: 2 });
  });

  it('collects total score 21 but marks truthful score 22 as not collected', () => {
    const score21 = [{ status: 'ready', evidenceHash: 'current', scores: [5, 4, 4, 4, 4] }] as AnnotationRound['evaluations'];
    const score22 = [{ status: 'ready', evidenceHash: 'current', scores: [5, 5, 4, 4, 4] }] as AnnotationRound['evaluations'];
    expect(getTableProgress({ rounds: [round({ evaluations: score21 })] })).toMatchObject({ prepared: 1, notCollected: 0, state: 'ready' });
    expect(getTableProgress({ rounds: [round({ evaluations: score22 })] })).toMatchObject({ prepared: 0, notCollected: 1, state: 'not_collected' });
  });
});
