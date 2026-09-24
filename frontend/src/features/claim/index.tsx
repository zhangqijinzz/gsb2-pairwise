import { useClaimProject } from './hooks/useClaimProject';
import QuestionBankPanel from './components/QuestionBankPanel';

export default function Claim() {
  const project = useClaimProject();

  return (
    <div className="mx-auto max-w-4xl p-4 sm:p-6 md:p-8">
      <header className="mb-4 sm:mb-5">
        <div>
          <h1 className="text-xl sm:text-2xl font-bold tracking-tight text-stone-900 dark:text-stone-50">
            领题
          </h1>
          <p className="mt-1 text-xs text-stone-500 dark:text-stone-400">
            {project.activeProject?.name ?? (project.loading ? '加载中…' : '暂未激活项目')}
          </p>
        </div>
      </header>

      <QuestionBankPanel project={project} />
    </div>
  );
}
