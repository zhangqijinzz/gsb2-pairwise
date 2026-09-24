import { useEffect, useState } from 'react';
import {
  getConfig,
  getAnnotationSettings,
  setConfig,
  saveAnnotationSettings,
  type AnnotationReviewEngine,
} from '../../../api/config';
import {
  CONTAINER_SORT_PREFIXES_CONFIG_KEY,
  normalizeContainerSortPrefixes,
} from '../../annotation/containerSorting';

export function AnnotationRuntimeSettings() {
  const [reviewEngine, setReviewEngine] = useState<AnnotationReviewEngine>('deepseek');
  const [containerApiKey, setContainerApiKey] = useState('');
  const [hasContainerApiKey, setHasContainerApiKey] = useState(false);
  const [containerSortPrefixes, setContainerSortPrefixes] = useState('cyc');
  const [loading, setLoading] = useState(true);
  const [saving, setSaving] = useState(false);
  const [message, setMessage] = useState('');

  useEffect(() => {
    let current = true;
    Promise.all([
      getAnnotationSettings(),
      getConfig(CONTAINER_SORT_PREFIXES_CONFIG_KEY),
    ])
      .then(([settings, prefixes]) => {
        if (!current) return;
        setReviewEngine(settings.reviewEngine);
        setHasContainerApiKey(settings.hasContainerApiKey);
        setContainerSortPrefixes(prefixes.trim() || 'cyc');
      })
      .catch((error) => { if (current) setMessage(String(error)); })
      .finally(() => { if (current) setLoading(false); });
    return () => { current = false; };
  }, []);

  async function save() {
    setSaving(true);
    setMessage('');
    try {
      await saveAnnotationSettings(reviewEngine, containerApiKey.trim());
      const normalizedPrefixes = normalizeContainerSortPrefixes(containerSortPrefixes).join(',');
      await setConfig(CONTAINER_SORT_PREFIXES_CONFIG_KEY, normalizedPrefixes);
      if (containerApiKey.trim()) setHasContainerApiKey(true);
      setContainerApiKey('');
      setContainerSortPrefixes(normalizedPrefixes);
      setMessage('已保存，后续审核和复制的容器命令将使用新设置');
    } catch (error) {
      setMessage(String(error));
    } finally {
      setSaving(false);
    }
  }

  return <section className="mb-6 rounded-2xl border border-stone-200 bg-white p-5 dark:border-stone-800 dark:bg-stone-900">
    <h2 className="text-base font-semibold text-stone-800 dark:text-stone-100">审核与容器设置</h2>
    <p className="mt-2 text-sm text-stone-500">审核引擎对题卡 AI 复审和容器五维审核统一生效。DeepSeek 模式使用“大语言模型”中已配置的 DeepSeek V4 Flash API 提供商。</p>
    <label className="mt-4 block text-sm text-stone-600 dark:text-stone-300">审核引擎
      <select aria-label="审核引擎" className="mt-2 w-full rounded-xl border border-stone-200 bg-transparent px-3 py-2 dark:border-stone-700" value={reviewEngine} disabled={loading || saving} onChange={(event) => setReviewEngine(event.target.value as AnnotationReviewEngine)}>
        <option value="deepseek">DeepSeek V4 Flash</option>
        <option value="codex">Codex CLI</option>
      </select>
    </label>
    <label className="mt-4 block text-sm text-stone-600 dark:text-stone-300">容器 API Key
      <input aria-label="容器 API Key" type="password" autoComplete="off" className="mt-2 w-full rounded-xl border border-stone-200 bg-transparent px-3 py-2 dark:border-stone-700" value={containerApiKey} disabled={loading || saving} onChange={(event) => setContainerApiKey(event.target.value)} placeholder={hasContainerApiKey ? '已保存，留空表示不修改' : '输入复制到容器启动命令的 API Key'} />
    </label>
    {hasContainerApiKey && <p className="mt-2 text-xs text-emerald-600">容器 API Key 已保存</p>}
    <p className="mt-2 text-xs text-stone-500">密钥不会在设置页回显；复制容器启动命令时会自动带入。</p>
    <label className="mt-4 block text-sm text-stone-600 dark:text-stone-300">容器排序前缀
      <input aria-label="容器排序前缀" type="text" className="mt-2 w-full rounded-xl border border-stone-200 bg-transparent px-3 py-2 dark:border-stone-700" value={containerSortPrefixes} disabled={loading || saving} onChange={(event) => setContainerSortPrefixes(event.target.value)} placeholder="cyc，可用逗号或空格填写多个" />
    </label>
    <p className="mt-2 text-xs text-stone-500">按填写顺序分组，每组内按容器名称中的数字降序排列；未匹配的容器排在最后。</p>
    <button className="mt-3 rounded-xl bg-slate-800 px-4 py-2 text-sm text-white disabled:opacity-40 dark:bg-slate-200 dark:text-slate-900" disabled={loading || saving} onClick={() => void save()}>{saving ? '保存中…' : '保存审核与容器设置'}</button>
    {message && <p role="status" className="mt-2 text-sm text-stone-500">{message}</p>}
  </section>;
}
