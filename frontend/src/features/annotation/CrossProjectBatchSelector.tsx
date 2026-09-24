import { Loader2 } from 'lucide-react';
import { useEffect, useState } from 'react';
import { listCases, type AnnotationCase } from '../../api/annotation';
import { getProjects, type ProjectConfig } from '../../api/config';

const SECONDARY_BUTTON = 'inline-flex items-center justify-center gap-2 rounded-xl border border-stone-200 bg-white px-3.5 py-2 text-sm font-semibold text-stone-700 transition hover:bg-stone-50 disabled:cursor-not-allowed disabled:opacity-40 dark:border-stone-700 dark:bg-stone-800 dark:text-stone-200 dark:hover:bg-stone-700';
const PRIMARY_BUTTON = 'inline-flex items-center justify-center gap-2 rounded-xl bg-slate-800 px-3.5 py-2 text-sm font-semibold text-white transition hover:bg-slate-700 disabled:cursor-not-allowed disabled:opacity-40 dark:bg-slate-100 dark:text-slate-900 dark:hover:bg-white';

type Props = {
  disabled: boolean;
  onSubmit: (taskIds: string[]) => Promise<void> | void;
};

function errorMessage(error: unknown) {
  return error instanceof Error ? error.message : String(error);
}

function folderName(path: string) {
  const normalized = path.replace(/[\\/]+$/, '');
  return normalized.split(/[\\/]/).pop() || 'repository';
}

export function CrossProjectBatchSelector({ disabled, onSubmit }: Props) {
  const [projects, setProjects] = useState<ProjectConfig[]>([]);
  const [selectedProjectIds, setSelectedProjectIds] = useState<string[]>([]);
  const [casesByProject, setCasesByProject] = useState<Record<string, AnnotationCase[]>>({});
  const [selectedTaskIds, setSelectedTaskIds] = useState<string[]>([]);
  const [loadingProjects, setLoadingProjects] = useState(true);
  const [loadingCaseProjects, setLoadingCaseProjects] = useState<string[]>([]);
  const [loadError, setLoadError] = useState('');

  useEffect(() => {
    let active = true;
    void getProjects()
      .then((items) => { if (active) setProjects(items); })
      .catch((error) => { if (active) setLoadError(`项目加载失败：${errorMessage(error)}`); })
      .finally(() => { if (active) setLoadingProjects(false); });
    return () => { active = false; };
  }, []);

  const selectProject = (project: ProjectConfig, checked: boolean) => {
    if (!checked) {
      const projectTaskIDs = new Set((casesByProject[project.id] ?? []).map((item) => item.taskId));
      setSelectedProjectIds((current) => current.filter((id) => id !== project.id));
      setSelectedTaskIds((current) => current.filter((id) => !projectTaskIDs.has(id)));
      return;
    }

    setSelectedProjectIds((current) => current.includes(project.id) ? current : [...current, project.id]);
    if (casesByProject[project.id] || loadingCaseProjects.includes(project.id)) return;
    setLoadingCaseProjects((current) => [...current, project.id]);
    setLoadError('');
    void listCases(project.id)
      .then((items) => setCasesByProject((current) => ({ ...current, [project.id]: items })))
      .catch((error) => {
        setLoadError(`${project.name}题目加载失败：${errorMessage(error)}`);
        setSelectedProjectIds((current) => current.filter((id) => id !== project.id));
      })
      .finally(() => setLoadingCaseProjects((current) => current.filter((id) => id !== project.id)));
  };

  const setTaskSelected = (taskID: string, checked: boolean) => {
    setSelectedTaskIds((current) => checked
      ? (current.includes(taskID) ? current : [...current, taskID])
      : current.filter((id) => id !== taskID));
  };

  const selectAllForProject = (projectID: string) => {
    const projectTaskIDs = (casesByProject[projectID] ?? []).map((item) => item.taskId);
    setSelectedTaskIds((current) => [...current, ...projectTaskIDs.filter((id) => !current.includes(id))]);
  };

  return (
    <section aria-label="跨项目批量审核" className="mb-5 rounded-2xl border border-stone-200 bg-white p-4 dark:border-stone-700 dark:bg-stone-900">
      <h2 className="font-semibold text-stone-800 dark:text-stone-100">选择要批量审核的项目和题目</h2>
      <p className="my-2 text-xs text-stone-500">先选择项目，再勾选需要审核的题目。项目和题目默认都不选，避免误操作。</p>
      {loadError && <p className="mb-3 text-sm text-red-600 dark:text-red-400">{loadError}</p>}
      {loadingProjects ? (
        <p className="flex items-center gap-2 py-3 text-sm text-stone-500"><Loader2 className="h-4 w-4 animate-spin" />正在加载项目</p>
      ) : (
        <div className="space-y-3">
          {projects.length === 0 && <p className="py-3 text-sm text-stone-500">暂无可选项目</p>}
          {projects.map((project) => {
            const selected = selectedProjectIds.includes(project.id);
            const projectCases = casesByProject[project.id] ?? [];
            const loadingCases = loadingCaseProjects.includes(project.id);
            return (
              <div key={project.id} className="rounded-xl border border-stone-200 p-3 dark:border-stone-700">
                <label className="flex items-center gap-2 text-sm font-semibold text-stone-800 dark:text-stone-100">
                  <input
                    type="checkbox"
                    aria-label={`选择项目 ${project.name}`}
                    checked={selected}
                    disabled={disabled}
                    onChange={(event) => selectProject(project, event.target.checked)}
                  />
                  {project.name}
                </label>
                {selected && (
                  <div className="mt-3 border-t border-stone-100 pt-3 dark:border-stone-800">
                    {loadingCases ? (
                      <p className="flex items-center gap-2 text-xs text-stone-500"><Loader2 className="h-3.5 w-3.5 animate-spin" />正在加载题目</p>
                    ) : projectCases.length === 0 ? (
                      <p className="text-xs text-stone-500">该项目暂无题目</p>
                    ) : (
                      <>
                        <button className={SECONDARY_BUTTON} disabled={disabled} onClick={() => selectAllForProject(project.id)}>全选 {project.name}</button>
                        <div className="mt-2 grid gap-2 md:grid-cols-2">
                          {projectCases.map((item) => (
                            <label key={item.taskId} className="flex items-start gap-2 rounded-lg bg-stone-50 p-2.5 text-sm dark:bg-stone-800/60 dark:text-stone-100">
                              <input
                                className="mt-0.5"
                                type="checkbox"
                                aria-label={`审核 ${item.taskName} ${folderName(item.sourcePath)}`}
                                checked={selectedTaskIds.includes(item.taskId)}
                                disabled={disabled}
                                onChange={(event) => setTaskSelected(item.taskId, event.target.checked)}
                              />
                              <span><span className="font-medium">{item.taskName}</span><span className="ml-2 text-xs text-stone-500">{folderName(item.sourcePath)}</span></span>
                            </label>
                          ))}
                        </div>
                      </>
                    )}
                  </div>
                )}
              </div>
            );
          })}
        </div>
      )}
      <div className="mt-4 flex justify-end">
        <button className={PRIMARY_BUTTON} disabled={disabled || selectedTaskIds.length === 0} onClick={() => void onSubmit(selectedTaskIds)}>
          {disabled && <Loader2 className="h-4 w-4 animate-spin" />}
          开始批量审核（{selectedTaskIds.length}）
        </button>
      </div>
    </section>
  );
}
