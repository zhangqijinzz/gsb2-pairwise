import { getAnnotationJob } from '../../api/annotation';
import type { BackgroundJob } from '../../api/job';

const POLL_INTERVAL_MS = 600;

function delay(ms: number) {
  return new Promise<void>((resolve) => window.setTimeout(resolve, ms));
}

export async function waitForAnnotationJob(
  jobId: string,
  onProgress?: (job: BackgroundJob) => void,
): Promise<BackgroundJob> {
  for (;;) {
    const job = await getAnnotationJob(jobId);
    if (!job) throw new Error('后台任务不存在或已被清理');
    onProgress?.(job);
    if (job.status === 'done') return job;
    if (job.status === 'error') throw new Error(job.errorMessage || '后台任务执行失败');
    if (job.status === 'cancelled') throw new Error('后台任务已取消');
    await delay(POLL_INTERVAL_MS);
  }
}
