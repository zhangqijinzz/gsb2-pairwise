import { CheckCircle2, Loader2, AlertCircle } from 'lucide-react';
import type { TableProgress } from './tableProgress';

export function TableStatusBadge({ progress }: { progress?: TableProgress }) {
  if (!progress) return null;
  const state = progress.state ?? (progress.prepared === progress.total && progress.total>0 ? 'ready' : 'partial');
  const complete = state === 'ready';
  const busy = state === 'running' || state === 'pending';
  const labels = {pending:'排队中',running:progress.message?.includes('正在采集')?'采集中':'制表中',error:'制表失败',cancelled:'已取消',ready:'已制表',partial:'部分完成',needs_evidence:'待补证据',stale:'待重新复审',not_collected:'超过21分，不收录',captured:'待复审',empty:'待采集'};
  return (
    <span
      title={`${progress.error || progress.message || ''} 已复审 ${progress.reviewed ?? progress.prepared}/${progress.total} 轮，可导出 ${progress.prepared} 轮${progress.notCollected ? `，总分超过21不收录 ${progress.notCollected} 轮` : ''}；评分按真实证据保留`}
      className={`inline-flex items-center gap-1 rounded-full border px-2 py-0.5 text-[10px] font-semibold ${complete
        ? 'border-emerald-200 bg-emerald-50 text-emerald-700 dark:border-emerald-500/20 dark:bg-emerald-500/10 dark:text-emerald-300'
        : state === 'error' ? 'border-red-200 bg-red-50 text-red-700 dark:border-red-500/20 dark:bg-red-500/10 dark:text-red-300'
        : busy ? 'border-indigo-200 bg-indigo-50 text-indigo-700 dark:border-indigo-500/20 dark:bg-indigo-500/10 dark:text-indigo-300'
        : 'border-amber-200 bg-amber-50 text-amber-700 dark:border-amber-500/20 dark:bg-amber-500/10 dark:text-amber-300'}`}
    >
      {complete && <CheckCircle2 className="h-3 w-3" aria-hidden="true" />}
      {busy && <Loader2 className="h-3 w-3 animate-spin" aria-hidden="true" />}
      {state === 'error' && <AlertCircle className="h-3 w-3" aria-hidden="true" />}
      {labels[state]}{progress.total>0 && ` · 已复审 ${progress.reviewed ?? progress.prepared}/${progress.total} 轮`}
    </span>
  );
}
