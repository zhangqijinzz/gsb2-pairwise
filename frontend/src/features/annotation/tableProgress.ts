import type { AnnotationCase, AnnotationRound } from '../../api/annotation';

export type TableProgress = {
  prepared: number; total: number; reviewed?: number; missing?: number; stale?: number; notCollected?: number;
  state?: 'pending' | 'running' | 'error' | 'cancelled' | 'ready' | 'partial' | 'needs_evidence' | 'stale' | 'not_collected' | 'captured' | 'empty';
  message?: string; error?: string; quiet?: boolean; elapsed?: number; lastActivityAt?: number;
};

export function latestEvaluation(round: AnnotationRound) {
  return [...(round.evaluations ?? [])].sort((a,b) => (a.createdAt ?? 0)-(b.createdAt ?? 0)).at(-1);
}

export function evaluationScoreTotal(evaluation: { scores?: Array<number | null> }) {
  const scores = evaluation.scores;
  if (!scores || scores.length !== 5 || scores.some((score) => score === null || !Number.isInteger(score) || score < 1 || score > 5)) return null;
  return scores.reduce<number>((total, score) => total + (score ?? 0), 0);
}

// Match the saved evaluations accepted by the reviewed-only export. A pending
// round still belongs in the total so adding a round removes the complete badge.
export function getTableProgress(item: Pick<AnnotationCase, 'rounds' | 'preparation'>, now = Date.now()/1000): TableProgress {
  const rounds = item.rounds.filter((round) => round.status !== 'excluded');
  let prepared=0, reviewed=0, missing=0, stale=0, notCollected=0;
  for (const round of rounds) {
    const e=latestEvaluation(round);
    if (!e) continue;
    reviewed++;
    if (e.current === false || e.evidenceHash !== round.evidenceHash) { stale++; continue; }
    if (e.status === 'needs_evidence') missing++;
    if (round.status === 'complete' && e.status === 'ready') {
      const total=evaluationScoreTotal(e);
      if (total !== null && total <= 21) prepared++;
      else if (total !== null && total > 21) notCollected++;
    }
  }
  const p=item.preparation;
  const active=p?.status === 'pending' || p?.status === 'running';
  const failed=p?.status === 'error' || p?.status === 'cancelled';
  const state: TableProgress['state'] = active || failed ? p!.status as TableProgress['state']
    : stale ? 'stale' : missing ? 'needs_evidence' : prepared && prepared===rounds.length ? 'ready'
    : notCollected && notCollected===rounds.length ? 'not_collected'
    : prepared ? 'partial' : rounds.length ? 'captured' : 'empty';
  return {prepared,total:rounds.length,reviewed,missing,stale,notCollected,state,message:p?.message,error:p?.error,
    quiet: p?.status === 'running' && now-p.lastActivityAt>=120,
    elapsed: p ? Math.max(0,Math.floor((p.finishedAt || now)-p.startedAt)) : 0,lastActivityAt:p?.lastActivityAt};
}
