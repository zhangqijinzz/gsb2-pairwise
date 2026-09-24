import type { AnnotationCase, PairwiseReview } from '../../api/annotation';

const DECORATIVE_BRACKETS = /[『』「」【】《》]/;

// A task counts as "已生成 GSB" as soon as any review exists, which matches the
// board badge so the export list and the task card never disagree.
export function hasGeneratedPairwiseGsb(item: AnnotationCase): boolean {
  return item.mode === 'pairwise_gsb' && (item.pairwise?.reviews?.length ?? 0) > 0;
}

export function currentExportReview(item: AnnotationCase): PairwiseReview | undefined {
  const reviews = item.pairwise?.reviews ?? [];
  for (let index = reviews.length - 1; index >= 0; index -= 1) {
    const review = reviews[index];
    if (review.current === true && review.status === 'ready' && review.reason.trim() !== '') return review;
  }
  return undefined;
}

// Empty string means the backend will export the task; anything else explains why it would be skipped.
export function pairwiseExportBlocker(item: AnnotationCase): string {
  if (!hasGeneratedPairwiseGsb(item)) return '尚未生成 GSB';
  const review = currentExportReview(item);
  if (!review) {
    const latest = item.pairwise?.reviews.at(-1);
    if (!latest?.reason.trim()) return 'GSB 理由不完整，需重新审核';
    if (latest?.current === true && latest.status !== 'ready') return 'GSB 结论未就绪，需重新审核';
    return 'A/B 证据已变化，需重新审核';
  }
  if (DECORATIVE_BRACKETS.test(review.reason)) return 'GSB 理由含装饰括号，需重新审核';
  return '';
}

export function selectableExportIds(items: AnnotationCase[]): string[] {
  return items.filter((item) => pairwiseExportBlocker(item) === '').map((item) => item.taskId);
}
